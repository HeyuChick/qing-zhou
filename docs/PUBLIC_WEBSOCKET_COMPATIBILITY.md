# 公共 WebSocket 订阅保真与兼容边界

这次修改仅涉及用户公开分享链接、订阅解析/格式转换与测试。不改账户 UUID/密码，不改 sing-box 依赖、构建脚本或 WS runtime 实现。内部中转配置与公共分享格式是不同路径，native 配置通过 `check` 不代表所有第三方客户端完成了真实握手或并发流量测试。

## 格式能力矩阵

| 输出/输入 | 协议 | path（含原始 query / `%`） | Host / 其它 headers | early data | TLS / SNI / ALPN / 验证 |
|---|---|---|---|---|---|
| VLESS/Trojan URI | VLESS、Trojan | 保留，外层 URI 仅转义一次 | Host 为常见字段；完整 headers 用轻舟扩展 | `max_early_data` / `early_data_header_name` 是轻舟转换所保留的扩展，第三方不保证识别 | 保留明确 TLS/none、SNI、ALPN、显式验证设置 |
| VMess base64(JSON) | VMess | 保留 JSON 字符串 | Host 为公共字段；完整 headers 用轻舟扩展 | 同上，修复原先编码/渲染时丢失 | 保留 TLS flag、SNI、ALPN、验证；支持历史 allowInsecure/insecure |
| sing-box JSON | VLESS、VMess、Trojan | 保留 | 支持单值及多值数组 | 原生 max_early_data / early_data_header_name | 保留；明确 plaintext 不伪装 TLS |
| Mihomo YAML | VLESS、VMess、Trojan | YAML 保留；native runtime 有下述规范化限制 | 原生单值 map；多值 header 节点明确省略并注释建议 sing-box | 原生 ws-opts 字段 | 保留；本导出器不将 plaintext Trojan 伪装成 TLS 节点 |
| Surge | VMess、Trojan；无 VLESS | 可表达且不含不能安全编码的分隔符时保留 | 单值 header 以 `Header:Value\|Header:Value` 表示；多值或危险分隔符节点明确省略 | 本导出器不宣称支持 ED；保留普通 WS，并在生成文件注释说明 ED 未应用 | 保留 SNI/验证；ALPN 需支持该字段的 Surge 版本；不输出 plaintext Trojan |

Mihomo/Surge 的省略只针对不能忠实表达的节点，其他节点和引用它们的选择组保持可用；绝不把无法表达的 WS 静默改成 TCP，也不自动开启 insecure。Surge 的常规 WS 并不要求 early data：固定候选 sing-box 服务端在 ED header 为空时走普通 Upgrade，ED 是可选首包优化。

### 轻舟扩展不是通用分享标准

`qz-ws-headers` 保存 header 到字符串数组的 JSON map；URL 风格放在 query，VMess 放在 JSON 对象。它用于轻舟自建链接 → 轻舟原生格式订阅转换，也用于 Clash 导入后的无损中间表示。未知第三方客户端可能忽略它，不能据此声称原始 URI 可以在所有客户端保留必需的自定义头。依赖多值/额外头的节点优先使用 sing-box 原生订阅；单值头也可使用 Mihomo。

早期 VLESS/Trojan 的 `max_early_data` / `early_data_header_name`、无扩展的 host/path、VMess v2 JSON 均继续接受；额外接受明确的 `ed` / `eh` 参数别名。轻舟的解析/导出始终保留 path 内的 `?ed=`，不自行删 query、重编码 `%2F` 或猜测传输指令。但 Mihomo native runtime 会把数字 `ed` 解释成 ED 设置并删除该 query，再编码剩余 query；它还可能规范化 escaped path（不保留 RawPath）。因此 YAML roundtrip 不等于这些情形的 wire 字节保真；生成配置会给出注释提醒，保留历史可用节点，管理员若依赖精确请求目标须另做该客户端实连验证。

### TLS 安全边界

VLESS/Trojan 历史直接调用 `BuildShareLink` 仍默认 TLS。新增 `TLSDisabled` 是显式 opt-out；store 同时检查 TLS profile 与 options 内联 TLS，不能仅凭 `tls_id=0` 降为明文。profile 按实际生成器优先于 inline；不明 inline TLS 形态拒绝导出。客户端 TLS profile 的 SNI/ALPN 覆盖值也保留。已有验证开关原样保留，不因转换失败自动放宽验证。userinfo 改为 URI userinfo 编码，避免密码中的空格/加号/保留字符改变原值。

## 验证

- `go test ./internal/subconv ./internal/singbox`
- `go test ./internal/store -run '^(TestSelfBuiltWS|TestShareLinkTLS)' -count=1`
- `go test -race ./internal/subconv ./internal/singbox`
- `QZ_SINGBOX_TEST_BIN=/path/to/pinned-core go test ./internal/subconv -run '^TestPublicWebSocket' -count=1 -v`

覆盖三协议 × 严格验证/显式 insecure 的 share → native Clash → 再导入 → sing-box roundtrip；TLS profile、inline TLS、明确 plaintext、不明 TLS 形态；query/百分号/特殊密码；Host 和多值头；ED 大小及 header；恶意 CRLF/重复 Host/分隔符/Surge 行内注释（空格后的 #、;、//）；无法表达的格式有明确负例。固定候选核心对生成的三协议 WS JSON 做 `check`（本次二进制 SHA-256：`706755c64d7aece8d023f33d84da8aa674779d374d6ef1e0b5b23c1998e9811c`）。另外提供 `QZ_MIHOMO_TEST_BIN` 驱动的同矩阵 `-t` 检查，本次环境无该二进制，未运行；Surge 本次仅按官方格式做静态回归。Mihomo 的 WS 实现固定使用 HTTP/1.1，保留 ALPN 配置字段不表示可以实现任意 HTTP/2 WebSocket 握手。没有把这类 schema/roundtrip 结果当成真实吞吐、TLS 握手或所有客户端兼容证明。

## 格式依据

- [VMess v2 分享字段](https://github.com/2dust/v2rayN/wiki/Description-of-VMess-share-link)
- [VLESS 分享格式提案](https://github.com/XTLS/Xray-core/discussions/716)
- [sing-box WS transport](https://sing-box.sagernet.org/configuration/shared/v2ray-transport/#websocket)
- [Mihomo WSOptions 类型](https://github.com/MetaCubeX/mihomo/blob/Meta/adapter/outbound/vmess.go) 与 [传输字段](https://github.com/MetaCubeX/Meta-Docs/blob/main/docs/config/proxies/transport.md)
- [Surge VMess](https://manual.nssurge.com/policies/vmess.html)、[Trojan](https://manual.nssurge.com/policies/trojan.html)、[TLS/ALPN](https://manual.nssurge.com/policies/tls.html)；ALPN 文档标注 iOS 5.20.0+ / Mac 6.7.0+

## 升级时保留节点禁用选择

新 URI/VMess 字段不能使用户已禁用的节点重新出现。store 用可信原始 profile/options 精确重建旧版 TLS、Host、ALPN 和 serializer 参数，只携带旧 NodeKey 哈希给内部节点清单；不持久化或再次发布旧凭据链接，也不信任外部 URI 自带的 alias 声明。订阅过滤、disabled 展示和用户明确启用都识别旧 key。

读取不会修改用户偏好。若两个新节点曾共用一个旧 key，用户明确启用其中一个时，同一事务先为仍应禁用的兄弟节点保存各自当前 key，再删除共享旧 key；写入失败整体回滚，不能顺带启用另一节点。冻结旧 serializer 字节、真实 store 导出、API 可信 alias、共享 key 负例与事务失败测试覆盖该兼容路径。

Surge 对空格后的 `#`、`;`、`//` 采用行内注释语义，因此不能安全编码的必需字段会明确省略；名称单独清理，重名后缀使用 `(2)`，避免 ` #2` 把整行后半段包括 TLS 设置注释掉。依据：[Surge profile 语法](https://manual.nssurge.com/profile/format.html)、[Mihomo WS runtime](https://github.com/MetaCubeX/mihomo/blob/Meta/transport/vmess/websocket.go)。
