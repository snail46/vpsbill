# 部署、备份与恢复

## 一键部署

安装 Docker Engine 与 Compose 插件后执行：

```sh
chmod +x deploy.sh backup.sh restore.sh rotate-key.sh
./deploy.sh --init
# 编辑 .env 后再启动
./deploy.sh
```

`--init` 生成 `.env` 及数据库密码、会话密钥和数据加密主密钥，但不启动容器。访问方式由 `.env` 的 `ACCESS_MODE` 决定（direct / caddy / cloudflare / proxy），客户前台和管理后台分别监听 `PORTAL_PORT`、`ADMIN_PORT`，详见 [访问方式](ACCESS.md)。站点与业务参数在首次 Web 安装页设置。

完整的新服务器与私有仓库步骤见 [全新服务器部署教程](DEPLOYMENT.md)。

## 备份与还原（后台「数据备份」）

后台「数据备份」页面（需要超级管理员）管理全部备份，不用登录服务器。

- **内容**：整个数据库，即客户、订单、账单、钱包流水、实例、节点与令牌、工单及图片附件、公告、站点设置、Logo 和各类加密保存的密钥。只有备份自身的设置和记录不在其中，所以还原旧备份不会把备份设置也改回去。
- **文件**：`vpsbill-日期-时间-来源.tar`，内含 `manifest.json`（时间、版本、数据库结构版本、加密密钥指纹、SHA-256）和 `database.dump`（PostgreSQL custom 格式，已压缩）。
- **每次备份**：先写入 API 容器的 `/var/lib/vpsbill/backups`（编排文件为它挂载了 `backups` 卷），配置了 WebDAV 时再上传一份。WebDAV 上传失败时，本地备份照常保留，记录显示「部分成功」。
- **定时**：关闭、每天 HH:MM（UTC+8）或每隔 N 小时。多个 API 实例只会有一个执行。
- **保留**：本地和 WebDAV 分别保留最近 N 份定时和手动备份；「还原前自动」和「上传」的文件不会自动删除。
- **WebDAV**：支持坚果云（`https://dav.jianguoyun.com/dav/`，使用应用密码）、Nextcloud / ownCloud、群晖和威联通 NAS、AList 等。「测试连通性」会依次创建目录、写入、列出并删除一个测试文件。自签名证书的 NAS 可以勾选「跳过证书校验」。

还原：

1. 在「本地备份」里选一份，或点「上传备份文件」从电脑上传，或在「WebDAV 备份」里点「拉取并还原」（先下载到本地并校验完整性）。
2. 确认弹窗显示备份时间和版本，输入「还原」后开始。
3. 系统依次：
   - 校验文件的 SHA-256；
   - 拒绝来自更新版本的备份；
   - 自动把当前数据备份一份（「还原前自动」）；
   - 暂停后台任务；
   - 在一个数据库事务里清空并导入备份。任何一步失败都会整体回滚。
4. 完成后 API 进程自动退出，由 Docker 重新拉起，并自动补齐数据库迁移。所有人需要重新登录，结果写在「备份记录」里。

注意：

- **ENCRYPTION_KEY 不在备份里**，请单独保管 `.env`。备份清单记录了密钥指纹：还原到密钥不同的服务器时会提示，因为还原后支付密钥、节点令牌、SMTP 和 WebDAV 密码无法解密。换服务器迁移的顺序是：
  1. 新服务器按部署文档装好；
  2. 把 `.env` 里的 `ENCRYPTION_KEY`、`SESSION_SECRET` 改成原服务器的值；
  3. `docker compose up -d`；
  4. 在后台填好同一个 WebDAV，拉取并还原。
- 多个 API 实例时，还原前先停掉其他实例，还原后再启动。
- 上传备份文件没有大小限制，但 Cloudflare Tunnel 单个请求最多 100 MB。更大的文件可以直接复制进卷里：`docker compose cp 文件 api:/var/lib/vpsbill/backups/`，文件名需保持 `vpsbill-YYYYMMDD-HHMMSS-upload.tar` 这种格式。
- 页面提示「备份目录没有挂载持久卷」时，说明编排文件是旧版，请按下文「更新」重新下载。

### 命令行脚本（源码部署）

`backup.sh` / `restore.sh` 直接在宿主机上调用容器里的 pg_dump / pg_restore，适合源码部署或 API 起不来时应急：

```sh
./backup.sh                 # 或 ./backup.sh /mnt/offsite/vpsbill
./restore.sh backups/vpsbill-YYYYMMDDTHHMMSSZ.dump --confirm
```

脚本生成的 `.dump` 与后台的 `.tar` 格式不同，不能互相导入。

## AES 主密钥轮换

```sh
./backup.sh
./rotate-key.sh --confirm
```

脚本停止 API，在单个数据库事务中重新加密全部 CLICD API Key、服务 root 密码、已启用及待确认的 TOTP 密钥，以及安装页保存的支付、通知和 Metrics 密钥；任一记录解密失败则整体回滚。成功后原子替换 `.env` 并重建 API 容器。

## 日常检查

- `/health/live`：进程存活。
- `/health/ready`：数据库可用且迁移完成。
- `/metrics`：携带 `Authorization: Bearer <安装页生成或填写的 Metrics Token>` 采集。
- 定期执行一次恢复演练，不能只验证备份文件存在。
