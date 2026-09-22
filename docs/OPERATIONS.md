# 部署、备份与恢复

## 一键部署

安装 Docker Engine 与 Compose 插件后执行：

```sh
chmod +x deploy.sh backup.sh restore.sh rotate-key.sh
./deploy.sh --init
# 编辑 .env 后再启动
./deploy.sh
```

`--init` 生成 `.env` 及数据库密码、会话密钥和数据加密主密钥，但不启动容器。未配置 `DOMAIN` 时入口为服务器的 `APP_PORT`；配置域名时必须使用回环绑定（例如 `APP_PORT=127.0.0.1:8080`），脚本会启用 Caddy、申请证书并开放 80/443。站点与业务参数在首次 Web 安装页设置。

完整的新服务器与私有仓库步骤见 [全新服务器部署教程](DEPLOYMENT.md)。

## 备份

```sh
./backup.sh
# 或指定备份目录
./backup.sh /mnt/offsite/clicd
```

备份采用 PostgreSQL custom format，并生成 SHA-256 校验文件。`.env` 中的 `ENCRYPTION_KEY` 不在数据库备份内，必须使用独立的加密密码库异地保存；缺少该密钥将无法读取节点 API Key 和 TOTP 密钥。

## 恢复

恢复会覆盖当前数据库，因此必须显式确认：

```sh
./restore.sh backups/clicd-billing-YYYYMMDDTHHMMSSZ.dump --confirm
```

脚本校验摘要、停止 API、执行 `pg_restore --clean`，然后重启 API。恢复后检查 `/health/ready`、节点连接和最近账单。

## AES 主密钥轮换

```sh
./backup.sh
./rotate-key.sh --confirm
```

脚本停止 API，在单个数据库事务中重新加密全部 CLICD API Key、已启用及待确认的 TOTP 密钥，以及安装页保存的支付、通知和 Metrics 密钥；任一记录解密失败则整体回滚。成功后原子替换 `.env` 并重建 API 容器。

## 日常检查

- `/health/live`：进程存活。
- `/health/ready`：数据库可用且迁移完成。
- `/metrics`：携带 `Authorization: Bearer <安装页生成或填写的 Metrics Token>` 采集。
- 定期执行一次恢复演练，不能只验证备份文件存在。
