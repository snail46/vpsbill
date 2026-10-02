import { FormEvent, useEffect, useState } from 'react'
import { X } from 'lucide-react'
import { AccountRecord, api, cached } from '../api'
import { AdminWalletPanel, walletMoney } from '../Wallet'
import { PageActions, StatusBadge } from '../shared/ui'
import { formatTime } from '../shared/time'
import { AdminLink, FilterBar, SearchFilter, SelectFilter, adminHref, includesText, matchesAny, useUrlFilters, type Option } from './filters'
import { confirmDialog } from '../shared/dialog'
import { toast } from '../shared/toast'
import { t } from '../shared/i18n'

const customerKinds: Option[] = [['individual', t('个人客户')], ['business', t('企业客户')]]
const customerStatuses: Option[] = [['active', t('正常')], ['suspended', t('已暂停')], ['pending', t('待激活')], ['closed', t('已关闭')]]
const balanceFilters: Option[] = [['positive', t('有余额')], ['zero', t('余额为零')], ['negative', t('欠款（余额为负）')]]

function balanceMatches(filter: string, balance: number) {
  return !filter || (filter === 'positive' ? balance > 0 : filter === 'negative' ? balance < 0 : balance === 0)
}

export function CustomersView() {
  const { filters, set, reset, active } = useUrlFilters(['q', 'kind', 'email', 'balance', 'status'] as const)
  const [customers, setCustomers] = useState<AccountRecord[]>(() => cached<AccountRecord[]>('/api/v1/admin/customers') ?? [])
  const [showForm, setShowForm] = useState(false)
  const [error, setError] = useState('')
  const [updating, setUpdating] = useState('')
  const [resetLink, setResetLink] = useState<{ name: string; email: string; link: string; expires_at: string } | null>(null)
  const [walletFor, setWalletFor] = useState<AccountRecord | null>(null)
  const [copied, setCopied] = useState(false)

  async function issueResetLink(customer: AccountRecord) {
    if (!(await confirmDialog({ title: t('为客户【{0}】生成密码重置链接？', customer.display_name), message: t('链接只能使用一次；此前未使用的链接会失效。'), confirmText: t('生成链接') }))) return
    setUpdating(customer.id)
    setError('')
    setCopied(false)
    try {
      const result = await api<{ email: string; link: string; expires_at: string }>(
        `/api/v1/admin/customers/${customer.id}/password-reset`,
        { method: 'POST' }
      )
      setResetLink({ name: customer.display_name, ...result })
    } catch (err) {
      setError(err instanceof Error ? err.message : t('生成重置链接失败'))
    } finally {
      setUpdating('')
    }
  }

  const load = () =>
    api<AccountRecord[]>('/api/v1/admin/customers')
      .then(setCustomers)
      .catch(err => setError(err.message))

  useEffect(() => {
    void load()
  }, [])

  const shown = customers.filter(
    customer =>
      includesText(filters.q, customer.display_name, customer.legal_name, customer.id) &&
      matchesAny(filters.kind, customer.kind) &&
      includesText(filters.email, customer.billing_email) &&
      balanceMatches(filters.balance, customer.balance_minor || 0) &&
      matchesAny(filters.status, customer.status),
  )

  async function toggleStatus(customer: AccountRecord) {
    const status = customer.status === 'active' ? 'suspended' : 'active'
    if (status === 'suspended' && !(await confirmDialog({ title: t('暂停客户【{0}】的账户？', customer.display_name), message: t('这会撤销其全部登录会话。'), confirmText: t('暂停账户'), danger: true }))) {
      return
    }
    setUpdating(customer.id)
    try {
      await api(`/api/v1/admin/customers/${customer.id}`, { method: 'PATCH', body: JSON.stringify({ status }) })
      await load()
      toast('success', status === 'suspended' ? t('已暂停 {0} 的账户', customer.display_name) : t('已恢复 {0} 的账户', customer.display_name))
    } catch (err) {
      toast('error', t('更新账户状态失败'), err instanceof Error ? err.message : undefined)
      setError(err instanceof Error ? err.message : t('更新状态失败'))
    } finally {
      setUpdating('')
    }
  }

  return (
    <section className="workspace-panel">
      <PageActions
        eyebrow="CUSTOMER ACCOUNTS"
        title={t('客户管理')}
        description={t('客户是订单、账单明细、VPS 实例与资金交易流水的统一归属主体。')}
        action={() => setShowForm(true)}
        actionLabel={t('新增客户')}
      />

      {error && <div className="form-error">{error}</div>}

      {resetLink && (
        <div className="inline-form reset-link-panel">
          <div className="inline-form-heading">
            <div>
              <h3>{t('{0} 的密码重置链接', resetLink.name)}</h3>
              <p>
                {t('登录邮箱 {0}，{1} 前有效，只能使用一次。请通过工单或其他可信渠道发给客户本人。', resetLink.email, formatTime(resetLink.expires_at))}
              </p>
            </div>
            <button className="icon-button" onClick={() => setResetLink(null)} aria-label={t('关闭')}>
              <X size={16} />
            </button>
          </div>
          <div className="reset-link-row">
            <code>{resetLink.link}</code>
            <button
              className="secondary-button compact"
              onClick={() => {
                void navigator.clipboard?.writeText(resetLink.link).then(() => setCopied(true), () => setCopied(false))
              }}
            >
              {copied ? t('已复制') : t('复制链接')}
            </button>
          </div>
        </div>
      )}

      {walletFor && (
        <AdminWalletPanel key={walletFor.id} accountID={walletFor.id} name={walletFor.display_name} onClose={() => setWalletFor(null)} onChanged={() => void load()} />
      )}

      {showForm && (
        <CustomerForm
          onClose={() => setShowForm(false)}
          onCreated={() => {
            setShowForm(false)
            load()
          }}
        />
      )}

      <FilterBar shown={shown.length} total={customers.length} active={active} onReset={reset}>
        <SearchFilter label={t('客户主体')} value={filters.q} onChange={value => set('q', value)} placeholder={t('名称、企业全称或 ID')} />
        <SelectFilter label={t('客户类型')} value={filters.kind} onChange={value => set('kind', value)} options={customerKinds} />
        <SearchFilter label={t('账单邮箱')} value={filters.email} onChange={value => set('email', value)} placeholder={t('邮箱')} />
        <SelectFilter label={t('账户余额')} value={filters.balance} onChange={value => set('balance', value)} options={balanceFilters} />
        <SelectFilter label={t('账户状态')} value={filters.status} onChange={value => set('status', value)} options={customerStatuses} />
      </FilterBar>

      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>{t('客户主体')}</th>
              <th>{t('客户类型')}</th>
              <th>{t('账单邮箱')}</th>
              <th>{t('账户余额')}</th>
              <th>{t('实例 / 订单 / 账单 / 流水')}</th>
              <th>{t('账户状态')}</th>
              <th>{t('注册时间')}</th>
              <th>{t('操作')}</th>
            </tr>
          </thead>
          <tbody>
            {shown.map(customer => (
              <tr key={customer.id}>
                <td>
                  <strong>{customer.display_name}</strong>
                  <small>{customer.id}</small>
                </td>
                <td>{customer.kind === 'business' ? t('企业客户') : t('个人客户')}</td>
                <td>{customer.billing_email}</td>
                <td className={(customer.balance_minor || 0) < 0 ? 'amount-negative' : ''}>
                  {walletMoney(customer.balance_minor || 0, customer.default_currency)}
                  <small>{customer.default_currency}</small>
                </td>
                <td>
                  <CustomerLinks customer={customer} />
                </td>
                <td><StatusBadge status={customer.status} /></td>
                <td>{formatTime(customer.created_at)}</td>
                <td>
                  <div className="row-actions">
                    <button
                      className="text-button"
                      disabled={updating === customer.id}
                      onClick={() => void toggleStatus(customer)}
                    >
                      {customer.status === 'active' ? t('暂停账户') : t('恢复正常')}
                    </button>
                    <button
                      className="text-button"
                      disabled={updating === customer.id}
                      onClick={() => void issueResetLink(customer)}
                    >
                      {t('重置密码链接')}
                    </button>
                    <button className="text-button" onClick={() => setWalletFor(customer)}>
                      {t('余额明细')}
                    </button>
                  </div>
                </td>
              </tr>
            ))}
            {!shown.length && (
              <tr>
                <td colSpan={8} className="empty-state">{customers.length ? t('没有符合筛选条件的客户') : t('尚未创建任何客户账户')}</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </section>
  )
}

// CustomerLinks opens the customer's instances, orders, invoices and
// payments: each list page filtered to this customer.
function CustomerLinks({ customer }: { customer: AccountRecord }) {
  const counts = customer.counts
  const links: [string, string, string, string?][] = [
    [t('实例'), adminHref('services', { account: customer.id }), counts ? `${counts.active_services}${counts.services > counts.active_services ? `/${counts.services}` : ''}` : '—', counts && counts.services > counts.active_services ? t('在用 / 全部（含已删除）') : undefined],
    [t('订单'), adminHref('orders', { account: customer.id }), counts ? String(counts.orders) : '—'],
    [t('账单'), adminHref('billing', { account: customer.id }), counts ? String(counts.invoices) : '—', counts?.open_invoices ? t('{0} 张未付', counts.open_invoices) : undefined],
    [t('流水'), adminHref('billing', { payer: customer.id }, 'transactions'), counts ? String(counts.transactions) : '—'],
  ]
  return (
    <div className="customer-links">
      {links.map(([label, href, count, hint]) => (
        <AdminLink key={label} href={href} title={hint ? t('{0}：{1}', label, hint) : t('查看{0}的{1}', customer.display_name, label)}>
          {label} <strong>{count}</strong>
          {label === t('账单') && counts?.open_invoices ? <em>{t('{0} 未付', counts.open_invoices)}</em> : null}
        </AdminLink>
      ))}
    </div>
  )
}

export function CustomerForm({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setSaving(true)
    setError('')
    const data = new FormData(event.currentTarget)
    try {
      await api('/api/v1/admin/customers', {
        method: 'POST',
        body: JSON.stringify({
          kind: data.get('kind'),
          display_name: data.get('display_name'),
          billing_email: data.get('billing_email'),
          legal_name: data.get('legal_name'),
          tax_id: data.get('tax_id'),
          country_code: data.get('country_code'),
          default_currency: data.get('default_currency'),
        }),
      })
      onCreated()
    } catch (err) {
      setError(err instanceof Error ? err.message : t('创建失败'))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="inline-form">
      <div className="inline-form-heading">
        <div>
          <h3>{t('创建新客户')}</h3>
          <p>{t('录入客户基础信息。企业客户可按需选填法定企业全称及纳税人识别号。')}</p>
        </div>
        <button className="icon-button" onClick={onClose}><X size={18} /></button>
      </div>

      <form className="form-grid" onSubmit={submit}>
        <label>
          <span>{t('客户类型')}</span>
          <select name="kind">
            <option value="individual">{t('个人')}</option>
            <option value="business">{t('企业')}</option>
          </select>
        </label>
        <label>
          <span>{t('显示名称 / 昵称')}</span>
          <input name="display_name" required placeholder={t('张三 / 某某科技')} />
        </label>
        <label>
          <span>{t('账单通知邮箱')}</span>
          <input name="billing_email" type="email" required placeholder="billing@example.com" />
        </label>
        <label>
          <span>{t('法定名称（企业）')}</span>
          <input name="legal_name" placeholder={t('某某网络科技有限公司')} />
        </label>
        <label>
          <span>{t('统一社会信用代码 / 税号')}</span>
          <input name="tax_id" placeholder="91310000XXXXXXXXXX" />
        </label>
        <label>
          <span>{t('国家 / 地区代码')}</span>
          <input name="country_code" maxLength={2} defaultValue="CN" />
        </label>
        <label>
          <span>{t('默认计费币种')}</span>
          <select name="default_currency">
            <option value="CNY">{t('CNY 人民币')}</option>
            <option value="USD">{t('USD 美元')}</option>
          </select>
        </label>

        {error && <div className="form-error wide">{error}</div>}

        <div className="form-actions wide">
          <button type="button" className="secondary-button" onClick={onClose}>{t('取消')}</button>
          <button className="primary-button" disabled={saving}>
            {saving ? t('正在创建…') : t('保存客户')}
          </button>
        </div>
      </form>
    </div>
  )
}
