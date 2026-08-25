---
kind: error_handling
name: 基于 chi 中间件与领域哨兵错误的统一 HTTP 错误处理
category: error_handling
scope:
    - '**'
source_files:
    - internal/api/router.go
    - internal/api/respond.go
    - internal/api/safehttp.go
    - internal/store/egress.go
    - internal/store/singbox.go
    - internal/store/users.go
    - internal/store/points.go
    - internal/store/packages.go
    - internal/store/purchase.go
    - internal/store/userplans.go
    - internal/acmesh/acmesh.go
    - cmd/probe/main.go
---

## 1. 整体方案

本项目采用 Go 标准库 + `github.com/go-chi/chi/v5` 构建 HTTP API，错误处理由三层组成：

- **框架层**：路由通过 `router.go` 中的 `API.Router()` 注册，挂载了 `middleware.Recoverer`（panic 恢复）、`middleware.Timeout(30s)`、`middleware.Compress`、`middleware.RequestID` 以及自定义的 `maxBodyMiddleware(8MiB)`。所有请求体被限制为 8 MiB，防止恶意 POST 耗尽内存。
- **响应层**：`internal/api/respond.go` 提供统一的 JSON 信封 `ok(w, data)` → `{code:0, msg:"", data:...}` 和 `fail(w, status, msg)` → `{code:status, msg, data:nil}`。业务 handler 不直接写 `w.WriteHeader`，而是调用这两个函数输出结果。
- **领域层**：各子包（`store`、`acmesh`、`subconv` 等）通过包级 `var ErrXxx = errors.New(...)` 定义语义化哨兵错误（sentinel errors），handler 用 `errors.Is(err, ErrXxx)` 判断并映射到合适的 HTTP 状态码。

## 2. 关键文件与位置

| 文件 | 职责 |
|---|---|
| `internal/api/router.go` | 注册 chi 路由与全局中间件（Recoverer/Timeout/Compress/MaxBytes） |
| `internal/api/respond.go` | 统一 JSON 信封 `ok` / `fail` |
| `internal/api/safehttp.go` | SSRF 防护（拒绝内网地址）、URL 白名单校验、请求体大小限制 |
| `internal/store/*.go` | 领域哨兵错误集中地：`ErrEgressUndecryptable`、`ErrInUse`、`ErrCredsResetCooldown`、`ErrInsufficientFunds`、`ErrPackageDisabled`、`ErrOutOfStock`、`ErrAlreadyRefunded`、`ErrOptionNotFound`、`ErrUserNotFound`、`ErrNegativeBalance`、`ErrBucketNotFound`、`ErrBucketProtected`、`ErrPackageNotAllowed` |
| `internal/acmesh/acmesh.go` | ACME 证书申请失败返回带上下文的 `fmt.Errorf("...: %v: %s", err, out)` |
| `cmd/probe/main.go` | 探针侧同样使用 `fmt.Errorf` 包装网络错误 |

## 3. 架构与约定

### 3.1 哨兵错误（Sentinel Errors）
领域逻辑只返回 `error`，将可被上层识别的业务异常定义为包级变量：
```go
// store/purchase.go
var (
    ErrPackageDisabled   = errors.New("商品已下架")
    ErrOutOfStock        = errors.New("商品库存不足")
    ErrAlreadyRefunded   = errors.New("该订单已退款")
)
// store/users.go
var ErrCredsResetCooldown = errors.New("节点凭据重置冷却中")
```
这些错误在 handler 中通过 `errors.Is` 精确匹配，再决定 HTTP 状态码。

### 3.2 Handler 错误映射模式
每个 handler 遵循同一模板：参数校验失败 → `fail(w, http.StatusBadRequest, "...")`；业务规则违反 → 检查 `errors.Is(err, ErrXxx)` 后返回对应状态码；底层 I/O 或未知异常 → `fail(w, http.StatusInternalServerError, "服务器错误")`。例如 `internal/api/account.go` 中密码修改路径对“未登录”、“原密码错误”、“保存失败”分别返回 401/400/500。

### 3.3 数据库层特殊处理
- `sql.ErrNoRows` 被统一转换为 `nil` 返回值（如 `scanUser`、`scanBucket`、`GetSbTls` 等），表示“不存在”，而非错误。调用方自行判断 `obj == nil`。
- 删除操作若存在外键引用，返回 `ErrInUse`，handler 将其转为 400 并提示剩余引用数。
- 加密字段解密失败时，结构体上设置 `DecryptFailed` 标志，并在配置生成阶段主动报错（`resolveTlsBlock` 中拒绝降级为明文入站），避免静默泄露。

### 3.4 Panic 与 recover
`router.go` 通过 `middleware.Recoverer` 全局捕获 panic，不会让进程崩溃。`internal/sbstats/client.go` 注释明确说明其 decode 路径运行在 recover 保护下，因为 sing-box 统计解析可能遇到畸形数据导致 panic。

### 3.5 安全相关错误
`safehttp.go` 的 `safeFetchClient` 在拨号阶段拦截内网 IP（loopback/private/link-local/CGNAT），返回 `fmt.Errorf("拒绝连接到内网地址 %s", ip.IP)`；`validFetchURL` 仅允许 `http`/`https` 方案，否则返回中文错误消息供 handler 包装进 `fail`。

### 3.6 错误上下文包装
非领域错误普遍使用 `fmt.Errorf("...: %v: %s", err, out)` 追加外部命令输出（如 acme.sh 失败时的 stderr），便于排查。删除类错误使用 `%w` 包装哨兵错误以便 `errors.Is` 穿透：`fmt.Errorf("%w：仍有 %d 个入站在使用此 TLS", ErrInUse, n)`。

## 4. 约定与约束

- **禁止裸 `fmt.Println`/`log.Print` 作为错误上报**：所有用户可见的错误必须通过 `fail(w, status, msg)` 以结构化 JSON 返回。
- **业务异常必须使用哨兵错误**：`store` 包中所有可被 handler 区分的失败路径都暴露为 `var ErrXxx`，新增业务分支需遵循这一模式。
- **`sql.ErrNoRows` 一律视为“不存在”**：扫描函数内部吞掉 `NoRows` 返回 `nil`，调用方不得将其当作错误处理。
- **HTTP 超时与 body 大小限制是强制边界**：所有请求受 30s 超时与 8 MiB body 限制，超出即中止，handler 无需重复实现。
- **SSRF 防护不可绕过**：任何对外发起 HTTP 请求必须经 `safeFetchClient()`，禁止直接使用 `http.Get`。
- **panic 不用于控制流**：仅在解析不可信输入（如 sing-box stats）时使用 recover 兜底，正常流程通过 error 返回。
- **错误消息面向管理员/用户**：所有 `fail` 中的 `msg` 均为中文，前端直接展示，因此不应暴露堆栈或内部标识。

## 5. 适用性

本仓库是一个完整的 Go HTTP 服务，拥有成熟的错误分层（框架中间件 → handler 响应封装 → 领域哨兵错误），因此本类别完全适用。