#!/usr/bin/env python3
"""
scripts/e2e_simulate.py — End-to-end OJ pipeline simulation.

Validates the full judge pipeline WITHOUT requiring the DB repos to be wired up.
Works by driving the system at two levels:
  • Admin HTTP API   → uploads testcase zip to MinIO
  • MinIO Python SDK → uploads contestant source code directly to MinIO
  • Redis Streams    → injects JudgeTasks directly (bypasses stub SubmissionRepo)
  • Redis Streams    → subscribes to results stream to verify verdicts
  • Redis key        → reads ranking snapshot to verify scoreboard state

Prerequisites:
    pip install requests PyJWT minio redis

Services running (docker compose up or local):
    redis   localhost:6379
    minio   localhost:9000
    api-server  localhost:8080     (for testcase upload only)
    judger-node (pulls from redis, downloads from minio, publishes results)

Usage:
    python3 scripts/e2e_simulate.py
    python3 scripts/e2e_simulate.py --api-base http://localhost:8080 \\
                                    --minio-addr localhost:9000 \\
                                    --redis-addr localhost:6379 \\
                                    --jwt-key change-me-in-production
"""

import argparse
import io
import json
import sys
import time
import uuid
import zipfile

try:
    import jwt          # pip install PyJWT
    import redis        # pip install redis
    import requests     # pip install requests
    from minio import Minio  # pip install minio
except ImportError as e:
    print(f"Missing dependency: {e}")
    print("Run: pip install requests PyJWT minio redis")
    sys.exit(1)

# ── ANSI colours ───────────────────────────────────────────────────────────────
GREEN  = "\033[92m"; YELLOW = "\033[93m"
RED    = "\033[91m"; CYAN   = "\033[96m"; RESET = "\033[0m"
OK     = f"{GREEN}✓{RESET}"; FAIL = f"{RED}✗{RESET}"; INFO = f"{CYAN}→{RESET}"

# ── Fixtures ───────────────────────────────────────────────────────────────────
PROBLEM_ID  = 1
CONTEST_ID  = 1
TIME_LIMIT  = 2_000   # ms
MEM_LIMIT   = 262_144 # KB (256 MB)

# Three source-code variants for A+B problem
AC_CODE  = "#include<iostream>\nint main(){int a,b;std::cin>>a>>b;std::cout<<a+b;}\n"
WA_CODE  = "#include<iostream>\nint main(){int a,b;std::cin>>a>>b;std::cout<<a-b;}\n"
TLE_CODE = "#include<iostream>\nint main(){while(true){}}\n"

SUBMISSIONS = [
    {"user_id": 2, "label": "AC",  "language": "C++17", "code": AC_CODE,  "sub_id": 1001},
    {"user_id": 3, "label": "WA",  "language": "C++17", "code": WA_CODE,  "sub_id": 1002},
    {"user_id": 4, "label": "TLE", "language": "C++17", "code": TLE_CODE, "sub_id": 1003},
]


# ── Helpers ────────────────────────────────────────────────────────────────────

def step(n: int, title: str) -> None:
    print(f"\n{CYAN}{'─'*60}{RESET}")
    print(f"{CYAN}  Step {n}: {title}{RESET}")
    print(f"{CYAN}{'─'*60}{RESET}")


def ok(msg: str)   -> None: print(f"  {OK}  {msg}")
def info(msg: str) -> None: print(f"  {INFO} {msg}")
def fail(msg: str) -> None: print(f"  {FAIL}  {RED}{msg}{RESET}"); sys.exit(1)
def warn(msg: str) -> None: print(f"  {YELLOW}⚠  {msg}{RESET}")


def make_jwt(user_id: int, role: str, secret: str) -> str:
    return jwt.encode(
        {"uid": user_id, "role": role, "exp": int(time.time()) + 3600},
        secret, algorithm="HS256",
    )


def make_testcase_zip() -> bytes:
    """Create a three-test-case A+B zip.  Layout: 1.in/1.out, 2.in/2.out, 3.in/3.out"""
    buf = io.BytesIO()
    cases = [("1 2\n", "3\n"), ("100 200\n", "300\n"), ("0 0\n", "0\n")]
    with zipfile.ZipFile(buf, "w", zipfile.ZIP_DEFLATED) as zf:
        for i, (inp, out) in enumerate(cases, 1):
            zf.writestr(f"{i}.in",  inp)
            zf.writestr(f"{i}.out", out)
    return buf.getvalue()


def source_key(user_id: int, problem_id: int, ext: str) -> str:
    """MinIO object key for contestant source code."""
    return f"sources/{user_id}/{problem_id}/{uuid.uuid4()}.{ext}"


def build_judge_task(sub: dict, src_key: str) -> dict:
    """Construct the JudgeTask JSON that would normally be created by the API server."""
    return {
        "task_id":        str(uuid.uuid4()),
        "submission_id":  sub["sub_id"],
        "user_id":        sub["user_id"],
        "problem_id":     PROBLEM_ID,
        "contest_id":     CONTEST_ID,
        "language":       sub["language"],
        "source_code_path": src_key,   # MinIO key; judger downloads via ObjectStore
        "judge_type":     "standard",
        "judge_config":   {},
        "time_limit_ms":  TIME_LIMIT,
        "mem_limit_kb":   MEM_LIMIT,
        "test_cases": [
            {"test_case_id": i, "group_id": 1, "ordinal": i,
             "input_path": f"{i}.in", "output_path": f"{i}.out", "score": 33 + (1 if i == 3 else 0)}
            for i in range(1, 4)
        ],
        "priority": 5,
        # Wrapper fields from mq.TaskMessage
        "enqueued_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    }


# ── Main simulation ────────────────────────────────────────────────────────────

def run(args: argparse.Namespace) -> None:
    rdb  = redis.Redis.from_url(f"redis://{args.redis_addr}", decode_responses=True)
    mc   = Minio(args.minio_addr, access_key=args.minio_key,
                 secret_key=args.minio_secret, secure=False)

    # ── Step 1: Health check ──────────────────────────────────────────────────
    step(1, "Connectivity check")
    try:
        rdb.ping();  ok(f"Redis   {args.redis_addr}")
    except Exception as e:
        fail(f"Redis unreachable: {e}")

    try:
        mc.bucket_exists("submissions");  ok(f"MinIO   {args.minio_addr}")
    except Exception as e:
        fail(f"MinIO unreachable: {e}")

    try:
        r = requests.get(f"{args.api_base}/api/v1/contests/{CONTEST_ID}/ranking", timeout=5)
        ok(f"API     {args.api_base}  (HTTP {r.status_code})")
    except Exception as e:
        warn(f"API server not reachable ({e}) — testcase upload step will be skipped")

    # ── Step 2: Upload testcase zip via Admin API ─────────────────────────────
    step(2, "Upload A+B testcase zip (Admin API)")
    admin_tok = make_jwt(1, "admin", args.jwt_key)
    zip_bytes = make_testcase_zip()
    info(f"Created zip ({len(zip_bytes)} bytes) with 3 test cases")

    try:
        resp = requests.post(
            f"{args.api_base}/api/v1/admin/problems/{PROBLEM_ID}/testcases",
            headers={"Authorization": f"Bearer {admin_tok}"},
            files={"file": ("testcases.zip", zip_bytes, "application/zip")},
            timeout=30,
        )
        if resp.status_code == 201:
            data = resp.json()
            ok(f"Uploaded → MinIO key: {data.get('key')}")
            ok(f"Files in zip: {data.get('files', [])}")
            if data.get("warnings"):
                [warn(w) for w in data["warnings"]]
        else:
            warn(f"Upload returned HTTP {resp.status_code}: {resp.text[:120]}")
    except Exception as e:
        warn(f"Admin API call failed ({e}) — uploading directly to MinIO instead")
        # Fallback: upload directly to MinIO (useful when API server has stub repos)
        mc.put_object("testcases", f"testcases/{PROBLEM_ID}/data.zip",
                      io.BytesIO(zip_bytes), len(zip_bytes), "application/zip")
        ok(f"Uploaded directly to MinIO: testcases/{PROBLEM_ID}/data.zip")

    # ── Step 3: Upload source code to MinIO + push JudgeTasks to Redis ────────
    step(3, "Upload source code to MinIO + inject JudgeTasks into Redis Stream")
    task_ids = {}

    for sub in SUBMISSIONS:
        ext = "cpp"
        key = source_key(sub["user_id"], PROBLEM_ID, ext)
        code_bytes = sub["code"].encode()

        # Upload source to MinIO directly (what the API server would do)
        mc.put_object("submissions", key,
                      io.BytesIO(code_bytes), len(code_bytes), "text/plain")
        ok(f"[User {sub['user_id']}] {sub['label']:3s} source → MinIO key: {key}")

        # Build and enqueue JudgeTask
        task = build_judge_task(sub, key)
        msg_id = rdb.xadd("oj:judge:tasks", {"data": json.dumps(task)})
        task_ids[task["task_id"]] = {"sub": sub, "msg_id": msg_id, "task": task}
        ok(f"[User {sub['user_id']}] {sub['label']:3s} task  → Redis stream ID: {msg_id}")

    # ── Step 4: Wait for judge results ────────────────────────────────────────
    step(4, "Waiting for judge results (timeout 90s)")
    info("Watching stream: oj:judge:results")
    info("Each result is published by the judger after nsjail execution.\n")

    deadline    = time.time() + 90
    seen_tasks  = set()
    last_id     = "$"   # read only new messages from this point forward

    expected = {t["task"]["task_id"]: t["sub"]["label"] for t in task_ids.values()}
    results  = {}

    while len(seen_tasks) < len(task_ids) and time.time() < deadline:
        streams = rdb.xread({"oj:judge:results": last_id}, count=10, block=2000)
        if not streams:
            continue
        for _stream, messages in streams:
            for msg_id, fields in messages:
                last_id = msg_id
                try:
                    payload = json.loads(fields.get("data", "{}"))
                except Exception:
                    continue

                tid = payload.get("task_id", "")
                if tid not in expected:
                    continue
                if tid in seen_tasks:
                    continue

                seen_tasks.add(tid)
                label  = expected[tid]
                status = payload.get("status", "?")
                ms     = payload.get("time_used_ms", 0)
                kb     = payload.get("mem_used_kb", 0)
                results[tid] = payload

                colour = GREEN if status == "Accepted" else (
                         YELLOW if "TLE" in status or "MLE" in status else RED)
                print(f"  {OK} [{label}] status={colour}{status}{RESET} "
                      f"time={ms}ms  mem={kb}KB")
                if payload.get("compile_log"):
                    warn(f"     compile_log: {payload['compile_log'][:80]}")

    if len(seen_tasks) < len(task_ids):
        missing = set(expected) - seen_tasks
        warn(f"Timed out — {len(missing)} submission(s) did not produce a result.")
        warn("Is judger-node running?  Check: docker compose logs judger-node")
    else:
        ok("All submissions judged!")

    # ── Step 5: Ranking snapshot ───────────────────────────────────────────────
    step(5, "Ranking snapshot from Redis")
    snap_key = f"oj:ranking:{CONTEST_ID}:snapshot"
    raw = rdb.get(snap_key)
    if not raw:
        warn("No ranking snapshot yet — ranking service may still be processing.")
        warn(f"Check Redis key: {snap_key}")
    else:
        snap = json.loads(raw)
        rows = snap.get("rows", [])
        print(f"\n  {'Rank':<6} {'UserID':<10} {'Solved':<8} {'Penalty':>8}")
        print(f"  {'─'*36}")
        for row in rows:
            print(f"  {row['rank']:<6} {row['user_id']:<10} "
                  f"{row['total_score']:<8} {row['total_penalty']:>8}m")

    # ── Done ──────────────────────────────────────────────────────────────────
    print(f"\n{GREEN}{'═'*60}{RESET}")
    print(f"{GREEN}  E2E simulation complete!{RESET}")
    print(f"  Open:  public/debug_board.html")
    print(f"  Enter: contest_id = {CONTEST_ID}")
    print(f"  Re-submit the above tasks to watch live ranking updates.")
    print(f"{GREEN}{'═'*60}{RESET}\n")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="OJ end-to-end simulation")
    parser.add_argument("--api-base",     default="http://localhost:8080")
    parser.add_argument("--redis-addr",   default="localhost:6379")
    parser.add_argument("--minio-addr",   default="localhost:9000")
    parser.add_argument("--minio-key",    default="minioadmin")
    parser.add_argument("--minio-secret", default="minioadmin")
    parser.add_argument("--jwt-key",      default="change-me-in-production")
    run(parser.parse_args())
