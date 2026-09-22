# CLICD 集成约定

本系统只从后端访问 CLICD。节点 API Key 加密存储，绝不返回给客户浏览器。

## 所需权限

- `host:read`：节点接入检查、容量对账和完整宿主机探针。
- `dashboard:read`：宿主机详情中的容器概览。
- `image:read`：读取节点已启用、已下载的 LXC/KVM 模板。
- `container:read`：实例查重、开通结果确认和运行状态对账。
- `container:create`：支付完成后的自动开通。
- `container:power`：客户中心的开机、关机和重启。
- `container:delete`：欠费保留期结束后的自动删除。

建议为财务系统创建独立 API Key，并只授予以上权限。重装、密码重置、网络修改等其他高风险权限当前不需要。

## 已使用接口

| 方法 | 路径 | 用途 |
|---|---|---|
| `GET` | `/api/v1/host-info` | 节点健康和容量 |
| `GET` | `/api/v1/dashboard` | 宿主机容器概览 |
| `GET` | `/api/v1/host-history` | 宿主机历史指标 |
| `GET` | `/api/v1/host-report` | 宿主机硬件与系统报告 |
| `GET` | `/api/v1/images` | 聚合可售 LXC/KVM 模板 |
| `GET` | `/api/v1/containers/{id-or-name}` | 幂等查重和状态对账 |
| `POST` | `/api/v1/containers` | 创建 LXC/KVM 实例 |
| `POST` | `/api/v1/containers/{id-or-name}/start` | 开机 |
| `POST` | `/api/v1/containers/{id-or-name}/stop` | 关机 |
| `POST` | `/api/v1/containers/{id-or-name}/restart` | 重启 |
| `DELETE` | `/api/v1/containers/{id-or-name}/delete` | 欠费延期删除 |

电源操作由 CLICD 异步执行。本系统收到任务后仅记录“期望状态”，最终运行状态由定时对账确认。

创建实例默认使用 CLICD 的 `auto_password` 模式，由节点生成初始 SSH 密码，避免声明自定义密码却未提供 `ssh_password`。确定性的 4xx 参数错误会直接进入任务错误信息；只有超时、网络中断和服务端错误等结果不确定的情况才按实例名对账。

## 安全边界

- 客户操作先通过账户与服务归属校验，再写入任务队列。
- 同一服务、同一动作只能存在一个未完成任务。
- 浏览器不直接连接 CLICD，也无法读取节点地址或 API Key。
- 实例名是稳定幂等标识；创建超时后会先查询实例，再决定是否重试。
- 对账发现实例缺失时只标记告警，不自动重建或删除。
