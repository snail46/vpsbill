import { useState } from 'react'
import { Check, Copy, Plus, Server, Trash2 } from 'lucide-react'
import type { PendingAgentRecord } from '../api'
import { formatTime } from './time'

const runtimeNames: Record<string, string> = { lxc: 'LXC', lxd: 'LXD', incus: 'Incus', podman: 'Podman' }

// InstallCommand shows the account's install command with a copy button.
export function InstallCommand({ command }: { command: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <div className="code-line">
      <code>{command}</code>
      <button
        type="button"
        className="icon-button"
        aria-label="复制安装命令"
        onClick={() => {
          void navigator.clipboard?.writeText(command)
          setCopied(true)
          window.setTimeout(() => setCopied(false), 1500)
        }}
      >
        {copied ? <Check size={14} /> : <Copy size={14} />}
      </button>
    </div>
  )
}

type RuntimeChoice = 'podman' | 'lxc'

const runtimeChoices: { id: RuntimeChoice; title: string; detail: string }[] = [
  {
    id: 'podman',
    title: 'Podman 容器',
    detail: '最省资源：1 核 64 MB 也能开机。脚本自动安装 Podman、建数据盘和网络，并构建 Debian 12、Alpine 3.22 两个镜像。适合 NAT 小鸡。',
  },
  {
    id: 'lxc',
    title: 'LXC 系统容器',
    detail: '完整的 Linux 系统（systemd、软件包与普通 VPS 一致），兼容性更好，空闲内存比 Podman 略多。使用已装的 Incus / LXD；都没有时自动安装 Incus，建存储池和网桥，并导入 Debian 12、Ubuntu 22.04、Alpine 3.22。',
  },
]

// ConnectSteps explains the one-command install; the same text serves the
// hosting center and the node page. The installer is told which runtime to
// set up, so nobody gets Podman when they meant LXC.
export function ConnectSteps({ command, where, actionLabel }: { command: string; where: string; actionLabel: string }) {
  const [runtime, setRuntime] = useState<RuntimeChoice | null>(null)
  return (
    <div className="install-step">
      <ol className="step-list">
        <li>选择这台母机用哪种虚拟化（一台母机选一种；装好后不能直接切换，换方式需要重新安装）。</li>
        <li>在母机上以 root 运行生成的命令。脚本会自动识别公网 IP，安装所选虚拟化并做好网络调优。</li>
        <li>装好后约半分钟，母机会出现在{where}的「待接入」列表里，点「{actionLabel}」补充名称和地域即可，不用复制令牌。</li>
      </ol>
      <div className="runtime-choice" role="radiogroup" aria-label="虚拟化方式">
        {runtimeChoices.map(choice => (
          <label key={choice.id} className={runtime === choice.id ? 'runtime-option selected' : 'runtime-option'}>
            <input type="radio" name="agent-runtime" checked={runtime === choice.id} onChange={() => setRuntime(choice.id)} />
            <span>
              <strong>{choice.title}</strong>
              <small>{choice.detail}</small>
            </span>
          </label>
        ))}
      </div>
      {runtime ? <InstallCommand command={`${command} --runtime ${runtime}`} /> : <p className="muted-text">先选择虚拟化方式，再复制安装命令。</p>}
      {command.includes('--server http://') && (
        <p className="muted-text">
          当前站点还没有启用 HTTPS。Agent 只允许经回环地址用 HTTP 连接，所以现在只有与计费站同机的服务器能接入；外部母机需要站点先配置域名和 HTTPS。
        </p>
      )}
    </div>
  )
}

// PendingAgents lists hosts that ran the install command but are not added
// yet.
export function PendingAgents({
  agents,
  actionLabel,
  onAdd,
  onDismiss,
}: {
  agents: PendingAgentRecord[]
  actionLabel: string
  onAdd: (agent: PendingAgentRecord) => void
  onDismiss: (agent: PendingAgentRecord) => void
}) {
  if (!agents.length) return <p className="muted-text pending-empty">暂无待接入的母机。运行上面的命令后，这里会自动出现。</p>
  return (
    <div className="pending-agents">
      {agents.map(agent => (
        <article key={agent.id} className="pending-agent">
          <Server size={18} aria-hidden />
          <div className="pending-agent-main">
            <strong>{agent.hostname || '未命名主机'}</strong>
            <small>
              {agent.public_ipv4 || agent.remote_ip || '未知 IP'} · {agent.runtimes.map(item => runtimeNames[item] || item).join(' / ') || '无运行时'}
              {agent.capacity && ` · ${agent.capacity.vcpu} 核 / ${agent.capacity.ram_mb} MB / ${agent.capacity.disk_gb} GB`}
            </small>
            <small>
              <span className={agent.online ? 'status-dot online' : 'status-dot'} />
              {agent.online ? '在线' : `离线 · 最后连接 ${formatTime(agent.last_seen_at)}`}
            </small>
          </div>
          <div className="pending-agent-actions">
            <button type="button" className="primary-button compact" disabled={!agent.online} onClick={() => onAdd(agent)}>
              <Plus size={14} />
              {actionLabel}
            </button>
            <button type="button" className="icon-button" aria-label="移除" title="从列表移除（重新连接后会再次出现）" onClick={() => onDismiss(agent)}>
              <Trash2 size={14} />
            </button>
          </div>
        </article>
      ))}
    </div>
  )
}
