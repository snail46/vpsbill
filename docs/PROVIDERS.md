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

节点的非敏感设置保存在 `nodes.provider_options`（JSONB），由描述符中的 `options` 字段声明，接入时经 `provider.NormalizeOptions` 校验。后台「新增节点」表单按 `GET /api/v1/admin/provider-types` 返回的描述符动态渲染。

## 已接入

| 类型 | 后端 | 虚拟化 | 可选能力 |
|---|---|---|---|
| `clicd` | [CLICD](https://cli.cd) `/api/v1`，`X-API-Key` | LXC / KVM | 重装、重置密码、端口映射、监控、WebSSH/VNC、宿主机探针 |
| `lxdapi` | [xkatld/lxdapi-web-server](https://github.com/xkatld/lxdapi-web-server) 系统接口 `/api/system`，`X-API-Hash` | LXC | 重装、重置密码、端口映射、监控（无历史曲线）、暂停/恢复 |

### LXDAPI 说明

接口依据 LXDAPI 服务端源码（MIT 许可）中 `internal/api/system` 的处理函数：

- 所有响应都是 HTTP 200，结果看响应体的 `code`：`200` 成功、`404` 不存在、`401` 密钥错误。
- 创建、删除、重装为异步任务。适配器轮询 `GET /api/system/tasks/detail?id=` 直到完成；创建前先查 `GET /api/system/tasks?name=`，若有进行中的创建任务就继续等待它，不会重复提交。
- 端口映射使用 `/api/system/port-mapping`（`version=v4`），按映射 ID 升序对应计费系统的映射序号。修改映射 = 释放旧规则 + 在同一公网端口分配新规则，失败时恢复旧规则。
- 系统接口不提供镜像列表和宿主机容量，因此需要在节点设置中填写：NAT 公网 IPv4、出口网卡、端口范围、可售镜像别名、可分配 vCPU/内存/磁盘。
- LXDAPI 默认使用自签名证书。节点必须二选一：开启「校验 HTTPS 证书」（受信任证书），或填写证书 SHA-256 指纹进行固定。获取指纹：

  ```sh
  openssl s_client -connect 节点IP:8443 </dev/null 2>/dev/null | openssl x509 -noout -fingerprint -sha256
  ```

- 暂不支持：WebSSH/VNC（LXDAPI 控制台是其自带页面，无法经本站同源代理）、历史监控曲线、宿主机探针详情。
- `Suspender`（pause/resume）已实现，但欠费流程目前仍使用关机，接入暂停需要另行调整生命周期。

## 规划

### `hatch` — 自研 Agent

参见 [Hatch Agent](HATCH-AGENT.md)。
