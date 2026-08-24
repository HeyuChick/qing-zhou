---
kind: logging_system
name: 基于标准库 log 的进程内日志输出
category: logging_system
scope:
    - '**'
source_files:
    - main.go
    - cmd/probe/main.go
    - internal/api/email.go
    - internal/api/backup_admin.go
    - internal/api/certs_renew.go
    - internal/api/monitor.go
    - internal/api/server_admin.go
---

## 1. 使用的系统/方案

本项目**没有引入任何第三方日志框架**（go.mod 中无 zap、logrus、zerolog、slog 等依赖）。整个 Go 服务仅使用 Go 标准库 `log` 包进行进程内日志输出，并通过 `log.Fatalf` 在启动阶段遇到致命错误时直接退出进程。所有日志均写入默认输出（即标准输出/标准错误），由运行环境（systemd、Docker、shell）负责收集与转发。

## 2. 关键文件

- `main.go`：唯一进程入口，集中调用 `log.Printf` / `log.Fatalf` 输出启动、监听、关闭、SMTP 启用状态、sing-box 控制器状态等关键生命周期事件。
- `cmd/probe/main.go`：探针子进程的日志，同样使用 `log.Printf` 输出告警与上报失败信息。
- `internal/api/*.go`：业务层各处理器广泛使用 `log.Printf` 记录备份失败、证书续签错误、邮件发送失败、队列推进结果、SSH host key 变更等运行时事件。
- `internal/config/config.go`：配置加载器，不包含日志逻辑，但通过环境变量驱动的行为会触发上述日志输出。

## 3. 架构与约定

- **单一全局 logger**：未封装自定义 logger 或注入点，所有模块直接 import `log` 并调用 `Printf` / `Fatalf`。这使得日志输出是进程级共享的全局行为，无法按模块或请求隔离。
- **无结构化字段**：日志消息采用 `fmt.Sprintf` 风格的字符串拼接（如 `log.Printf("backup: %v", err)`、`log.Printf("admin: server %d host changed %s -> %s, pinned SSH host key dropped", id, sv.Host, sv.OldHost)`），不输出 JSON 或键值对结构，因此不适合直接接入结构化日志采集系统。
- **无日志级别**：全部使用同一 `log.Printf` 输出，没有区分 info/warn/error/debug；错误与正常流程混在同一级别。唯一的“严重性”信号是 `log.Fatalf`，用于启动阶段的不可恢复错误（数据库打开、迁移、seed、JWT secret 缺失等）。
- **无 sink 路由**：没有将日志重定向到文件、syslog、远程收集器或带时间戳前缀的 writer；输出完全依赖运行时的 stdout/stderr 管道。
- **探针与面板共用模式**：`cmd/probe/main.go` 与主服务遵循相同约定——用 `log.Printf` 输出警告（以 `WARNING:` 前缀标识）和常规信息。

## 4. 约定与约束

- **启动期必须使用 `log.Fatalf`**：`main.go` 在数据库打开、迁移、seed、JWT secret 缺失等不可恢复场景下直接 `log.Fatalf` 终止进程，这是项目内对致命错误的统一处理方式。
- **运维相关日志以语义化前缀组织可读性**：例如 `backup:`、`cert renew:`、`email send to ... failed:`、`probe alert check:`、`local metrics:`、`queue advance:`、`admin:` 等前缀便于人工 grep 过滤。
- **敏感信息警告**：探针命令行参数中的 token 会通过 `log.Printf("WARNING: -token on the command line is visible via ps/proc; prefer the QZ_PROBE_TOKEN env var")` 提醒管理员；主服务在未设置 `QZ_SECRET_KEY` 时也会输出 WARNING 提示 at-rest 加密风险。
- **开发模式下的特殊日志**：当 SMTP 未配置时，邮件发送分支会输出 `[email:dev]` 标记的日志行，表明该环境不会真正发送邮件而是把链接打印到日志中。
- **约束来源**：这些约定并非来自独立文档，而是由代码实现本身体现——全仓零第三方日志依赖、全量使用 `log.Printf`/`log.Fatalf`、无 logger 初始化或注入点，构成了事实上的强制约束。