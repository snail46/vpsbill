import { CustomerIdentity } from '../api'
import { StatusBadge, SecuritySettings } from '../shared/ui'
import { TelegramPanel } from '../Telegram'

export function CustomerProfile({ customer }: { customer: CustomerIdentity }) {
  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">ACCOUNT</p>
          <h2>账户资料与安全</h2>
          <p>管理个人账户身份、登录密码及 TOTP 二步验证设置。</p>
        </div>
      </div>

      <div className="panel profile-panel">
        <div>
          <span>姓名 / 昵称</span>
          <strong>{customer.display_name}</strong>
        </div>
        <div>
          <span>登录邮箱</span>
          <strong>{customer.email}</strong>
        </div>
        <div>
          <span>账户角色</span>
          <strong>{customer.role === 'owner' ? '所有者' : customer.role}</strong>
        </div>
        <div>
          <span>账户状态</span>
          <StatusBadge status={customer.account_status} />
        </div>
        <div>
          <span>账户 ID</span>
          <code>{customer.account_id}</code>
        </div>
        <div>
          <span>默认计费币种</span>
          <code>{customer.default_currency}</code>
        </div>
      </div>

      <TelegramPanel />
      <SecuritySettings enabled={customer.mfa_enabled} customer />
    </section>
  )
}
