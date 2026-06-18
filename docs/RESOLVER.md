# 滚榜（ICPC Resolver）使用说明

本系统可为每场比赛导出 **CCS event-feed XML**，直接喂给 ICPC Tools Resolver
（`org.icpc.tools.resolver.Resolver`，即仓库根目录 `滚榜/` 下的工具）做赛后滚榜动画。

## 1. 完善参赛者信息（重要）

导出的 XML 中：

- **队名** = 用户的用户名（username）
- **学校 / 单位** = 用户的「学校/单位」字段（organization）

`organization` 默认可能为空。每位选手登录后，进入 **个人主页 → 编辑资料**，填写
「学校/单位」即可。滚榜界面会用它做学校归属与奖牌分组展示。

> 管理员可在「管理后台 → 用户管理」查看每位用户是否已填写。

## 2. 导出 XML

管理后台 → 比赛管理 → 对应比赛点击 **「滚榜XML」** 按钮，浏览器会下载
`contest-<id>-event-feed.xml`。

该文件包含：

- `<info>`：比赛标题、总时长、封榜时长（无封榜时输出 `0:00:00`，**不可省略**，
  否则 Resolver 的 checkContestState 会空指针）、罚时（penalty_minutes）、开始时间
- `<judgement>`：判定类型定义（AC/WA/TLE/MLE/RTE/CE）。**必须有**，否则 Resolver 无法把
  `<run>` 的结果识别为已判，会报 "unjudged submissions" 并拒绝滚榜
- `<problem>`：每道题的题号（A/B/C…）与标题
- `<team>`：每个参赛者（已报名者 + 有提交者）的 id / 队名 / 学校
- `<run>`：每次已判定提交（AC/WA/TLE/MLE/RTE/CE），含相对时间、是否通过、是否计罚时
- `<finalized>`：比赛结束标记。**必须有**，否则 Resolver 报 "Contest is not over"。
  奖牌边界这里填 0，真正数量由 `awards.bat --medals` 决定

> 仅导出最终判定状态的提交；Pending/评测中/SystemError 不计入。比赛结束后的提交不计入。

### 编译错误（CE）是否计罚时

CE 的罚时口径由比赛 `settings.ce_no_penalty` 这一**单一事实来源**决定，实时排行榜
（`core/contest` 的 ICPC 策略）与本导出的 XML（`<judgement>` 定义和每条 CE `<run>`
的 `penalty`）都读取同一项，二者**始终一致**：

- **默认（`ce_no_penalty` 缺省或为 `true`）：CE 不计罚时。** 贴合现代 ICPC 规则，也与
  ICPC Tools Resolver 自带的标准判定类型一致。AC 之前的 CE 不会增加罚时。
- `ce_no_penalty=false`：CE 计一次罚时，与 WA 等同（部分旧赛区规则）。

> 该默认值在 `internal/models/contest.go` 的 `ContestSettings.CENoPenalty()` 中定义，
> 是两侧唯一的取值来源；改默认只需改这一处。

## 3. 运行环境：Java 17+

ICPC Resolver 2.6 需要 **Java 17 或更高**（Java 8 会报 `UnsupportedClassVersionError`）。
本仓库的 `滚榜/awards.bat`、`滚榜/resolver.bat` 已写死使用本机的 Temurin JDK 17
（`set JAVA=...`）。换机器时若 Java 装在别处，改这两个脚本顶部的 `JAVA` 路径即可。

## 4. 运行滚榜工具

将下载的 XML 放到 `滚榜/` 目录。有两种用法：

### 直接滚榜（不发奖）

**Windows**（PowerShell 里注意 `.\` 前缀）
```powershell
cd 滚榜
.\resolver.bat contest-1-event-feed.xml
```

**Linux / macOS**
```bash
cd 滚榜
./resolver.sh contest-1-event-feed.xml
```

### 先发奖再滚榜（推荐：金银铜奖牌 + 名次）

第一步用 `awards.bat` 给比赛分配奖项，它会读取 XML 并输出一个带奖项的
`*-awards.ndjson`（JSON 事件流）：

```powershell
cd 滚榜
.\awards.bat contest-1-event-feed.xml --medals 1 1 1 --rank 10
```

- `--medals <金> <银> <铜>`：奖牌数量
- `--rank <N>`：给前 N 名分配名次奖（"1st place"…）
- `--fts <封榜前> <封榜后>`：一血奖（first-to-solve）
- 更多参数见 `.\awards.bat --help`

第二步把生成的 ndjson 喂给 resolver 滚榜：

```powershell
.\resolver.bat contest-1-event-feed-awards.ndjson
```

> 奖牌等参数也可参考 `滚榜/README.pdf` 或
> https://blog.csdn.net/xzx18822942899/article/details/128275137 。

### 滚榜界面操作

| 按键 | 作用 |
|------|------|
| 空格 / 鼠标点击 | 推进一步，揭晓下一个 |
| `p` | 暂停 / 继续 |
| `+` / `-` | 加速 / 减速 |
| `j` | 恢复默认速度 |
| `r` / `1` / `2` | 回退 / 快退一步 / 快进一步 |
| `0` | 跳回开头 |
| `Ctrl-Q` | 退出 |

## 注意

- 接口为管理员专用：`GET /api/v1/admin/contests/:contest_id/resolver.xml`
- 时间以比赛 `start_time` 为基准计算相对秒数；请确保比赛的开始/结束/封榜时间设置正确。
