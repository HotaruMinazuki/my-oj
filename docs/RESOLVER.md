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

- `<info>`：比赛标题、总时长、封榜时长（由封榜时间推导）、罚时（penalty_minutes）、开始时间
- `<problem>`：每道题的题号（A/B/C…）与标题
- `<team>`：每个参赛者（已报名者 + 有提交者）的 id / 队名 / 学校
- `<run>`：每次已判定提交（AC/WA/TLE/MLE/RTE/CE），含相对时间、是否通过、是否计罚时

> 仅导出最终判定状态的提交；Pending/评测中/SystemError 不计入。比赛结束后的提交不计入。

## 3. 运行滚榜工具

将下载的 XML 放到 `滚榜/` 目录，然后：

**Windows**
```bat
cd 滚榜
resolver.bat contest-1-event-feed.xml
```

**Linux / macOS**
```bash
cd 滚榜
./resolver.sh contest-1-event-feed.xml
```

奖牌数量（金/银/铜）等参数按工具自带说明（`滚榜/README.pdf`）或
参考 https://blog.csdn.net/xzx18822942899/article/details/128275137 配置。

## 注意

- 接口为管理员专用：`GET /api/v1/admin/contests/:contest_id/resolver.xml`
- 时间以比赛 `start_time` 为基准计算相对秒数；请确保比赛的开始/结束/封榜时间设置正确。
