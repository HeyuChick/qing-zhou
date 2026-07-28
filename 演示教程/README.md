# 轻舟 · 演示视频 & 使用教程 素材包

这个文件夹是为「轻舟」制作**产品演示视频 + 使用教程**准备的全套素材，覆盖**用户端**与**管理端**。

## 目录内容

| 文件 / 目录 | 说明 |
|---|---|
| `录屏脚本-分镜.md` | ⭐ 核心。逐场景的录屏脚本：画面 / 操作步骤 / 口播稿 / 字幕 / 时长，附分镜速查表。照此录屏即可。 |
| `图文教程.html` | 真实截图 + 步骤讲解的图文教程（双击用浏览器打开；配图取自同目录 `截图/`，请连同该目录一起保留）。既是书面教程，也可当分镜预览。 |
| `产品演示动画.html` | 自动播放、循环的 16:9 产品介绍片，适合录屏当**片头**。空格暂停/播放，右下角可重播。加 `?loop=0` 只播一遍。 |
| `截图/` | 25 张真实界面截图（2× 高清 PNG），可直接用于视频、幻灯片或文档。 |

## 三种成品怎么配合

- **只要图文教程** → 直接发 `图文教程.html`。
- **要录演示视频** → 按 `录屏脚本-分镜.md` 录屏；片头用 `产品演示动画.html` 录一段；缺画面时用 `截图/` 里的图补。
- **要产品介绍短片** → 单独录 `产品演示动画.html` 即可（约 40 秒）。

## 演示环境与账号

截图取自本地演示实例，数据是灌好的演示数据：

- 地址：`http://127.0.0.1:8099`（正式环境替换为你的域名，如 `node.xrilang.com`）
- 管理员：`admin / admin12345`
- 普通用户：`demo / demo12345`（已分配「月享套餐」，积分 5000）

> 正式录制建议用你的真实域名和真实节点，画面更可信；演示实例的节点是占位数据。

## 截图清单

**公共**：`pub-home`(首页监控大屏) · `pub-login`(登录/注册弹窗)

**用户端**：`user-dashboard`(控制台) · `user-shop`(积分商城) · `user-sub`(订阅管理) · `user-orders`(订单) · `user-points`(积分明细) · `user-notices`(公告) · `user-help`(帮助中心) · `user-account`(账户设置)

**管理端**：`admin-overview`(管理概览) · `admin-nodes`(节点管理) · `admin-singbox`(sing-box/出口/中转) · `admin-packages`(套餐管理) · `admin-users`(用户管理) · `admin-user-groups`(用户组) · `admin-orders`(订单管理) · `admin-reg-codes`(注册码) · `admin-certs`(证书中心) · `admin-servers`(服务器) · `admin-monitor`(监控) · `admin-settings`(系统设置) · `admin-update`(在线更新) · `admin-announcements`(公告管理) · `admin-help`(帮助文档)

## 如何重新截图（可选）

若界面有更新、想重截，可参考本会话用到的流程（Windows）：

1. 构建前端并嵌入后端二进制：`cd frontend && npx vite build && cd .. && go build -o qz.exe .`
2. 用独立 DB 启动（勿动正式库）：设置 `QZ_DB` 指向临时文件、`QZ_LISTEN=127.0.0.1:8099`、`QZ_ADMIN_USER/PASS`；创建用户会调用 sing-box，本机需用空 `.cmd` stub 顶替 `sing-box`/`systemctl`。
3. 通过管理 API 灌演示数据（套餐 / 节点 / 公告 / 帮助 / demo 用户并分配套餐）。
4. 用无头 Chrome（puppeteer-core）逐页注入 token 后 `reload` 再截图（注意：哈希路由切换不重载，必须 reload 才会带上登录态）。

> 注意：`traffic_bytes` 这类大整数在 PowerShell 里要 `[int64]` 强制转换，否则会被序列化成浮点导致后端 400。
