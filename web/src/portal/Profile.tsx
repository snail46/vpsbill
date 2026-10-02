import { CustomerIdentity } from '../api'
import { StatusBadge, SecuritySettings } from '../shared/ui'
import { NotifyPanel, TelegramPanel } from '../Telegram'
import { displayCurrency } from '../shared/currency'
import { t } from '../shared/i18n'

export function CustomerProfile({ customer }: { customer: CustomerIdentity }) {
  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">ACCOUNT</p>
          <h2>{t('账户资料与安全')}</h2>
          <p>{t('管理个人账户身份、登录密码及 TOTP 二步验证设置。')}</p>
        </div>
      </div>

      <div className="panel profile-panel">
        <div>
          <span>{t('姓名 / 昵称')}</span>
          <strong>{customer.display_name}</strong>
        </div>
        <div>
          <span>{t('登录邮箱')}</span>
          <strong>{customer.email}</strong>
        </div>
        <div>
          <span>{t('账户角色')}</span>
          <strong>{customer.role === 'owner' ? t('所有者') : customer.role}</strong>
        </div>
        <div>
          <span>{t('账户状态')}</span>
          <StatusBadge status={customer.account_status} />
        </div>
        <div>
          <span>{t('账户 ID')}</span>
          <code>{customer.account_id}</code>
        </div>
        <div>
          <span>{t('显示币种')}</span>
          <code>{displayCurrency()}</code>
        </div>
      </div>

      <TelegramPanel />
      <NotifyPanel email={customer.email} />
      <SecuritySettings enabled={customer.mfa_enabled} customer />
    </section>
  )
}
