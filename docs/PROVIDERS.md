# 母鸡对接层（Provider）

业务核心（开通任务、对账、客户中心、商家后台）只依赖 `internal/provider`，不直接调用任何母鸡 API。每种母鸡后端是一个独立的适配器包，在 `init` 中通过 `provider.Register` 注册，并由 `cmd/server/main.go` 以空白导入启用。

## 契约

| 接口 | 是否必需 | 用途 |
|---|---|---|
| `Driver` | 必需 | 宿主机容量、镜像列表、幂等创建、查询、开关机重启、删除 |
| `Reinstaller` | 可选 | 重装系统 |
| `PasswordResetter` | 可选 | 重置 root 密码 |
| `PortMapper` | 可选 | NAT 端口映射增删改 |
| `Metrics` | 可选 | 实例用量、流量、历史曲线 |
| `Console` | 可选 | WebSSH / VNC 票据与同源反代目标 |
| `HostProbe` | 可选 | 后台宿主机探针详情 |
| `Suspender` | 可选 | 欠费暂停 / 恢复（不关机） |

可选能力通过类型断言发现，`provider.CapabilitiesOf` 由同一断言生成能力标志，随客户实例运行时接口返回（`capabilities` 字段），前端据此显示或隐藏操作。

适配器必须遵守：

- `EnsureInstance` 以实例名为稳定身份，可安全重试；遇到含糊错误要按名称回查，不能重复创建。
- 实例不存在时返回包装了 `provider.ErrNotFound` 的错误；`DeleteInstance` 对不存在的实例视为成功。
- 节点自身返回的错误转换为 `*provider.Error`，其 `Message` 会展示给客户。
- 工厂函数不得发起网络请求，连通性由调用方通过 `HostInfo` 验证。
- 节点凭据只以 AES-256-GCM 密文落库，调用方用 `provider.OpenSealed` 解密后立即交给驱动。

## 已接入

| 类型 | 后端 | 状态 |
|---|---|---|
| `clicd` | [CLICD](https://cli.cd) `/api/v1`，`X-API-Key` | 已接入，全部可选能力（除 `Suspender`） |

## 规划

### `lxdapi` — xkatld/lxdapi-web-server

接口依据官方开源 FOSSBilling 插件 `Fmis/fossbilling/Servicelxdapi/Service.php`（main-stable 分支）：

- 地址 `https://{host}:8443`，Header `X-API-Hash: <后端 Hash>`，默认自签证书，需要按节点配置是否校验证书。
- `POST /api/system/containers` 创建；`GET/DELETE /api/system/containers/{name}` 查询 / 删除。
- `POST /api/system/containers/{name}/action?action=start|stop|restart|pause|resume|reinstall|reset-password`。
- `POST /api/system/console/create-token` 控制台；`POST /api/system/traffic/reset?name=` 重置流量。
- `pause` / `resume` 对应 `Suspender`，用于欠费暂停。

注意：vps-billing-agent-pack 中名为 `lxdapi` 的适配器对接的是原生 LXD REST（`/1.0/instances`，客户端证书），并非 xkatld 的 LXDAPI，不能复用。

### `runman` — narwhal-cloud/runman-agent

协议为 Agent 主动拨入平台的 gRPC 双向流（`AgentGateway.Connect`，Bearer token）。上游 `main.go` 将平台地址写死为常量，官方二进制无法连接自建平台。

该仓库截至 2026-09-26 **没有任何开源许可证**（GitHub API `license: null`）。公开仓库不等于开源：未授权时默认保留全部权利，修改和分发其代码都需要作者许可。在取得作者书面授权（或上游添加开源许可证 / 可配置平台地址）之前，本项目不 fork、不分发 runman-agent，也不复制其源码或 proto 文件。
