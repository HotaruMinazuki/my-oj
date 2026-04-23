-- migrations/001_init.sql
-- Full schema for the OJ platform.
-- Idempotent (IF NOT EXISTS / CREATE INDEX IF NOT EXISTS).
-- Automatically executed by postgres:16-alpine on first container start
-- because this file is mounted to /docker-entrypoint-initdb.d.

BEGIN;

-- ── Users ─────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS users (
    id            BIGSERIAL   PRIMARY KEY,
    username      TEXT        NOT NULL UNIQUE,
    email         TEXT        NOT NULL UNIQUE,
    password_hash TEXT        NOT NULL,
    role          TEXT        NOT NULL DEFAULT 'contestant',   -- admin | contestant | guest
    organization  TEXT        NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ── Problems ──────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS problems (
    id             BIGSERIAL   PRIMARY KEY,
    title          TEXT        NOT NULL,
    statement      TEXT        NOT NULL DEFAULT '',            -- Markdown; large blobs via CDN
    time_limit_ms  BIGINT      NOT NULL DEFAULT 2000,
    mem_limit_kb   BIGINT      NOT NULL DEFAULT 262144,        -- 256 MB
    judge_type     TEXT        NOT NULL DEFAULT 'standard',   -- standard|special|interactive|communication
    -- JSONB: carries checker_path, interactor_path, comm_channels, etc.
    judge_config   JSONB       NOT NULL DEFAULT '{}',
    -- NULL = all configured languages allowed; otherwise a JSON array of Language strings.
    allowed_langs  JSONB,
    is_public      BOOLEAN     NOT NULL DEFAULT FALSE,
    author_id      BIGINT      NOT NULL REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ── Test Cases ────────────────────────────────────────────────────────────────
-- Paths are relative filenames inside the testcase zip (e.g. "1.in", "1.out").
-- The judger's testcase cache resolves them to absolute local paths at Stage 0.
CREATE TABLE IF NOT EXISTS test_cases (
    id          BIGSERIAL PRIMARY KEY,
    problem_id  BIGINT    NOT NULL REFERENCES problems(id) ON DELETE CASCADE,
    group_id    INT       NOT NULL DEFAULT 1,   -- IOI subtask group
    ordinal     INT       NOT NULL,             -- execution order within a group
    input_path  TEXT      NOT NULL,
    output_path TEXT      NOT NULL DEFAULT '',  -- empty for interactive problems
    score       INT       NOT NULL DEFAULT 0,
    is_sample   BOOLEAN   NOT NULL DEFAULT FALSE,
    UNIQUE (problem_id, group_id, ordinal)
);

CREATE INDEX IF NOT EXISTS idx_test_cases_problem
    ON test_cases (problem_id);

-- ── Contests ──────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS contests (
    id                  BIGSERIAL   PRIMARY KEY,
    title               TEXT        NOT NULL,
    description         TEXT        NOT NULL DEFAULT '',
    contest_type        TEXT        NOT NULL DEFAULT 'ICPC',  -- ICPC|OI|IOI|Team|Custom
    status              TEXT        NOT NULL DEFAULT 'draft', -- draft|ready|running|frozen|ended
    start_time          TIMESTAMPTZ NOT NULL,
    end_time            TIMESTAMPTZ NOT NULL,
    freeze_time         TIMESTAMPTZ,            -- NULL = no scoreboard freeze
    -- JSONB: strategy-specific knobs, e.g. {"penalty_minutes": 20}
    settings            JSONB       NOT NULL DEFAULT '{}',
    is_public           BOOLEAN     NOT NULL DEFAULT FALSE,
    allow_late_register BOOLEAN     NOT NULL DEFAULT FALSE,
    organizer_id        BIGINT      NOT NULL REFERENCES users(id),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_contests_status
    ON contests (status);

-- ── Contest ↔ Problem mapping ─────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS contest_problems (
    contest_id  BIGINT NOT NULL REFERENCES contests(id) ON DELETE CASCADE,
    problem_id  BIGINT NOT NULL REFERENCES problems(id) ON DELETE CASCADE,
    label       TEXT   NOT NULL,        -- display letter: "A", "B", "1"
    max_score   INT    NOT NULL DEFAULT 100,
    ordinal     INT    NOT NULL,        -- column order on the scoreboard
    PRIMARY KEY (contest_id, problem_id)
);

-- ── Contest participants ───────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS contest_participants (
    contest_id    BIGINT      NOT NULL REFERENCES contests(id) ON DELETE CASCADE,
    user_id       BIGINT      NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    team_id       BIGINT,                -- non-null for team contests
    registered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (contest_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_contest_participants_user
    ON contest_participants (user_id);

-- ── Submissions ───────────────────────────────────────────────────────────────
-- High-write table: every code submission lands here first (status=Pending),
-- then the judger result updates it in-place.
CREATE TABLE IF NOT EXISTS submissions (
    id                BIGSERIAL   PRIMARY KEY,
    user_id           BIGINT      NOT NULL REFERENCES users(id),
    problem_id        BIGINT      NOT NULL REFERENCES problems(id),
    contest_id        BIGINT      REFERENCES contests(id),    -- NULL = practice
    language          TEXT        NOT NULL,
    -- MinIO object key: "sources/{userID}/{problemID}/{uuid}.{ext}"
    source_code_path  TEXT        NOT NULL,
    status            TEXT        NOT NULL DEFAULT 'Pending',
    score             INT         NOT NULL DEFAULT 0,
    time_used_ms      BIGINT      NOT NULL DEFAULT 0,
    mem_used_kb       BIGINT      NOT NULL DEFAULT 0,
    compile_log       TEXT        NOT NULL DEFAULT '',
    judge_message     TEXT        NOT NULL DEFAULT '',
    -- JSONB: per-test-case verdicts; NULL until judging completes.
    test_case_results JSONB,
    judge_node_id     TEXT        NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Index 1: scoreboard rebuild — all submissions for a given contest+problem
CREATE INDEX IF NOT EXISTS idx_submissions_contest_problem
    ON submissions (contest_id, problem_id)
    WHERE contest_id IS NOT NULL;

-- Index 2: user submission history page — newest first
CREATE INDEX IF NOT EXISTS idx_submissions_user_created
    ON submissions (user_id, created_at DESC);

-- Index 3: background retry job — find stuck Pending submissions
CREATE INDEX IF NOT EXISTS idx_submissions_pending
    ON submissions (status, created_at)
    WHERE status = 'Pending';

COMMIT;
