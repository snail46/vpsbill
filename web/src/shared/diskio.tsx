import { useEffect, useRef } from 'react'
import type { PlanRecord, StockCapacityRecord } from '../api'

type DiskIO = Pick<PlanRecord, 'disk_read_mbps' | 'disk_write_mbps' | 'disk_read_iops' | 'disk_write_iops'>

const fields: { name: keyof DiskIO; label: string }[] = [
  { name: 'disk_read_mbps', label: '磁盘读 MB/s' },
  { name: 'disk_write_mbps', label: '磁盘写 MB/s' },
  { name: 'disk_read_iops', label: '磁盘读 IOPS' },
  { name: 'disk_write_iops', label: '磁盘写 IOPS' },
]

// diskIOText summarises a plan's disk limits for plan cards; empty when the
// plan has none.
export function diskIOText(plan: Partial<DiskIO>) {
  const read = plan.disk_read_mbps || 0
  const write = plan.disk_write_mbps || 0
  if (!read && !write && !plan.disk_read_iops && !plan.disk_write_iops) return ''
  const speed = read || write ? `磁盘 ${read || '不限'}/${write || '不限'} MB/s` : '磁盘'
  const iops = plan.disk_read_iops || plan.disk_write_iops ? ` · ${plan.disk_read_iops || '不限'}/${plan.disk_write_iops || '不限'} IOPS` : ''
  return speed + iops
}

// DiskIOFields edits a plan's per-instance disk limits and suggests values
// from the host's measured disk (capacity comes from the stock preview). A
// new plan starts at the suggestion unless the storage ignores limits.
export function DiskIOFields({ plan, capacity }: { plan?: Partial<DiskIO>; capacity: StockCapacityRecord | null }) {
  const ref = useRef<HTMLDivElement>(null)
  const touched = useRef(false)
  const suggestion = capacity?.disk_io

  const fill = () => {
    if (!suggestion) return
    for (const field of fields) {
      const input = ref.current?.querySelector<HTMLInputElement>(`input[name="${field.name}"]`)
      if (input) input.value = String(suggestion[field.name] || '')
    }
  }

  useEffect(() => {
    if (!plan && !touched.current && suggestion && !suggestion.unsupported?.length) fill()
    // fill reads the latest suggestion; only a new suggestion should refill.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [suggestion?.disk_read_mbps, suggestion?.disk_write_mbps, suggestion?.disk_read_iops, suggestion?.disk_write_iops])

  return (
    <div ref={ref} className="disk-io-field" onInput={() => (touched.current = true)}>
      <div className="disk-io-inputs">
        {fields.map(field => (
          <label key={field.name}>
            <span>{field.label}</span>
            <input name={field.name} type="number" min="0" step="1" placeholder="不限" defaultValue={plan?.[field.name] || ''} />
          </label>
        ))}
      </div>
      <small className="field-hint">
        {suggestion ? (
          <>
            建议每台：读 {suggestion.disk_read_mbps} MB/s、写 {suggestion.disk_write_mbps} MB/s、读 {suggestion.disk_read_iops} IOPS、写{' '}
            {suggestion.disk_write_iops} IOPS。母机磁盘实测读 {suggestion.host.read_mbps} MB/s、写 {suggestion.host.write_mbps} MB/s、读{' '}
            {suggestion.host.read_iops} IOPS、写 {suggestion.host.write_iops} IOPS，按本套餐配置满载约 {suggestion.instances} 台，按其中 1/4
            同时读写、每台最多占一半计算。{' '}
            <button type="button" className="link-button" onClick={() => { touched.current = true; fill() }}>
              填入建议值
            </button>
          </>
        ) : capacity ? (
          '母机还没有上报磁盘测速（Hatch Agent 升级后启动时自动测一次），暂无建议值。'
        ) : (
          '正在读取母机磁盘性能…'
        )}{' '}
        留空不限速；只对 Hatch 节点生效，对新开通和重装的实例生效。
      </small>
      {suggestion?.unsupported?.map(reason => (
        <small key={reason} className="field-hint field-warning">
          {reason}
        </small>
      ))}
    </div>
  )
}

// readDiskIO returns the limits from the form, 0 for fields left empty.
export function readDiskIO(form: FormData): DiskIO {
  const value = (name: string) => Math.max(0, Math.floor(Number(String(form.get(name) ?? '').trim() || 0)))
  return {
    disk_read_mbps: value('disk_read_mbps'),
    disk_write_mbps: value('disk_write_mbps'),
    disk_read_iops: value('disk_read_iops'),
    disk_write_iops: value('disk_write_iops'),
  }
}
