# 灾备恢复

## 范围与开关

`系统设置 → 数据备份` 默认上传一个 SQLite 一致性快照。服务器运维人员设置
`QZ_BACKUP_MANIFEST=/absolute/path/recovery.json` 后，手动和定时远端备份会改为上传
`.tar.gz` 灾备恢复包。

恢复包包含：

- `database/qingzhou.db`：通过 SQLite `VACUUM INTO` 生成的已提交数据快照；
- `files/`：由服务器清单显式允许的环境文件、systemd unit、sing-box/Tunnel 配置或私钥；
- `manifest.json`：归档时间、版本、平台、原始路径、权限、大小、SHA-256 和缺失的可选项；
- `RESTORE.md`：隔离恢复步骤。

源代码和面板二进制不放入恢复包；应由受保护的 Git 仓库与 `manifest.json` 中记录的版本重新构建。
清单由服务器文件控制，管理员网页不能指定任意路径。必需文件缺失、符号链接、特殊文件、在线
SQLite 文件、超过 256 MiB 原始数据或 10,000 个文件都会使该次备份失败，不会上传不完整的包。

示例（仅按实际运行路径调整，切勿提交生产内容）：

```json
{
  "repository": "https://github.com/your-org/qing-zhou",
  "sing_box_version": "1.x with required features",
  "sources": [
    { "path": "/opt/qingzhou/qingzhou.env" },
    { "path": "/etc/systemd/system/qingzhou.service" },
    { "path": "/etc/systemd/system/qingzhou-sing-box.service" },
    { "path": "/etc/qingzhou-sing-box" },
    { "path": "/etc/cloudflared/config.yml", "optional": true },
    { "path": "/etc/cloudflared/tunnel-credentials.json", "optional": true }
  ]
}
```

## 未额外加密的风险

本实现不对归档再做客户端加密。数据库中的敏感设置仍由 `QZ_SECRET_KEY` 加密，但恢复包可能包含
`QZ_SECRET_KEY`、SSH 私钥、Tunnel 凭据或证书；因此它必须只存入私有桶。不要公开 bucket、设置公开
下载、提交下载链接、或将归档放进 Git。对象存储访问密钥、Cloudflare MFA 恢复方式及 Git 恢复权限必须
在服务器之外独立保管。

R2 使用账户 S3 Endpoint，不附加 Bucket 路径；完整灾备包只允许 HTTPS Endpoint。保留策略删除的是
已记录的远端对象；在启用自动删除前确认保留天数与份数适合恢复目标。

## 恢复演练

1. 下载包到隔离目录，核对面板记录中的整包 SHA-256，再核对 `manifest.json` 的每个文件哈希、大小和路径。
2. 用清单记录的仓库和版本构建匹配面板，安装相同功能集的 sing-box；不要恢复未知二进制。
3. 停止目标面板、sing-box 和 Tunnel 服务；保留目标机现有数据，禁止覆盖运行中的 SQLite。
4. 人工确认 `destination` 后恢复数据库和配置，按清单恢复权限、所有者和 `QZ_SECRET_KEY`。缺失原密钥时，已有敏感设置无法解密。
5. 根据环境重新核对 DNS、Tunnel、反向代理、防火墙与安全组；它们可能不在包中。
6. 对恢复的数据库执行 `PRAGMA integrity_check`，执行 `systemctl daemon-reload`，在隔离环境验证登录、订阅、节点、流量与健康接口后再切换生产。

每次变更服务定义、外部证书/凭据路径、环境变量或节点配置后，更新恢复清单并执行一次非破坏性恢复演练。
