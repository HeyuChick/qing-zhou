# 轻舟 — 开发模式启动（后端）
#
# 前端有两种改法：
#   1) 热更新（推荐）：另开一个终端 `cd frontend; npm run dev`，浏览器开
#      http://127.0.0.1:5173 —— vite 把 /api、/sub 代理到本脚本的 8081。
#   2) 直读磁盘：`cd frontend; npx vite build` 后，下面的 QZ_WEB_DIR 让服务
#      直接读 frontend/dist，改完重新 build、刷新浏览器即可，无需重编 Go。
#      （生产留空，用编译进二进制的内嵌资源。）
#
# 后端：go run 每次启动都会重新编译最新代码。
$env:QZ_LISTEN  = '127.0.0.1:8081'   # 浏览器访问 http://127.0.0.1:8081
$env:QZ_WEB_DIR = 'frontend/dist'    # 前端直读磁盘开关（仅第 2 种改法需要）

# SMTP 等配置请在面板「设置」页填写并保存（保存后重启本服务生效），
# 这样密码不会出现在脚本里。

go run .
