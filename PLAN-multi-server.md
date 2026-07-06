# 轻舟多机节点管理改造计划

## 目标
轻舟通过 SSH 直接管理多台服务器上的 sing-box。

## Task 1: 数据库改造
- 新增 `servers` 表：id, name, host, port, ssh_user, ssh_key, ssh_key_pass, enabled
- `sb_inbounds` 加 `server_id` 字段（默认 0 = 旧服务器）
- 文件: `internal/store/migrate.go`, `internal/store/nodes.go`

## Task 2: SSH 远程管理器
- 新建 `internal/sshctl/` 包
- SSH 连接池 + 配置推送 + sing-box 重启
- 文件: `internal/sshctl/remote.go`

## Task 3: 改造 sbctl 控制器
- 支持多服务器：每台服务器独立生成配置并推送
- 文件: `internal/sbctl/controller.go`

## Task 4: API 改造
- 新增服务器管理 API (CRUD)
- 修改入站 API 支持 server_id
- 修改节点 API 支持多服务器
- 文件: `internal/api/sb_admin.go`, `internal/api/nodes_admin.go`, `internal/api/router.go`

## Task 5: 配置生成改造
- `singbox/generate.go` 支持按服务器过滤 inbounds
- 文件: `internal/singbox/generate.go`, `internal/store/singbox.go`

## Task 6: 前端改造
- 新增服务器管理页面
- 修改入站管理选择服务器
- 文件: `web/dist/views-admin.js`
