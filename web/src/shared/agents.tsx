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

// ConnectSteps explains the one-command install; the same text serves the
// hosting center and the node page.
export function ConnectSteps({ command, where, actionLabel }: { command: string; where: string; actionLabel: string }) {
  return (
    <div className="install-step">
      <ol className="step-list">
        <li>
          在母机上以 root 运行下面这条命令（所有母机通用，不用改任何内容）。脚本会自动识别公网 IP 和已安装的 LXD / Incus，并安装 Podman、做好网络调优。
        </li>
        <li>装好后约半分钟，母机会出现在{where}的「待接入」列表里，点「{actionLabel}」补充名称和地域即可，不用复制令牌。</li>
      </ol>
      <InstallCommand command={command} />
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
