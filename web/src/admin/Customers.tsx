import { FormEvent, useEffect, useState } from 'react'
import { X } from 'lucide-react'
import { AccountRecord, api } from '../api'
import { AdminWalletPanel, walletMoney } from '../Wallet'
import { PageActions, StatusBadge } from '../shared/ui'
import { formatTime } from '../shared/time'

export function CustomersView() {
  const [customers, setCustomers] = useState<AccountRecord[]>([])
  const [showForm, setShowForm] = useState(false)
  const [error, setError] = useState('')
  const [updating, setUpdating] = useState('')
  const [resetLink, setResetLink] = useState<{ name: string; email: string; link: string; expires_at: string } | null>(null)
  const [walletFor, setWalletFor] = useState<AccountRecord | null>(null)
  const [copied, setCopied] = useState(false)

  async function issueResetLink(customer: AccountRecord) {
    if (!window.confirm(`为客户【${customer.display_name}】生成一次性密码重置链接？此前未使用的链接会失效。`)) return
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
      setError(err instanceof Error ? err.message : '生成重置链接失败')
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

  async function toggleStatus(customer: AccountRecord) {
    const status = customer.status === 'active' ? 'suspended' : 'active'
    if (status === 'suspended' && !window.confirm(`确认暂停客户【${customer.display_name}】的账户？这会撤销其全部登录会话。`)) {
      return
    }
    setUpdating(customer.id)
    try {
      await api(`/api/v1/admin/customers/${customer.id}`, { method: 'PATCH', body: JSON.stringify({ status }) })
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '更新状态失败')
    } finally {
      setUpdating('')
    }
  }

  return (
    <section className="workspace-panel">
      <PageActions
        eyebrow="CUSTOMER ACCOUNTS"
        title="客户管理"
        description="客户是订单、账单明细、VPS 实例与资金交易流水的统一归属主体。"
        action={() => setShowForm(true)}
        actionLabel="新增客户"
      />

      {error && <div className="form-error">{error}</div>}

      {resetLink && (
        <div className="inline-form reset-link-panel">
          <div className="inline-form-heading">
            <div>
              <h3>{resetLink.name} 的密码重置链接</h3>
              <p>
                登录邮箱 {resetLink.email}，{formatTime(resetLink.expires_at)} 前有效，只能使用一次。请通过工单或其他可信渠道发给客户本人。
              </p>
            </div>
            <button className="icon-button" onClick={() => setResetLink(null)} aria-label="关闭">
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
              {copied ? '已复制' : '复制链接'}
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

      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>客户主体</th>
              <th>客户类型</th>
              <th>账单邮箱</th>
              <th>计费币种</th>
              <th>账户余额</th>
              <th>账户状态</th>
              <th>注册时间</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            {customers.map(customer => (
              <tr key={customer.id}>
                <td>
                  <strong>{customer.display_name}</strong>
                  <small>{customer.id}</small>
                </td>
                <td>{customer.kind === 'business' ? '企业客户' : '个人客户'}</td>
                <td>{customer.billing_email}</td>
                <td><code>{customer.default_currency}</code></td>
                <td className={(customer.balance_minor || 0) < 0 ? 'amount-negative' : ''}>{walletMoney(customer.balance_minor || 0, customer.default_currency)}</td>
                <td><StatusBadge status={customer.status} /></td>
                <td>{formatTime(customer.created_at)}</td>
                <td>
                  <div className="row-actions">
                    <button
                      className="text-button"
                      disabled={updating === customer.id}
                      onClick={() => void toggleStatus(customer)}
                    >
                      {customer.status === 'active' ? '暂停账户' : '恢复正常'}
                    </button>
                    <button
                      className="text-button"
                      disabled={updating === customer.id}
                      onClick={() => void issueResetLink(customer)}
                    >
                      重置密码链接
                    </button>
                    <button className="text-button" onClick={() => setWalletFor(customer)}>
                      余额明细
                    </button>
                  </div>
                </td>
              </tr>
            ))}
            {!customers.length && (
              <tr>
                <td colSpan={8} className="empty-state">尚未创建任何客户账户</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </section>
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
      setError(err instanceof Error ? err.message : '创建失败')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="inline-form">
      <div className="inline-form-heading">
        <div>
          <h3>创建新客户</h3>
          <p>录入客户基础信息。企业客户可按需选填法定企业全称及纳税人识别号。</p>
        </div>
        <button className="icon-button" onClick={onClose}><X size={18} /></button>
      </div>

      <form className="form-grid" onSubmit={submit}>
        <label>
          <span>客户类型</span>
          <select name="kind">
            <option value="individual">个人</option>
            <option value="business">企业</option>
          </select>
        </label>
        <label>
          <span>显示名称 / 昵称</span>
          <input name="display_name" required placeholder="张三 / 某某科技" />
        </label>
        <label>
          <span>账单通知邮箱</span>
          <input name="billing_email" type="email" required placeholder="billing@example.com" />
        </label>
        <label>
          <span>法定名称（企业）</span>
          <input name="legal_name" placeholder="某某网络科技有限公司" />
        </label>
        <label>
          <span>统一社会信用代码 / 税号</span>
          <input name="tax_id" placeholder="91310000XXXXXXXXXX" />
        </label>
        <label>
          <span>国家 / 地区代码</span>
          <input name="country_code" maxLength={2} defaultValue="CN" />
        </label>
        <label>
          <span>默认计费币种</span>
          <select name="default_currency">
            <option value="CNY">CNY 人民币</option>
            <option value="USD">USD 美元</option>
          </select>
        </label>

        {error && <div className="form-error wide">{error}</div>}

        <div className="form-actions wide">
          <button type="button" className="secondary-button" onClick={onClose}>取消</button>
          <button className="primary-button" disabled={saving}>
            {saving ? '正在创建…' : '保存客户'}
          </button>
        </div>
      </form>
    </div>
  )
}
