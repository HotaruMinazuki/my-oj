# OJ 使用文档

一个 ICPC 风格的在线评测系统：Go 后端 + Vue 3 前端 + nsjail 沙盒评测机 + Redis Streams 任务队列 + PostgreSQL + MinIO。

---

## 目录

1. [系统组件](#系统组件)
2. [快速部署](#快速部署)
3. [首次初始化](#首次初始化)
4. [管理员工作流](#管理员工作流)
5. [选手使用指南](#选手使用指南)
6. [完整比赛流程示例](#完整比赛流程示例)
7. [运维常用命令](#运维常用命令)
8. [故障排查](#故障排查)
9. [API 参考](#api-参考)

---

## 系统组件

| 服务                 | 端口          | 说明                            |
| ------------------ | ----------- | ----------------------------- |
| `frontend` (nginx) | **8088**    | Vue 3 单页应用                    |
| `api-server`       | 8080 (内网)   | HTTP + WebSocket API          |
| `judger-node`      | —           | 评测机（nsjail 沙盒），可水平扩展          |
| `postgres`         | 5432        | 用户、题目、比赛、提交                   |
| `redis`            | 6379        | 任务队列（Streams）+ 排行榜推送（Pub/Sub） |
| `minio`            | 9000 / 9001 | 源代码与测试数据对象存储                  |

用户访问 `http://服务器IP:8088` 即可，其它端口生产环境建议用防火墙挡住。

---

## 快速部署

### 1. 环境要求

- Linux（内核 ≥ 5.0，需支持 cgroup v2 / user namespaces 供 nsjail 使用）
- Docker ≥ 20.10
- Docker Compose v2
- 至少 2 GB 内存、10 GB 磁盘

### 2. 克隆代码

```bash
git clone <your-repo-url> my-oj
cd my-oj
```

### 3. **必须**修改密钥

默认 `docker-compose.yml` 里的密钥都是占位符，**上线前必须改掉**。推荐用 `.env` 覆盖：

```bash
cat > .env <<EOF
JWT_SECRET=$(openssl rand -hex 32)
POSTGRES_PASSWORD=$(openssl rand -hex 16)
MINIO_ROOT_USER=oj_admin
MINIO_ROOT_PASSWORD=$(openssl rand -hex 24)
EOF
```

然后在 `docker-compose.yml` 里把硬编码的密钥改成 `${JWT_SECRET}` 等引用。

### 4. 启动

```bash
docker compose up -d --build
```

首次启动会：
- 自动执行 `migrations/001_init.sql` 建表
- 自动创建 MinIO 桶（`oj-source`, `oj-testcases`）
- 拉起 API、前端、评测机

### 5. 验证

```bash
# 看服务是否都 Up (healthy)
docker compose ps

# 看 API 日志
docker compose logs -f api-server

# 访问前端
curl -I http://localhost:8088
```

打开浏览器访问 `http://服务器IP:8088`，看到首页即成功。

---

## 首次初始化

### 创建第一个管理员

系统启动后数据库里没有任何用户。你需要：

**方式 A（推荐）：** 前端注册一个普通账号，然后用 SQL 提升为 admin：

```bash
# 1. 在前端 /register 注册账号，比如用户名 "root"

# 2. 提升为管理员
docker compose exec postgres psql -U oj -d oj \
  -c "UPDATE users SET role='admin' WHERE username='root';"
```

刷新前端页面，登录后顶栏会看到"管理后台"入口。

---

## 管理员工作流

所有管理功能都在 `/admin`。顺序是：

### Step 1. 创建题目

**管理后台 → 题目管理 → 新建题目**

- 标题、题面（支持 Markdown）
- 时限（ms）、内存（KB）
- 评测类型：
  - `standard` — diff 比较输出（最常用）
  - `special` — Special Judge（自定义 checker）
  - `interactive` — 交互题
  - `communication` — 通信题
- **公开** 开关：关闭则只有管理员能看到（草稿状态）

### Step 2. 上传测试数据

题目列表里点"上传测试数据"，上传 `.zip` 包，结构必须是：

```
testcases.zip
├── 1.in
├── 1.out
├── 2.in
├── 2.out
├── ...
```

> ⚠️ `.in` 与 `.out` 必须成对，编号从 1 开始递增，不能跳号。

### Step 3. 创建比赛

**管理后台 → 比赛管理 → 新建比赛**

- 赛制：ICPC（罚时制）/ OI（总分制）/ IOI（逐测试点得分）
- 开始 / 结束时间
- **封榜时间**（可选）：ICPC 最后一小时封榜用
- 公开 开关 / 允许补报名 开关

### Step 4. 给比赛加题目（**新加的功能**）

比赛列表里点"管理题目"：
- 左侧显示已加入的题目
- 右下选择题目 + 输入 Label（如 A、B、C，系统会自动建议下一个空闲字母）+ 设置分值
- 可随时移除未使用的题目

> Label 在排行榜上就是题目列的表头，同一个比赛内不能重复。

### Step 5. 选手报名 & 比赛进行

选手在比赛详情页点"报名参赛"即可。系统会按 `start_time` 自动把 `status` 从 `ready` 切到 `running`；到 `freeze_time` 切到 `frozen`；到 `end_time` 切到 `ended`。

### Step 6. 滚榜（仅 ICPC）

比赛结束后到 **管理后台 → 比赛管理 → 滚榜**：
- 点一次"揭晓下一个"，解冻当前排名最低且有未公开提交的队伍的一道题
- 前端排行榜会实时推送动画，适合现场演出用
- 直到所有冻结数据都解冻完毕

---

## 选手使用指南

### 注册 & 登录

`/register` 填用户名（3–32 字符）、邮箱、密码（≥ 6 位）、组织/学校（选填）。

### 刷题（非比赛）

1. `/problems` 看题库
2. 点进题目后右侧是 Monaco 编辑器
3. 选语言 → 写代码 → **Ctrl+Enter** 提交（或点按钮）
4. 代码会自动按 `题目 ID + 语言` 存到浏览器本地草稿，Ctrl+S 强制保存
5. 提交后右侧显示最近评测状态，点"详情"进入逐测试点结果

### 参加比赛

1. `/contests` 看比赛列表，状态徽标一目了然（进行中 / 即将开始 / 已结束）
2. 进比赛详情页 → 报名参赛
3. 比赛开始后题目列表的"提交"按钮亮起，点击弹出提交对话框
4. 实时排行榜：点顶部"排行榜"按钮，WebSocket 实时推送，自己的行会高亮蓝色边框

### 编辑器快捷键

| 快捷键 | 功能 |
|--------|------|
| `Ctrl+Enter` / `Cmd+Enter` | 提交代码 |
| `Ctrl+S` / `Cmd+S` | 强制保存草稿 |
| 顶栏 ±号 | 调整字号 |
| 顶栏日/月图标 | 切换深色/浅色主题 |
| 顶栏垃圾桶 | 清空当前草稿 |

---

## 完整比赛流程示例

假设你要办一场 3 小时、5 题的 ICPC 比赛，现在是周五晚 8 点，比赛定在周六下午 2 点。

```
周五 20:00  admin 登录 → 创建 5 道题 → 每题上传测试数据
周五 20:30  新建比赛 "周赛 #42"
              start_time = 周六 14:00
              end_time   = 周六 17:00
              freeze_time = 周六 16:00  （最后 1 小时封榜）
              contest_type = ICPC
              is_public = true
周五 20:40  管理题目 → 把 5 道题分别用 Label A/B/C/D/E 加入
周五 21:00  发布比赛通知（可选，系统不带通知功能，自行发群）

周六 13:00  选手进入比赛详情页报名
周六 14:00  系统自动将状态切为 running，"提交"按钮亮起
周六 16:00  系统自动切为 frozen，封榜后的提交不再公开显示排名变化
周六 17:00  系统自动切为 ended，提交按钮禁用

周六 17:05  admin → 比赛管理 → 滚榜
              点一次揭晓一次，观众席前端动画滚动
              直到全部解冻 → 接口返回 {done: true}

周六 17:30  颁奖
```

---

## 运维常用命令

```bash
# 查看所有服务状态
docker compose ps

# 查看某个服务日志（-f 跟踪）
docker compose logs -f api-server
docker compose logs -f judger-node

# 重启单个服务
docker compose restart api-server

# 重建前端（改完前端代码后）
docker compose up -d --build frontend

# 扩展评测机（并发评测）
docker compose up -d --scale judger-node=4

# 进入数据库
docker compose exec postgres psql -U oj -d oj

# 备份数据库
docker compose exec postgres pg_dump -U oj oj > backup-$(date +%F).sql

# 恢复数据库
cat backup.sql | docker compose exec -T postgres psql -U oj -d oj

# 查看当前评测任务队列深度
docker compose exec redis redis-cli XLEN oj:queue:judge_tasks

# 查看 MinIO 里的测试数据桶
# 浏览器打开 http://服务器IP:9001  用 .env 里的 MINIO_ROOT_USER 登录

# 彻底清空数据（谨慎！）
docker compose down -v
```

### 常用 SQL

```sql
-- 提升用户为管理员
UPDATE users SET role='admin' WHERE username='xxx';

-- 查看某个用户的所有提交
SELECT id, problem_id, language, status, created_at
FROM submissions WHERE user_id=(SELECT id FROM users WHERE username='xxx')
ORDER BY id DESC LIMIT 20;

-- 查看 stuck 在 Pending 的提交（评测机可能挂了）
SELECT id, user_id, problem_id, created_at
FROM submissions
WHERE status IN ('Pending','Compiling','Judging')
  AND created_at < NOW() - INTERVAL '5 minutes';

-- 强制把某个卡住的提交标记为 SystemError
UPDATE submissions SET status='SystemError', judge_message='manually cancelled'
WHERE id=12345;
```

---

## 故障排查

### 1. 前端打开是空白或 502

```bash
docker compose ps
# 看 frontend 与 api-server 是否都 healthy
# 看 frontend 日志：
docker compose logs frontend
```

### 2. 提交后 status 一直是 Pending

评测机没消费任务。检查：

```bash
# 评测机是否在跑
docker compose logs -f judger-node

# 队列有没有积压
docker compose exec redis redis-cli XLEN oj:queue:judge_tasks
```

常见原因：
- **nsjail 启动失败** — 宿主机内核太旧（< 5.0）或不支持 user namespaces。`docker compose logs judger-node` 会报错。
- **cgroup v2 未挂载** — `docker-compose.yml` 里 `judger-node` 挂载了 `/sys/fs/cgroup`，宿主机必须是 cgroup v2。`mount | grep cgroup2` 验证。
- **MinIO 连不上** — 评测机需要从 MinIO 下源代码和测试数据。

### 3. 排行榜 WebSocket 一直"连接中"

检查前端 nginx 是否正确代理 `/ws`：

```bash
docker compose exec frontend cat /etc/nginx/conf.d/default.conf | grep -A5 "/ws"
```

应该看到 `proxy_pass http://api-server:8080;` + `proxy_http_version 1.1;` + `Upgrade` 头。

### 4. "problem already in contest"

管理题目时添加同一题两次，正常报错。前端会把已加入的题目从下拉里过滤掉，如果仍出现说明刷新前做了 stale 操作，关闭对话框重开即可。

### 5. 忘记管理员密码

SQL 重置（密码存储格式是 `sha256(salt+password):salt_hex`，无法直接解密，必须替换整行）：

```bash
# 简单做法：用前端注册新账号，再提升为 admin，原账号可以删掉或留着
```

### 6. 数据库表没建

检查首次启动日志：

```bash
docker compose logs postgres | grep -i "001_init"
```

PG 只会在**数据目录为空**时跑 `/docker-entrypoint-initdb.d` 里的脚本。如果你升级了 migration 想重跑，必须先 `docker compose down -v` 销毁 volume（会丢数据）。

生产环境后续 migration 建议手动：

```bash
cat migrations/002_xxx.sql | docker compose exec -T postgres psql -U oj -d oj
```

---

## API 参考

前端统一通过 `/api/v1/*` 调用，所有需要认证的路由走 `Authorization: Bearer <JWT>` 头。

### 认证

| 方法 | 路径 | 认证 | 说明 |
|------|------|------|------|
| POST | `/auth/register` | — | 注册，返回 token + user |
| POST | `/auth/login` | — | 登录 |
| GET  | `/auth/me` | ✅ | 当前用户信息 |

### 题目

| 方法 | 路径 | 认证 | 说明 |
|------|------|------|------|
| GET | `/problems?page=&size=` | 可选 | 题目列表（非管理员只看公开题） |
| GET | `/problems/:id` | 可选 | 题目详情 |
| POST | `/admin/problems` | 🔒 admin | 创建题目 |
| POST | `/admin/problems/:id/testcases` | 🔒 admin | 上传测试数据 zip |

### 比赛

| 方法 | 路径 | 认证 | 说明 |
|------|------|------|------|
| GET | `/contests?page=&size=` | 可选 | 比赛列表 |
| GET | `/contests/:contest_id` | 可选 | 比赛详情 |
| GET | `/contests/:contest_id/problems` | — | 比赛题目列表 |
| GET | `/contests/:contest_id/ranking` | — | 排行榜快照 |
| GET | `/contests/:contest_id/ranking/me` | ✅ | 当前用户在比赛中的排名 |
| POST | `/contests/:contest_id/register` | ✅ | 报名 |
| POST | `/admin/contests` | 🔒 admin | 创建比赛 |
| POST | `/admin/contests/:contest_id/problems` | 🔒 admin | 把题目加入比赛 |
| DELETE | `/admin/contests/:contest_id/problems/:problem_id` | 🔒 admin | 从比赛移除题目 |
| POST | `/admin/contests/:contest_id/unfreeze-next` | 🔒 admin | 滚榜（揭晓下一个）|

### 提交

| 方法 | 路径 | 认证 | 说明 |
|------|------|------|------|
| POST | `/contests/:contest_id/submissions` | ✅ | 比赛中提交 |
| POST | `/submissions` | ✅ | 练习模式提交 |
| GET | `/submissions/:id` | ✅ | 提交详情（含逐测试点） |

### WebSocket

```
ws://服务器IP:8088/ws/ranking/:contest_id
```

- 连接后立即收到 `{"type":"snapshot","data":{...}}`
- 之后每次排名变化推送 `{"type":"delta","data":{...}}`
- 消息体字段与 REST `/contests/:id/ranking` 相同

### 请求示例

```bash
# 登录
curl -X POST http://localhost:8088/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"root","password":"xxx"}'
# → {"token":"eyJ...", "user":{...}}

# 创建题目
curl -X POST http://localhost:8088/api/v1/admin/problems \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "title":"A+B Problem",
    "statement":"给定两个整数 a b，输出 a+b",
    "time_limit_ms":1000,
    "mem_limit_kb":262144,
    "judge_type":"standard",
    "is_public":true
  }'

# 把题目加进比赛
curl -X POST http://localhost:8088/api/v1/admin/contests/1/problems \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"problem_id":1,"label":"A","max_score":100}'

# 提交代码
curl -X POST http://localhost:8088/api/v1/contests/1/submissions \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "problem_id":1,
    "language":"C++17",
    "source":"#include<iostream>\nint main(){int a,b;std::cin>>a>>b;std::cout<<a+b;}"
  }'
# → {"id":123, "status":"Pending", ...}

# 查询评测结果
curl http://localhost:8088/api/v1/submissions/123 \
  -H "Authorization: Bearer $TOKEN"
```

---

## 附录：目录结构

```
my-oj/
├── cmd/
│   ├── api-server/      # HTTP + WS 服务入口
│   └── judger-node/     # 评测机入口
├── internal/
│   ├── api/
│   │   ├── handler/     # Gin handlers
│   │   ├── middleware/  # 认证、CORS
│   │   └── server.go    # 路由装配
│   ├── core/
│   │   └── ranking/     # 排行榜计算 + WS Hub
│   ├── infra/postgres/  # 所有 SQL Repo
│   ├── judger/          # nsjail 沙盒、编译器、Runner
│   ├── mq/redis/        # Redis Streams 封装
│   └── storage/         # MinIO 封装
├── configs/
│   ├── languages.yaml   # 语言编译/运行命令
│   ├── nginx/           # 前端 nginx 配置
│   └── seccomp/         # BPF 策略
├── migrations/
│   └── 001_init.sql     # 首次建表脚本
├── frontend/            # Vue 3 前端
│   ├── src/
│   │   ├── api/         # axios 封装
│   │   ├── components/  # 通用组件（编辑器、排行榜、布局）
│   │   ├── composables/ # useCountdown 等 hook
│   │   ├── stores/      # Pinia
│   │   ├── types/       # TypeScript 类型
│   │   ├── views/       # 页面
│   │   └── router/
│   └── vite.config.ts
├── Dockerfile.api       # 后端镜像
├── Dockerfile.frontend  # 前端镜像（Node 构建 → nginx 分发）
├── Dockerfile.judger    # 评测机镜像（含 nsjail）
└── docker-compose.yml
```

---

## 反馈

遇到问题先看：
1. `docker compose logs -f <service>`
2. 本文档的 [故障排查](#故障排查) 一节
3. 提 Issue 时附上服务日志 + 复现步骤
