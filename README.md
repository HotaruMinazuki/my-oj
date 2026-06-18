# my-oj · 在线评测系统（Online Judge）

一个支持 **ICPC / OI / IOI** 多赛制的在线算法评测平台：Go 后端 + Vue 3 前端 +
nsjail 沙盒评测机 + Redis Streams 任务队列 + PostgreSQL + MinIO 对象存储，全部用
Docker Compose 一键部署。

> 面向校内 / 社团算法竞赛与日常刷题。内置实时排行榜、ICPC 封榜与滚榜、OI 盲考（挂机）
> 模式、特判 / 交互 / 通信题，以及可直接喂给官方 ICPC Tools Resolver 的 XML 导出。

---

## ✨ 功能特性

- **多赛制**
  - **ICPC** — 罚时制（解题数优先、罚时次之），支持封榜（freeze）与赛后滚榜。
  - **OI** — 总分制 + **盲考 / 挂机模式**：比赛中只显示「等待中」，不给任何反馈，
    结束后由管理员统一「赛后评测」，每题只评最后一次提交。
  - **IOI** — 总分制，实时逐测试点反馈（非盲考）。
- **多语言评测** — C / C++17 / C++20 / Python3（Java21 / Go / Rust 已预留，需在评测镜像装运行时后启用）。
- **安全沙盒** — nsjail + cgroup v2 + seccomp-bpf，内核计数器判定 TLE / MLE，禁网。
- **判题类型** — 标准（token 比对）、特判（Special Judge）、交互（Interactive）、通信（Communication）。
- **实时排行榜** — WebSocket 全量快照推送，自己的行高亮，首杀（first blood）徽标。
- **滚榜 / 解榜** — 赛后一键解冻封榜数据；或导出 CCS event-feed XML 用官方 Resolver 做现场滚榜动画。
- **题库与权限** — 公开题库；比赛题在比赛结束后自动公开；私有比赛仅报名者可见题面。
- **账号体系** — JWT 鉴权，bcrypt 密码哈希（老库无感升级），邮箱验证码找回密码。
- **题面渲染** — Markdown + KaTeX 数学公式，外链图片自适应宽度。
- **管理后台** — 题目 / 测试数据 / 比赛 / 用户 / 全站提交管理。

---

## 🏗️ 架构总览

```
                         ┌──────────────┐
        浏览器  ──────▶  │  frontend    │  Vue3 + Element Plus + Monaco
                         │  (nginx)     │  端口 8088
                         └──────┬───────┘
                                │ /api/v1, /ws  反向代理
                         ┌──────▼───────┐         ┌───────────────┐
                         │  api-server  │────────▶│  PostgreSQL   │ 用户/题目/比赛/提交
                         │  (Gin, 8080) │         └───────────────┘
                         │  HTTP + WS   │────────▶┌───────────────┐
                         └──┬────────┬──┘         │    MinIO      │ 源码 + 测试数据
                            │        │            └───────────────┘
                 publish ▲  │        │  ▲ pub/sub 排行榜
              judge_tasks│  ▼        ▼  │
                         ┌───────────────┐
                         │     Redis     │ Streams 任务队列 + 排行榜推送
                         └──────┬────────┘
                  consume       │  publish judge_results
                         ┌──────▼────────┐
                         │  judger-node  │  nsjail 沙盒，可水平扩展 (--scale)
                         └───────────────┘
```

判题结果由两个独立的消费者组消费同一条 `oj:judge:results` 流：
`ranker`（刷新实时排行榜）与 `api-server-results`（落库到 PostgreSQL）。

### 技术栈

| 层 | 选型 |
| --- | --- |
| 后端 | Go 1.2x、Gin、sqlx、go-redis、zap |
| 评测 | nsjail、cgroup v2、seccomp-bpf |
| 前端 | Vue 3、TypeScript、Vite、Element Plus、Monaco Editor、markdown-it + KaTeX |
| 存储 | PostgreSQL 16、Redis 7（Streams + Pub/Sub）、MinIO |
| 部署 | Docker、Docker Compose v2 |

---

## 🚀 快速开始

### 环境要求

- Linux（内核 ≥ 5.x，需 **cgroup v2** 与 user namespaces 供 nsjail 使用）
- Docker ≥ 20.10、Docker Compose v2
- 至少 2 GB 内存、10 GB 磁盘

### 1. 配置密钥（上线前必须改）

```bash
cp .env.example .env
# 生成强随机密钥
cat > .env <<EOF
JWT_SECRET=$(openssl rand -hex 32)
POSTGRES_PASSWORD=$(openssl rand -hex 16)
MINIO_ROOT_USER=oj_admin
MINIO_ROOT_PASSWORD=$(openssl rand -hex 24)
EOF
```

> `api-server` 启动时会拒绝弱于 32 字节的 `JWT_SECRET`。

### 2. 启动

```bash
docker compose up -d --build
```

首次启动会自动：建表（`migrations/001_init.sql`）→ 创建 MinIO 桶
（`submissions`、`testcases`）→ 拉起 API / 前端 / 评测机。

### 3. 访问与验证

```bash
docker compose ps           # 各服务应为 Up (healthy)
curl -I http://localhost:8088
```

浏览器打开 `http://<服务器IP>:8088`。除前端 8088 外，其余端口都绑定在 `127.0.0.1`，
生产环境请用防火墙额外加固。

### 4. 创建第一个管理员

系统初始无任何用户。先在 `/register` 注册账号，再用 SQL 提权：

```bash
docker compose exec postgres psql -U oj -d oj \
  -c "UPDATE users SET role='admin' WHERE username='你的用户名';"
```

刷新页面，顶栏会出现「管理后台」入口。

常用运维命令见 Makefile（`make up / down / logs / db-shell / db-reset` 等）。

---

## 🧑‍💻 支持的语言

评测镜像当前内置：**C++17、C++20、C、Python3**（Python 时限 ×3）。
语言的编译 / 运行命令在 [`configs/languages.yaml`](configs/languages.yaml) 配置；
启用 Java21 / Go / Rust 需先在 `Dockerfile.judger` 安装对应运行时再加回配置项。

---

## 📚 文档

| 文档 | 内容 |
| --- | --- |
| [docs/USAGE.md](docs/USAGE.md) | 部署、初始化、管理员 / 选手工作流、完整比赛流程示例、运维命令、故障排查、API 参考 |
| [docs/RESOLVER.md](docs/RESOLVER.md) | 滚榜：导出 CCS event-feed XML 并用 ICPC Tools Resolver 做现场动画 |

---

## 🗂️ 项目结构

```
my-oj/
├── cmd/
│   ├── api-server/       # HTTP + WebSocket 服务入口
│   └── judger-node/      # 评测机入口
├── internal/
│   ├── api/              # Gin handlers、middleware、路由装配 (server.go)
│   ├── core/
│   │   ├── contest/      # 赛制 Strategy（ICPC / OI / IOI）
│   │   └── ranking/      # 排行榜计算 + WebSocket Hub
│   ├── infra/postgres/   # 所有 SQL Repository
│   ├── judger/           # 编译、nsjail 沙盒、Runner、调度器
│   ├── mq/redis/         # Redis Streams 封装
│   └── storage/          # MinIO 封装
├── configs/              # languages.yaml、nginx、seccomp 策略
├── migrations/           # 001_init.sql 建表脚本
├── frontend/             # Vue 3 前端（src/views、components、stores、api…）
├── 滚榜/                  # ICPC Tools Resolver 工具
├── Dockerfile.api / .frontend / .judger
└── docker-compose.yml
```

---

## 🔧 本地开发与测试

```bash
# 后端：构建与单元测试（赛制 / 排行榜 / handler 逻辑）
go build ./...
go test ./internal/core/... ./internal/api/handler/...

# 前端
cd frontend && npm install && npm run dev
```

> 注意：`internal/judger/sandbox/nsjail` 使用 Linux 专有系统调用（`Setpgid`/`Kill`），
> 只能在 Linux 下构建运行；在其他平台请用 `GOOS=linux GOARCH=amd64 go build ./...` 交叉编译验证。

---

## ⚠️ 已知限制

- 评测镜像默认只装 C/C++/Python3 运行时；其它语言需自行扩展镜像。
- OI 盲考模式下，提交详情页会持续显示「等待中」直到管理员赛后评测——这是预期行为。
- nsjail 需要 `privileged` 容器与宿主 cgroup namespace（见 `docker-compose.yml` 注释）。

---

## 📄 许可

内部 / 教学用途。请在 fork 后补充你的开源许可证信息。
