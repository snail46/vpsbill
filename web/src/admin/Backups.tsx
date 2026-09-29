import { ChangeEvent, FormEvent, useEffect, useRef, useState } from 'react'
import { ArchiveRestore, CloudDownload, DatabaseBackup, Download, Lock, PlugZap, RefreshCw, Trash2, Upload, X } from 'lucide-react'
import { api, cached, csrfToken } from '../api'
import { formatBytes } from '../shared/ui'
import { formatTime } from '../shared/time'

type BackupSettings = {
  schedule: 'off' | 'daily' | 'interval'
  daily_at: string
  interval_hours: number
  keep_local: number
  keep_remote: number
  webdav_url: string
  webdav_username: string
  webdav_password_set: boolean
  webdav_directory: string
  webdav_insecure: boolean
  encrypt: boolean
  passphrase_set: boolean
  next_run_at?: string
}
type LocalBackup = { name: string; size_bytes: number; created_at: string; trigger: string; app_version: string; key_matches: boolean; encrypted: boolean; error?: string }
type RemoteBackup = { name: string; size_bytes: number; modified_at: string }
type BackupRun = { id: number; kind: string; trigger: string; file_name: string; size_bytes: number; status: string; webdav_status: string; message: string; started_at: string; finished_at?: string }
type Activity = { kind: string; stage: string; file?: string; started_at: string }
type Overview = {
  settings?: BackupSettings
  local?: LocalBackup[]
  runs?: BackupRun[]
  activity: Activity | null
  directory?: string
  persistent?: boolean
  restoring?: boolean
}

const endpoint = '/api/v1/admin/backups'

const triggerLabels: Record<string, string> = { scheduled: '定时', manual: '手动', 'pre-restore': '还原前自动', upload: '上传' }
const kindLabels: Record<string, string> = { backup: '备份', restore: '还原', fetch: '从 WebDAV 下载' }
const statusLabels: Record<string, [string, string]> = {
  running: ['进行中', 'pending'],
  succeeded: ['成功', 'succeeded'],
  partial: ['部分成功', 'pending'],
  failed: ['失败', 'failed'],
}
const webdavLabels: Record<string, string> = { uploaded: '已上传 WebDAV', skipped: '未配置 WebDAV', failed: 'WebDAV 上传失败' }

function settingsForm(settings?: BackupSettings) {
  return {
    schedule: settings?.schedule ?? 'off',
    daily_at: settings?.daily_at ?? '03:30',
    interval_hours: String(settings?.interval_hours ?? 24),
    keep_local: String(settings?.keep_local ?? 7),
    keep_remote: String(settings?.keep_remote ?? 14),
    webdav_url: settings?.webdav_url ?? '',
    webdav_username: settings?.webdav_username ?? '',
    webdav_password: '',
    webdav_directory: settings?.webdav_directory ?? 'vpsbill',
    webdav_insecure: settings?.webdav_insecure ?? false,
    encrypt: settings?.encrypt ?? false,
    encryption_passphrase: '',
    passphrase_confirm: '',
  }
}

type RestartState = { stage: string; file: string } | null

export function BackupsView() {
  const [data, setData] = useState<Overview | null>(() => cached<Overview>(endpoint) ?? null)
  const [form, setForm] = useState(() => settingsForm(cached<Overview>(endpoint)?.settings))
  const [remote, setRemote] = useState<RemoteBackup[] | null>(null)
  const [loadingRemote, setLoadingRemote] = useState(false)
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [testResult, setTestResult] = useState<{ ok: boolean; message: string } | null>(null)
  const [restoring, setRestoring] = useState<LocalBackup | null>(null)
  const [restart, setRestart] = useState<RestartState>(null)
  const [upload, setUpload] = useState<number | null>(null)
  // After a WebDAV download finishes, its restore dialog opens.
  const pendingRestore = useRef('')
  const formTouched = useRef(false)

  const load = () =>
    api<Overview>(endpoint)
      .then(value => {
        setData(value)
        if (value.settings && !formTouched.current) setForm(settingsForm(value.settings))
        if (pendingRestore.current && !value.activity) {
          const file = value.local?.find(item => item.name === pendingRestore.current)
          const run = value.runs?.find(item => item.kind === 'fetch' && item.file_name === pendingRestore.current)
          pendingRestore.current = ''
          if (file) setRestoring(file)
          else if (run?.status === 'failed') setError(`从 WebDAV 下载失败：${run.message}`)
        }
        return value
      })
      .catch(err => setError(err instanceof Error ? err.message : '加载失败'))

  useEffect(() => {
    void load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Follow a backup or download while it runs.
  const busy = Boolean(data?.activity)
  useEffect(() => {
    if (!busy || restart) return
    const timer = window.setInterval(() => void load(), 2000)
    return () => window.clearInterval(timer)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [busy, restart])

  const update = (key: keyof ReturnType<typeof settingsForm>) => (event: ChangeEvent<HTMLInputElement | HTMLSelectElement>) => {
    formTouched.current = true
    const value = event.target.type === 'checkbox' ? (event.target as HTMLInputElement).checked : event.target.value
    setForm(current => ({ ...current, [key]: value }))
  }

  const body = () => ({
    ...form,
    passphrase_confirm: undefined,
    interval_hours: Number(form.interval_hours) || 0,
    keep_local: Number(form.keep_local) || 0,
    keep_remote: Number(form.keep_remote) || 0,
  })

  async function save(event: FormEvent) {
    event.preventDefault()
    if (form.encryption_passphrase !== form.passphrase_confirm) {
      setError('两次输入的备份口令不一致')
      return
    }
    setSaving(true)
    setError('')
    setNotice('')
    try {
      const settings = await api<BackupSettings>(`${endpoint}/settings`, { method: 'PUT', body: JSON.stringify(body()) })
      formTouched.current = false
      setForm(settingsForm(settings))
      setData(current => (current ? { ...current, settings } : current))
      setNotice('备份设置已保存。')
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  async function testWebDAV() {
    setTesting(true)
    setTestResult(null)
    try {
      const result = await api<{ message: string }>(`${endpoint}/test-webdav`, { method: 'POST', body: JSON.stringify(body()) })
      setTestResult({ ok: true, message: result.message })
    } catch (err) {
      setTestResult({ ok: false, message: err instanceof Error ? err.message : '连接失败' })
    } finally {
      setTesting(false)
    }
  }

  async function runNow() {
    setError('')
    setNotice('')
    try {
      await api(`${endpoint}/run`, { method: 'POST' })
      setNotice('已开始备份，完成后会出现在下面的列表里。')
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '备份失败')
    }
  }

  async function loadRemote() {
    setLoadingRemote(true)
    setError('')
    try {
      setRemote(await api<RemoteBackup[]>(`${endpoint}/remote`))
    } catch (err) {
      setError(err instanceof Error ? err.message : '读取 WebDAV 失败')
    } finally {
      setLoadingRemote(false)
    }
  }

  async function fetchRemote(file: RemoteBackup) {
    setError('')
    setNotice('')
    const local = data?.local?.find(item => item.name === file.name)
    if (local) {
      setRestoring(local)
      return
    }
    try {
      await api(`${endpoint}/remote/${encodeURIComponent(file.name)}/fetch`, { method: 'POST' })
      pendingRestore.current = file.name
      setNotice(`正在从 WebDAV 下载 ${file.name}，下载并校验完成后会弹出还原确认。`)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '下载失败')
    }
  }

  async function remove(file: LocalBackup) {
    if (!window.confirm(`删除本地备份 ${file.name}？WebDAV 上的副本不受影响。`)) return
    try {
      await api(`${endpoint}/local/${encodeURIComponent(file.name)}`, { method: 'DELETE' })
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '删除失败')
    }
  }

  function uploadFile(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0]
    event.target.value = ''
    if (!file) return
    setError('')
    setNotice('')
    setUpload(0)
    // XMLHttpRequest reports upload progress; fetch does not.
    const request = new XMLHttpRequest()
    request.open('POST', `${endpoint}/upload`)
    request.setRequestHeader('X-CSRF-Token', csrfToken(`${endpoint}/upload`))
    request.setRequestHeader('Content-Type', 'application/octet-stream')
    request.upload.onprogress = progress => {
      if (progress.lengthComputable) setUpload(Math.round((progress.loaded / progress.total) * 100))
    }
    request.onload = () => {
      setUpload(null)
      let payload: { data?: LocalBackup; message?: string } = {}
      try {
        payload = JSON.parse(request.responseText)
      } catch {
        // not JSON (a proxy error page)
      }
      if (request.status >= 200 && request.status < 300 && payload.data) {
        setNotice(`已上传并校验：${payload.data.name}。`)
        void load().then(() => setRestoring(payload.data ?? null))
      } else {
        setError(payload.message || `上传失败（HTTP ${request.status}）${request.status === 413 ? '：文件超过了反向代理允许的大小' : ''}`)
      }
    }
    request.onerror = () => {
      setUpload(null)
      setError('上传中断，请检查网络后重试')
    }
    request.send(file)
  }

  if (restart) return <RestartPanel state={restart} />

  const settings = data?.settings
  const activity = data?.activity
  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">BACKUP & RESTORE</p>
          <h2>数据备份</h2>
          <p>备份包含平台全部数据（客户、订单、账单、实例、节点、工单及附件、站点设置和各类密钥）。每次备份先保存到本地，再上传到 WebDAV；可以从本地、上传的文件或 WebDAV 还原。</p>
        </div>
        <div className="form-actions">
          <button className="secondary-button" onClick={() => void load()}>
            <RefreshCw size={15} />刷新
          </button>
          <button className="primary-button" disabled={busy} onClick={() => void runNow()}>
            <DatabaseBackup size={15} />
            {activity?.kind === 'backup' ? '备份中…' : '立即备份'}
          </button>
        </div>
      </div>

      {error && <div className="form-error" role="alert">{error}</div>}
      {notice && <div className="form-success" role="status">{notice}</div>}
      {data && data.persistent === false && (
        <div className="note-banner warn">
          本地备份目录 {data.directory} 没有挂载持久卷，重建容器后本地备份会丢失。请按部署文档更新 docker-compose.yml（为 api 服务挂载 backups 卷）后执行 docker compose up -d。
        </div>
      )}
      {activity && (
        <div className="note-banner backup-activity" role="status">
          <span className="spinner small" />
          {activity.stage}
          {activity.file ? `：${activity.file}` : ''}（开始于 {formatTime(activity.started_at)}）
        </div>
      )}

      <form className="backup-settings" onSubmit={save}>
        <section className="panel">
          <div className="panel-heading">
            <h3>定时备份</h3>
            {settings?.next_run_at && <span className="muted-text">下次：{formatTime(settings.next_run_at)}</span>}
          </div>
          <div className="form-grid">
            <label>
              <span>备份计划</span>
              <select value={form.schedule} onChange={update('schedule')}>
                <option value="off">关闭（只手动备份）</option>
                <option value="daily">每天定时</option>
                <option value="interval">每隔几小时</option>
              </select>
            </label>
            {form.schedule === 'daily' && (
              <label>
                <span>每天几点（UTC+8）</span>
                <input type="time" value={form.daily_at} onChange={update('daily_at')} required />
              </label>
            )}
            {form.schedule === 'interval' && (
              <label>
                <span>间隔小时数（1–168）</span>
                <input type="number" min={1} max={168} value={form.interval_hours} onChange={update('interval_hours')} required />
              </label>
            )}
            <label>
              <span>本地保留份数</span>
              <input type="number" min={1} max={365} value={form.keep_local} onChange={update('keep_local')} required />
              <small>只轮换定时和手动备份；还原前备份和上传的文件需要手动删除</small>
            </label>
            <label>
              <span>WebDAV 保留份数</span>
              <input type="number" min={1} max={365} value={form.keep_remote} onChange={update('keep_remote')} required />
            </label>
          </div>
        </section>

        <section className="panel">
          <div className="panel-heading">
            <h3>WebDAV 远程备份</h3>
          </div>
          <div className="form-grid">
            <label className="wide">
              <span>WebDAV 地址</span>
              <input type="url" value={form.webdav_url} onChange={update('webdav_url')} placeholder="https://dav.jianguoyun.com/dav/ 或 https://cloud.example.com/remote.php/dav/files/admin/" />
              <small>留空表示只备份到本地</small>
            </label>
            <label>
              <span>用户名</span>
              <input value={form.webdav_username} onChange={update('webdav_username')} autoComplete="off" />
            </label>
            <label>
              <span>密码 / 应用密码{settings?.webdav_password_set ? '（留空保持不变）' : ''}</span>
              <input type="password" value={form.webdav_password} onChange={update('webdav_password')} autoComplete="new-password" />
            </label>
            <label>
              <span>备份目录</span>
              <input value={form.webdav_directory} onChange={update('webdav_directory')} placeholder="vpsbill" />
              <small>不存在时自动创建</small>
            </label>
            <label className="checkbox wide">
              <input type="checkbox" checked={form.webdav_insecure} onChange={update('webdav_insecure')} /> 跳过证书校验（自签名证书的 NAS）
            </label>
            {testResult && <div className={testResult.ok ? 'form-success wide' : 'form-error wide'}>{testResult.message}</div>}
          </div>
        </section>

        <section className="panel backup-encryption">
          <div className="panel-heading">
            <h3>备份加密</h3>
            {settings?.encrypt && <span className="tag"><Lock size={12} /> 已开启</span>}
          </div>
          <div className="form-grid">
            <label className="checkbox wide">
              <input type="checkbox" checked={form.encrypt} onChange={update('encrypt')} /> 用口令加密备份（AES-256-GCM）
            </label>
            {(form.encrypt || settings?.passphrase_set) && (
              <>
                <label>
                  <span>备份口令{settings?.passphrase_set ? '（留空保持不变）' : ''}</span>
                  <input type="password" value={form.encryption_passphrase} onChange={update('encryption_passphrase')} autoComplete="new-password" minLength={12} placeholder="至少 12 个字符" />
                </label>
                <label>
                  <span>再输入一次</span>
                  <input type="password" value={form.passphrase_confirm} onChange={update('passphrase_confirm')} autoComplete="new-password" />
                </label>
              </>
            )}
            <p className="muted-text wide">
              开启后，之后的备份只有凭口令才能打开，存在 WebDAV 上也读不到内容；备份时间和版本仍然可见。本机还原时自动使用保存的口令；换服务器，或还原修改口令之前的备份时，需要输入当时的口令。
              <strong>口令丢失后备份无法恢复</strong>，请记在密码管理器里。已有的备份不会被重新加密。
            </p>
          </div>
        </section>

        <div className="form-actions">
          <button type="button" className="secondary-button" disabled={testing || !form.webdav_url.trim()} onClick={() => void testWebDAV()}>
            <PlugZap size={15} />
            {testing ? '测试中…' : '测试连通性'}
          </button>
          <button className="primary-button" disabled={saving}>{saving ? '保存中…' : '保存设置'}</button>
        </div>
      </form>

      <section className="panel">
        <div className="panel-heading">
          <h3>本地备份</h3>
          <label className={upload !== null || busy ? 'secondary-button disabled' : 'secondary-button'}>
            <Upload size={15} />
            {upload !== null ? `上传中 ${upload}%` : '上传备份文件'}
            <input type="file" accept=".tar" hidden disabled={upload !== null || busy} onChange={uploadFile} />
          </label>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>备份文件</th>
                <th>备份时间</th>
                <th>来源</th>
                <th>大小</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {(data?.local ?? []).map(file => (
                <tr key={file.name}>
                  <td>
                    <strong className="truncate" title={file.name}>{file.name}</strong>
                    {file.encrypted && <small className="tag-inline"><Lock size={11} /> 已加密</small>}
                    {file.error ? <small className="danger-text">{file.error}</small> : !file.key_matches && <small className="warn-text">加密密钥与当前不同</small>}
                  </td>
                  <td>{formatTime(file.created_at)}</td>
                  <td>{triggerLabels[file.trigger] ?? file.trigger ?? '—'}</td>
                  <td>{formatBytes(file.size_bytes)}</td>
                  <td>
                    <div className="row-actions">
                      <a className="text-button" href={`${endpoint}/local/${encodeURIComponent(file.name)}/download`} download>
                        <Download size={13} />下载
                      </a>
                      <button className="text-button" disabled={busy || Boolean(file.error)} onClick={() => setRestoring(file)}>
                        <ArchiveRestore size={13} />还原
                      </button>
                      <button className="text-button danger" disabled={busy} onClick={() => void remove(file)}>
                        <Trash2 size={13} />删除
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
              {data && !data.local?.length && (
                <tr>
                  <td colSpan={5} className="empty-state">还没有本地备份，点右上角「立即备份」创建第一份。</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      <section className="panel">
        <div className="panel-heading">
          <h3>WebDAV 备份</h3>
          <button className="secondary-button" disabled={loadingRemote || !settings?.webdav_url} onClick={() => void loadRemote()}>
            <CloudDownload size={15} />
            {loadingRemote ? '读取中…' : remote ? '刷新列表' : '读取列表'}
          </button>
        </div>
        {!settings?.webdav_url ? (
          <p className="muted-text">配置并保存 WebDAV 后，可以在这里查看远程备份，并拉取到本机还原（换服务器迁移时也用这里）。</p>
        ) : (
          remote && (
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>备份文件</th>
                    <th>修改时间</th>
                    <th>大小</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {remote.map(file => (
                    <tr key={file.name}>
                      <td>
                        <strong className="truncate" title={file.name}>{file.name}</strong>
                      </td>
                      <td>{file.modified_at && !file.modified_at.startsWith('0001') ? formatTime(file.modified_at) : '—'}</td>
                      <td>{formatBytes(file.size_bytes)}</td>
                      <td>
                        <button className="text-button" disabled={busy} onClick={() => void fetchRemote(file)}>
                          <ArchiveRestore size={13} />拉取并还原
                        </button>
                      </td>
                    </tr>
                  ))}
                  {!remote.length && (
                    <tr>
                      <td colSpan={4} className="empty-state">WebDAV 目录里还没有备份。</td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          )
        )}
      </section>

      <section className="panel">
        <div className="panel-heading">
          <h3>备份记录</h3>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>时间</th>
                <th>操作</th>
                <th>文件</th>
                <th>结果</th>
                <th>说明</th>
              </tr>
            </thead>
            <tbody>
              {(data?.runs ?? []).map(run => {
                const [label, tone] = statusLabels[run.status] ?? [run.status, 'pending']
                return (
                  <tr key={run.id}>
                    <td>{formatTime(run.started_at)}</td>
                    <td>
                      {kindLabels[run.kind] ?? run.kind}
                      {run.kind === 'backup' && <small>{triggerLabels[run.trigger] ?? run.trigger}</small>}
                    </td>
                    <td>
                      <span className="truncate" title={run.file_name}>{run.file_name || '—'}</span>
                      {run.size_bytes > 0 && <small>{formatBytes(run.size_bytes)}</small>}
                    </td>
                    <td>
                      <span className={`status-badge ${tone}`}>{label}</span>
                      {run.webdav_status && <small>{webdavLabels[run.webdav_status] ?? run.webdav_status}</small>}
                    </td>
                    <td className="backup-message">{run.message}</td>
                  </tr>
                )
              })}
              {data && !data.runs?.length && (
                <tr>
                  <td colSpan={5} className="empty-state">暂无记录</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      {restoring && (
        <RestoreDialog
          file={restoring}
          onClose={() => setRestoring(null)}
          onStarted={stage => {
            setRestoring(null)
            setRestart({ stage, file: restoring.name })
          }}
        />
      )}
    </section>
  )
}

function RestoreDialog({ file, onClose, onStarted }: { file: LocalBackup; onClose: () => void; onStarted: (stage: string) => void }) {
  const [understood, setUnderstood] = useState(false)
  const [confirm, setConfirm] = useState('')
  const [keyMismatch, setKeyMismatch] = useState(!file.key_matches)
  const [allowMismatch, setAllowMismatch] = useState(false)
  const [passphrase, setPassphrase] = useState('')
  const [needPassphrase, setNeedPassphrase] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function submit(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    setError('')
    try {
      const result = await api<{ activity: Activity | null }>(`${endpoint}/local/${encodeURIComponent(file.name)}/restore`, {
        method: 'POST',
        body: JSON.stringify({ confirm, allow_key_mismatch: allowMismatch, passphrase }),
      })
      onStarted(result.activity?.stage ?? '正在准备还原')
    } catch (err) {
      const status = (err as { status?: number }).status
      if (status === 409 && err instanceof Error && err.message.includes('ENCRYPTION_KEY')) setKeyMismatch(true)
      if (err instanceof Error && err.message.includes('口令')) setNeedPassphrase(true)
      setError(err instanceof Error ? err.message : '还原失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="modal-backdrop" role="dialog" aria-modal="true">
      <form className="modal panel" onSubmit={submit}>
        <div className="panel-heading">
          <h3>还原数据</h3>
          <button type="button" className="icon-button" aria-label="关闭" onClick={onClose}>
            <X size={16} />
          </button>
        </div>
        <dl className="backup-facts">
          <dt>备份文件</dt>
          <dd>{file.name}</dd>
          <dt>备份时间</dt>
          <dd>{formatTime(file.created_at)}</dd>
          <dt>来源 / 版本</dt>
          <dd>
            {triggerLabels[file.trigger] ?? file.trigger} · {file.app_version || '未知版本'}
          </dd>
        </dl>
        <div className="note-banner warn">
          还原会把平台的<strong>全部数据</strong>替换成备份时的状态：之后产生的订单、付款、工单、客户注册和设置改动都会消失。还原前系统会自动把当前数据备份一份（「还原前自动」），还原失败会整体回滚。完成后服务自动重启，所有人需要重新登录。
        </div>
        {keyMismatch && (
          <div className="note-banner danger">
            这份备份使用了另一个 ENCRYPTION_KEY。还原后支付密钥、节点令牌、SMTP 和 WebDAV 密码等无法解密，需要重新填写。换服务器迁移时，请先把 .env 里的 ENCRYPTION_KEY 改成原服务器的值并重启，再来还原。
            <label className="checkbox">
              <input type="checkbox" checked={allowMismatch} onChange={event => setAllowMismatch(event.target.checked)} /> 我了解，仍然还原
            </label>
          </div>
        )}
        {file.encrypted && (
          <label>
            <span>备份口令{needPassphrase ? '' : '（留空使用已保存的口令）'}</span>
            <input type="password" value={passphrase} onChange={event => setPassphrase(event.target.value)} autoComplete="off" required={needPassphrase} />
          </label>
        )}
        <label className="checkbox">
          <input type="checkbox" checked={understood} onChange={event => setUnderstood(event.target.checked)} /> 我已了解当前数据会被替换
        </label>
        <label>
          <span>输入「还原」确认</span>
          <input value={confirm} onChange={event => setConfirm(event.target.value)} placeholder="还原" autoComplete="off" />
        </label>
        {error && <div className="form-error">{error}</div>}
        <div className="form-actions">
          <button type="button" className="secondary-button" onClick={onClose}>取消</button>
          <button className="danger-button" disabled={busy || !understood || confirm.trim() !== '还原' || (keyMismatch && !allowMismatch) || (needPassphrase && !passphrase)}>
            {busy ? '正在校验备份…' : '开始还原'}
          </button>
        </div>
      </form>
    </div>
  )
}

// RestartPanel follows a restore: its stages, then the restart, then sends
// the administrator to sign in again.
function RestartPanel({ state }: { state: NonNullable<RestartState> }) {
  const [stage, setStage] = useState(state.stage)
  const [phase, setPhase] = useState<'restoring' | 'restarting' | 'done' | 'aborted'>('restoring')

  useEffect(() => {
    let stopped = false
    let sawDown = false
    const tick = async () => {
      if (stopped) return
      try {
        const response = await fetch(endpoint, { credentials: 'same-origin', headers: { Accept: 'application/json' }, cache: 'no-store' })
        const payload = (await response.json().catch(() => ({}))) as { data?: Overview }
        if (response.ok && payload.data?.restoring) {
          setStage(payload.data.activity?.stage ?? '正在还原')
        } else if (response.ok && payload.data?.activity?.kind === 'restore') {
          setStage(payload.data.activity.stage)
        } else if (response.ok || response.status === 401 || response.status === 403) {
          // The restarted API answers normally (or the session is gone);
          // without a restart the restore stopped before touching the data.
          setPhase(sawDown ? 'done' : 'aborted')
          return
        } else {
          sawDown = true
          setPhase('restarting')
        }
      } catch {
        sawDown = true
        setPhase('restarting')
      }
      window.setTimeout(() => void tick(), 2000)
    }
    const timer = window.setTimeout(() => void tick(), 1500)
    return () => {
      stopped = true
      window.clearTimeout(timer)
    }
  }, [])

  return (
    <section className="workspace-panel">
      <div className="panel restore-progress">
        {phase === 'restoring' || phase === 'restarting' ? <span className="spinner" /> : <ArchiveRestore size={40} />}
        <h2>{phase === 'done' ? '还原已结束' : phase === 'aborted' ? '还原没有执行' : phase === 'restarting' ? '服务正在重启…' : '正在还原数据'}</h2>
        <p>
          {phase === 'done'
            ? '服务已重启。请重新登录，然后在「数据备份 → 备份记录」查看还原结果。'
            : phase === 'aborted'
              ? '还原在改动数据之前就停止了（例如还原前备份或解包失败），当前数据没有变化。原因见「备份记录」。'
              : phase === 'restarting'
                ? '数据库已处理完毕，正在等待服务重新启动，通常不超过一分钟。'
                : `${stage}：${state.file}。请不要关闭页面。`}
        </p>
        {(phase === 'done' || phase === 'aborted') && (
          <button className="primary-button" onClick={() => window.location.reload()}>
            {phase === 'done' ? '重新登录' : '返回备份页面'}
          </button>
        )}
      </div>
    </section>
  )
}
