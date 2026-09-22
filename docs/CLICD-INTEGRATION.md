# CLICD 集成约定

本系统只从后端访问 CLICD。节点 API Key 加密存储，绝不返回给客户浏览器。

## 所需权限

- `host:read`：节点接入检查和容量对账。
- `container:read`：实例查重、开通结果确认和运行状态对账。
- `container:create`：支付完成后的自动开通。
- `container:power`：客户中心的开机、关机和重启。
- `container:delete`：欠费保留期结束后的自动删除。

建议为财务系统创建独立 API Key，并只授予以上权限。重装、密码重置、网络修改等其他高风险权限当前不需要。

## 已使用接口

| 方法 | 路径 | 用途 |
|---|---|---|
| `GET` | `/api/v1/host-info` | 节点健康和容量 |
| `GET` | `/api/v1/containers/{id-or-name}` | 幂等查重和状态对账 |
| `POST` | `/api/v1/containers` | 创建 LXC/KVM 实例 |
| `POST` | `/api/v1/containers/{id-or-name}/start` | 开机 |
| `POST` | `/api/v1/containers/{id-or-name}/stop` | 关机 |
| `POST` | `/api/v1/containers/{id-or-name}/restart` | 重启 |
| `DELETE` | `/api/v1/containers/{id-or-name}/delete` | 欠费延期删除 |

电源操作由 CLICD 异步执行。本系统收到任务后仅记录“期望状态”，最终运行状态由定时对账确认。

## 安全边界

- 客户操作先通过账户与服务归属校验，再写入任务队列。
- 同一服务、同一动作只能存在一个未完成任务。
- 浏览器不直接连接 CLICD，也无法读取节点地址或 API Key。
- 实例名是稳定幂等标识；创建超时后会先查询实例，再决定是否重试。
- 对账发现实例缺失时只标记告警，不自动重建或删除。
