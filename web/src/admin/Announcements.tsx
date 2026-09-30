import { FormEvent, useEffect, useState } from 'react'
import { Pin, Plus } from 'lucide-react'
import { api, cached, AnnouncementRecord } from '../api'
import { formatTime } from '../shared/time'
import { confirmDialog } from '../shared/dialog'

// AnnouncementsView lets staff publish notices on the customer overview.
export function AnnouncementsView() {
  const [items, setItems] = useState<AnnouncementRecord[]>(() => cached<AnnouncementRecord[]>('/api/v1/admin/announcements') ?? [])
  const [editing, setEditing] = useState<AnnouncementRecord | 'new' | null>(null)
  const [error, setError] = useState('')

  const load = () =>
    api<AnnouncementRecord[]>('/api/v1/admin/announcements')
      .then(setItems)
      .catch(err => setError(err instanceof Error ? err.message : '加载失败'))
  useEffect(() => {
    void load()
  }, [])

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    const body = {
      title: form.get('title'),
      body: form.get('body'),
      pinned: form.get('pinned') === 'on',
      published: form.get('published') === 'on',
    }
    setError('')
    try {
      const id = editing && editing !== 'new' ? editing.id : ''
      await api(`/api/v1/admin/announcements${id ? `/${id}` : ''}`, { method: id ? 'PUT' : 'POST', body: JSON.stringify(body) })
      setEditing(null)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
    }
  }

  async function remove(item: AnnouncementRecord) {
    if (!(await confirmDialog({ title: `删除公告「${item.title}」？`, message: '删除后客户将不再看到这条公告。', confirmText: '删除', danger: true }))) return
    try {
      await api(`/api/v1/admin/announcements/${item.id}`, { method: 'DELETE' })
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '删除失败')
    }
  }

  const current = editing && editing !== 'new' ? editing : undefined
  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">ANNOUNCEMENTS</p>
          <h2>平台公告</h2>
          <p>已发布的公告显示在客户中心「服务概览」，置顶的排在最前。</p>
        </div>
        <button className="primary-button compact" onClick={() => setEditing('new')}>
          <Plus size={14} />新建公告
        </button>
      </div>
      {error && <div className="form-error">{error}</div>}
      {editing && (
        <form key={current?.id ?? 'new'} className="panel form-grid" onSubmit={save}>
          <label className="wide">
            <span>标题（1–120 字）</span>
            <input name="title" required maxLength={120} defaultValue={current?.title} />
          </label>
          <label className="wide">
            <span>正文（最多 4000 字，换行会保留）</span>
            <textarea name="body" rows={6} maxLength={4000} defaultValue={current?.body} />
          </label>
          <label className="check-row">
            <input type="checkbox" name="published" defaultChecked={current ? current.published : true} />
            发布（不勾选则保存为草稿）
          </label>
          <label className="check-row">
            <input type="checkbox" name="pinned" defaultChecked={current?.pinned} />
            置顶
          </label>
          <div className="form-actions wide">
            <button type="button" className="secondary-button" onClick={() => setEditing(null)}>取消</button>
            <button className="primary-button compact">保存</button>
          </div>
        </form>
      )}
      <div className="table-wrap">
        <table>
          <thead>
            <tr><th>标题</th><th>状态</th><th>发布时间</th><th>操作</th></tr>
          </thead>
          <tbody>
            {items.map(item => (
              <tr key={item.id}>
                <td>
                  <strong>{item.pinned && <Pin size={12} />} {item.title}</strong>
                  {item.body && <small>{item.body.slice(0, 60)}{item.body.length > 60 ? '…' : ''}</small>}
                </td>
                <td><span className={item.published ? 'tag success' : 'tag'}>{item.published ? '已发布' : '草稿'}</span></td>
                <td>{formatTime(item.created_at)}</td>
                <td className="row-actions">
                  <button className="text-button" onClick={() => setEditing(item)}>编辑</button>
                  <button className="text-button danger" onClick={() => void remove(item)}>删除</button>
                </td>
              </tr>
            ))}
            {!items.length && (
              <tr><td colSpan={4} className="empty-state">还没有公告</td></tr>
            )}
          </tbody>
        </table>
      </div>
    </section>
  )
}
