export type StaffUser = {
  id: string
  email: string
  display_name: string
  role: string
  permissions: string[]
  mfa_enabled: boolean
}

export type CustomerIdentity = {
  id: string
  account_id: string
  email: string
  display_name: string
  account_status: string
  default_currency: string
  role: string
  mfa_enabled: boolean
  email_verified: boolean
}

// Overcommit is a node's oversell ratio per resource; 1 sells exactly
// what the agent reports.
export type Overcommit = { cpu: number; ram: number; disk: number; traffic: number }
export type HostHealth = {
  cpus: number
  load1: number
  load5: number
  load15: number
  mem_total_mb: number
  mem_available_mb: number
  swap_total_mb: number
  swap_free_mb: number
  disks?: { name: string; total_gb: number; used_gb: number }[]
  quota_errors?: Record<string, string>
}
// NodeSupply is how a node's sellable capacity is derived and whether it
// may sell right now.
export type NodeSupply = {
  reported_vcpu: number
  reported_ram_mb: number
  reported_disk_gb: number
  overcommit: Overcommit
  health?: HostHealth
  health_hold_reason?: string
  health_hold_since?: string
  shared_machine: boolean
  shared_machine_with?: string[]
  sold_traffic_gb: number
}

export type NodeRecord = NodeSupply & {
  id: string
  region_code: string
  region_name: string
  name: string
  provider_type: string
  base_url: string
  provider_options?: Record<string, unknown>
  status: string
  virtualization_types: string[]
  capacity: Record<string, unknown>
  capacity_vcpu: number
  capacity_ram_mb: number
  capacity_disk_gb: number
  last_seen_at: string | null
  expires_at?: string
  traffic_quota_gb?: number
  traffic_used_bytes?: number
  // owner_account_id marks a hosted node; retired nodes take no new
  // instances.
  owner_account_id?: string
  retired_at?: string
}

export type ProviderOptionField = {
  key: string; label: string; kind: 'text' | 'number' | 'bool' | 'list'; required: boolean; placeholder?: string; help?: string
}
export type ProviderTypeRecord = {
  type: string; name: string; credential_label: string; base_url_hint: string
  virtualization_types: string[]; agent_managed: boolean; options: ProviderOptionField[] | null
}

export type MoneyTotal = { currency: string; amount_minor: number }
export type OperationsOverviewRecord = {
  accounts: number; orders_30_days: number; services: number; running_services: number; open_invoices: number; overdue_services: number
  nodes: number; online_nodes: number; open_tickets: number; pending_jobs: number; failed_jobs: number
  capacity_vcpu: number; reserved_vcpu: number; capacity_ram_mb: number; reserved_ram_mb: number; capacity_disk_gb: number; reserved_disk_gb: number
  revenue_30_days: MoneyTotal[]; outstanding: MoneyTotal[]
}
export type HostProbeRecord = NodeRecord & { reserved_vcpu: number; reserved_ram_mb: number; reserved_disk_gb: number }
export type HostProbeDetailRecord = {
  node: NodeRecord
  sources: Record<'dashboard'|'host_info'|'host_history'|'host_report', unknown>
  errors: Partial<Record<'dashboard'|'host_info'|'host_history'|'host_report', string>>
  fetched_at: string
}
export type PaymentSettingsRecord = {
  gateway: {
    type: string; generic_base_url: string; generic_secret_configured: boolean
    alipay_app_id: string; alipay_gateway_url: string; alipay_private_key_configured: boolean; alipay_public_key_configured: boolean
    epay_api_url: string; epay_partner_id: string; epay_payment_type: string; epay_merchant_key_configured: boolean
  }
  callbacks: Record<string,string>
}

export type Price = {
  currency: string
  billing_cycle: string
  amount_minor: number
  setup_fee_minor: number
  // Set on hosted market listings when the host lease ends inside the
  // cycle: what the buyer pays and until when.
  charge_minor?: number
  period_end?: string
  // purchase_limit caps units sold at this price (null = unlimited).
  purchase_limit?: number | null
  sold?: number
}

export type PlanRecord = {
  id: string
  code: string
  name: string
  provider_type: string
  virtualization: 'lxc' | 'kvm' | 'podman'
  vcpu: number
  ram_mb: number
  disk_gb: number
  traffic_gb: number
  network_down_mbps: number
  network_up_mbps: number
  snapshot_limit: number
  assign_nat: boolean
  port_mapping_count: number
  assign_ipv4: boolean
  ipv4_count: number
  assign_ipv6: boolean
  ipv6_count: number
  default_template_id: string
  allowed_template_ids: string[]
  enabled: boolean
  version: number
  prices: Price[]
  owner_account_id?: string
  node_id?: string
  description?: string
  purchase_limit?: number
  early_refund?: boolean
  // stock_limit is the plan's total stock (null = capacity only);
  // stock_held counts live instances and units in payable orders.
  stock_limit?: number | null
  stock_held?: number
  // Per-instance disk limits; 0 is unlimited.
  disk_read_mbps?: number
  disk_write_mbps?: number
  disk_read_iops?: number
  disk_write_iops?: number
  // category_id groups a platform plan ('' = uncategorised).
  category_id?: string
  // node_selection is how a platform plan picks a node: only node_ids
  // ('nodes'), the fullest node that fits ('pack') or the emptiest
  // ('spread'). region_ids are where the plan can be ordered.
  node_selection?: NodeSelection
  node_ids?: string[]
  region_ids?: string[]
  // tags are short labels on the plan's shop card.
  tags?: string[]
  // sort_order orders plans in the admin list and the shop, smaller first.
  sort_order?: number
}

export type NodeSelection = 'nodes' | 'pack' | 'spread'

export type PlanCategoryRecord = {
  id: string
  name: string
  description: string
  sort_order: number
  plans: number
}

// PlanPresetRecord keeps the settings a series of plans shares; the admin
// plan form defines their shape.
export type PlanPresetRecord = {
  id: string
  name: string
  settings: Record<string, unknown>
  updated_at: string
}

export type StockCapacityRecord = {
  max: number
  held: number
  free_vcpu: number
  free_ram_mb: number
  free_disk_gb: number
  free_traffic_gb?: number
  nodes: number
  // disk_io suggests per-instance disk limits from the weakest machine's
  // measured disk; absent until a node reports one.
  disk_io?: {
    disk_read_mbps: number
    disk_write_mbps: number
    disk_read_iops: number
    disk_write_iops: number
    host: { read_mbps: number; write_mbps: number; read_iops: number; write_iops: number; measured_at: string }
    instances: number
    unsupported?: string[]
  }
}

export type AvailableTemplateRecord = {
  id: string
  provider_type: string
  name: string
  virtualization: 'lxc' | 'kvm' | 'podman'
  distro: string
  release: string
  arch: string
  description: string
  node_ids: string[]
  node_names: string[]
}

export type AccountRecord = {
  id: string
  kind: 'individual' | 'business'
  status: string
  display_name: string
  billing_email: string
  country_code?: string
  default_currency: string
  created_at: string
  balance_minor?: number
  legal_name?: string
  // counts are in the admin customer list only.
  counts?: { services: number; active_services: number; orders: number; invoices: number; open_invoices: number; transactions: number }
}

export type RegionRecord = { id: string; code: string; name: string }

export type OrderRecord = {
  id: string
  number: string
  account_id: string
  customer_name: string
  status: string
  currency: string
  subtotal_minor: number
  discount_minor?: number
  tax_minor: number
  total_minor: number
  invoice_id: string
  invoice_number: string
  created_at: string
}

export type InvoiceRecord = {
  id: string
  number: string
  account_id: string
  customer_name: string
  order_id: string | null
  service_id?: string
  kind: 'initial' | 'renewal' | 'topup'
  status: string
  currency: string
  total_minor: number
  balance_minor: number
  due_at: string
  paid_at: string | null
  period_start?: string
  period_end?: string
  created_at: string
}

export type TransactionRecord = {
  id: string
  account_id: string
  customer_name: string
  invoice_number: string
  provider: string
  provider_transaction_id: string
  type: string
  status: string
  currency: string
  amount_minor: number
  created_at: string
}

export type ServiceRecord = {
  id: string
  account_id: string
  customer_name: string
  plan_name: string
  region_name: string
  node_name?: string
  status: string
  runtime_status: string
  instance_name: string
  external_id?: string
  primary_ipv4?: string
  primary_ipv6?: string
  next_due_at?: string
  last_reconciled_at?: string
  last_reconcile_error?: string
  created_at: string
}

export type ProvisioningJobRecord = {
  id: string
  service_id: string
  instance_name: string
  customer_name: string
  action: string
  status: string
  attempts: number
  available_at: string
  locked_at?: string
  last_error?: string
  created_at: string
  updated_at: string
}

export type CustomerServiceRecord = {
  id: string
  plan_name: string
  region_name: string
  status: string
  runtime_status: string
  desired_runtime_status?: string
  instance_name: string
  virtualization: string
  vcpu: number
  ram_mb: number
  disk_gb: number
  traffic_gb: number
  network_down_mbps: number
  primary_ipv4?: string
  primary_ipv6?: string
  next_due_at?: string
  grace_until?: string
  termination_scheduled_at?: string
  last_reconciled_at?: string
  last_reconcile_error?: string
  host_name?: string
  termination_reason?: string
  acquired_at?: string
  listing_id?: string
  listing_price_minor?: number
  traffic_locked_month?: string
  traffic_rx_bytes?: number | null
  traffic_tx_bytes?: number | null
  traffic_used_bytes?: number | null
  // source is where the instance was sold; via_trade marks a trading
  // market purchase. node_id names a hosted node (its chat room).
  source: 'platform' | 'hosted'
  via_trade: boolean
  node_id?: string
  template_id: string
  auto_renew: boolean
  // password_stored says a root password is kept (the password itself
  // comes from /credential).
  password_stored?: boolean
  billing_cycle: string
  currency: string
  renewal_price_minor: number | null
}

export type AnnouncementRecord = { id: string; title: string; body: string; pinned: boolean; published: boolean; created_at: string; updated_at: string }
export type CustomerOverviewRecord = {
  balance_minor: number
  currency: string
  hosting: { nodes: number; pending_minor: number; released_minor: number }
  announcements: AnnouncementRecord[]
}

// Overdue services keep running through the grace period, so customers can
// still manage them; suspended and terminating ones are read-only.
export const serviceUsable = (status: string) => status === 'active' || status === 'overdue'

export type PortMappingRecord = { container_port: number; host_port: number; host_ip?: string; protocol: string; description: string }
// ReinstallRecord is a service's latest reinstall, which runs in the
// background.
export type ReinstallRecord = {
  template_id: string
  status: 'running' | 'succeeded' | 'failed'
  error?: string
  started_at: string
  finished_at?: string
}

export type ServiceRuntimeRecord = {
  container: {
    id: string; name: string; virtualization: string; status: string; template: string; ip: string; ipv6: string
    vcpu: number; ram_mb: number; disk_gb: number; ssh_port: number; port_mapping_limit: number
    port_mappings: PortMappingRecord[]; monthly_traffic_gb: number; network_down_mbps: number; network_up_mbps: number
  }
  usage?: Record<string, number>
  history?: Array<Record<string, number | string>>
  traffic?: Record<string, number | string>
  templates: AvailableTemplateRecord[]
  errors: Record<string, string>
  capabilities: ProviderCapabilities
}
export type ProviderCapabilities = {
  reinstall: boolean; reset_password: boolean; port_mapping: boolean; metrics: boolean
  console: string[]; host_probe: boolean; suspend: boolean
}
export type ServiceCredentialRecord = { username: string; password: string; stored: boolean }
export type ConsoleTicketRecord = { ticket: string; websocket_path: string }

export type CustomerInvoiceRecord = {
  id: string
  number: string
  status: string
  kind: 'initial' | 'renewal' | 'topup'
  service_id?: string
  currency: string
  total_minor: number
  balance_minor: number
  due_at: string
  paid_at?: string
  period_start?: string
  period_end?: string
  created_at: string
}

export type CustomerTransactionRecord = {
  id: string
  invoice_number?: string
  provider: string
  provider_transaction_id?: string
  type: string
  status: string
  currency: string
  amount_minor: number
  created_at: string
}

export type CustomerCatalogRecord = {
  plans: PlanRecord[]
  regions: RegionRecord[]
  categories?: PlanCategoryRecord[]
  checkout_enabled: boolean
}

export type PaymentIntentRecord = {
  id: string
  invoice_id: string
  invoice_number: string
  merchant_reference: string
  status: string
  currency: string
  amount_minor: number
  checkout_url: string
  expires_at: string
}

export type TicketRecord = {
  id: string
  number: string
  account_id: string
  customer_name: string
  service_id?: string
  instance_name?: string
  subject: string
  priority: 'low' | 'normal' | 'high' | 'urgent'
  status: 'open' | 'customer_reply' | 'staff_reply' | 'resolved' | 'closed'
  assigned_staff?: string
  host_account_id?: string
  host_name?: string
  message_count: number
  last_reply_at: string
  created_at: string
}

export type TicketAttachmentRecord = { id: string; file_name: string; content_type: string; size_bytes: number }
export type TicketMessageRecord = {
  id: string
  author_type: string
  author_name: string
  body: string
  internal: boolean
  created_at: string
  attachments?: TicketAttachmentRecord[]
}
export type TicketDetailRecord = { ticket: TicketRecord; messages: TicketMessageRecord[] }
export type AuditLogRecord = { id: number; actor_type: string; actor_id?: string; action: string; target_type: string; target_id?: string; ip?: string; user_agent?: string; metadata: Record<string, unknown>; created_at: string }

type Envelope<T> = { data: T; message?: string; error?: string }

// Staff and customer sessions use separate cookies; staff APIs live under
// /api/v1/admin and /api/v1/auth.
export function csrfToken(path: string) {
  const name = /^\/api\/v1\/(admin|auth)\//.test(path) ? 'cb_admin_csrf=' : 'cb_csrf='
  const row = document.cookie
    .split('; ')
    .find((item) => item.startsWith(name))
  return row ? decodeURIComponent(row.split('=').slice(1).join('=')) : ''
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const reading = (!init.method || init.method.toUpperCase() === 'GET') && init.body === undefined && !init.signal
  if (!reading) {
    // A reload after a change must not reuse a read that started before it.
    inflight.clear()
    return request<T>(path, init).finally(() => inflight.clear())
  }
  // Two views asking for the same data at once share one request.
  const running = inflight.get(path)
  if (running) return running as Promise<T>
  const pending = request<T>(path, init)
    .then(data => {
      responses.set(path, data)
      store(path, data)
      return data
    })
    .finally(() => {
      if (inflight.get(path) === pending) inflight.delete(path)
    })
  inflight.set(path, pending)
  return pending
}

async function request<T>(path: string, init: RequestInit): Promise<T> {
  const headers = new Headers(init.headers)
  headers.set('Accept', 'application/json')
  if (typeof init.body === 'string') headers.set('Content-Type', 'application/json')
  if (init.method && !['GET', 'HEAD'].includes(init.method.toUpperCase())) {
    headers.set('X-CSRF-Token', csrfToken(path))
  }
  const response = await fetch(path, { ...init, headers, credentials: 'same-origin' })
  if (response.status === 204) return undefined as T
  const payload = (await response.json().catch(() => ({}))) as Partial<Envelope<T>>
  if (!response.ok) {
    const error = new Error(payload.message || '请求失败') as Error & { status?: number }
    error.status = response.status
    throw error
  }
  return payload.data as T
}

// Every successful GET is kept for this page, so a view opened again (or
// opened after prefetch) shows its last data at once while it reloads.
const responses = new Map<string, unknown>()
const inflight = new Map<string, Promise<unknown>>()

// cached is the last response for path, for a view's initial state: from
// this page, or from before a reload of this tab.
export function cached<T>(path: string): T | undefined {
  return (responses.has(path) ? responses.get(path) : restore(path)) as T | undefined
}

// clearCached forgets every response; signing in or out changes whose data
// it is.
export function clearCached() {
  responses.clear()
  // The service worker's stored pages carry who was signed in.
  navigator.serviceWorker?.controller?.postMessage('forget-pages')
  owner = ''
  clearStored()
}

// Responses are also kept in the tab's sessionStorage, for the signed-in
// user only, so a reload shows the page's data at once too. The storage
// ends with the tab and is cleared on sign-out.
const storagePrefix = 'vpsbill:get:'
const ownerKey = 'vpsbill:owner'
const storedLimit = 256 * 1024
let owner = ''

// adoptCache names the signed-in user; what the tab stored for anyone else
// is dropped.
export function adoptCache(id: string) {
  owner = id
  try {
    if (sessionStorage.getItem(ownerKey) !== id) {
      clearStored()
      if (id) sessionStorage.setItem(ownerKey, id)
    }
  } catch {
    // storage unavailable (private mode); the page cache still works
  }
}

// Secrets (root passwords) stay in this page's memory only.
const unstored = /\/credential$/

function store(path: string, data: unknown) {
  if (!owner || unstored.test(path)) return
  try {
    const text = JSON.stringify(data)
    if (text.length <= storedLimit) sessionStorage.setItem(storagePrefix + path, text)
  } catch {
    // full or unavailable
  }
}

function restore(path: string): unknown {
  if (!owner) return undefined
  try {
    const text = sessionStorage.getItem(storagePrefix + path)
    return text ? JSON.parse(text) : undefined
  } catch {
    return undefined
  }
}

function clearStored() {
  try {
    for (const key of Object.keys(sessionStorage)) {
      if (key.startsWith(storagePrefix) || key === ownerKey) sessionStorage.removeItem(key)
    }
  } catch {
    // unavailable
  }
}

// prefetch loads the paths that are not cached yet, a few at a time so the
// open view's own requests are not held up.
export async function prefetch(paths: string[], parallel = 3) {
  const queue = [...new Set(paths)].filter(path => !responses.has(path) && !inflight.has(path))
  const worker = async () => {
    for (let path = queue.shift(); path; path = queue.shift()) {
      if (!responses.has(path)) await api(path).catch(() => undefined)
    }
  }
  await Promise.all(Array.from({ length: parallel }, worker))
}

// imageLabel names a system image; release and arch are optional because
// some node types (Hatch, LXDAPI) only report an alias.
export function imageLabel(item: { name: string; release?: string; arch?: string }) {
  const release = item.release && !item.name.includes(item.release) ? ` ${item.release}` : ''
  return `${item.name}${release}${item.arch ? ` · ${item.arch}` : ''}`
}

export type WalletEntryRecord = {
  id: string
  kind: 'topup' | 'earning' | 'payment' | 'clearance_refund' | 'clearance_penalty' | 'adjustment' | 'refund' | 'trade_purchase' | 'trade_sale'
  amount_minor: number
  balance_after_minor: number
  currency: string
  description: string
  reference_type?: string
  reference_id?: string
  created_at: string
}
export type WalletRecord = { balance_minor: number; currency: string; entries: WalletEntryRecord[] }
export type TopupInvoiceRecord = { id: string; number: string; currency: string; total_minor: number; due_at: string }

export type HostedServiceRecord = {
  id: string
  instance_name: string
  plan_name: string
  status: string
  runtime_status: string
  buyer_name: string
  next_due_at?: string
  created_at: string
  remaining_value_minor: number
}

export type HostedNodeRecord = NodeSupply & {
  id: string
  name: string
  owner_account_id?: string
  owner_name: string
  owner_email?: string
  owner_balance_minor: number
  region_id: string
  region_name: string
  location: string
  line_description: string
  status: string
  listing_status: 'listed' | 'paused' | 'retired'
  virtualization_types: string[]
  expires_at: string
  traffic_quota_gb: number
  capacity_vcpu: number
  capacity_ram_mb: number
  capacity_disk_gb: number
  capacity_cap_vcpu?: number | null
  capacity_cap_ram_mb?: number | null
  capacity_cap_disk_gb?: number | null
  free_vcpu: number
  free_ram_mb: number
  free_disk_gb: number
  last_seen_at?: string
  clearance_hold_until?: string
  retired_at?: string
  retired_reason?: string
  active_services: number
  escrow_holding_minor: number
  host_pending_minor: number
  host_released_minor: number
  fee_minor: number
  created_at: string
  plans: PlanRecord[]
  services?: HostedServiceRecord[]
  mine?: boolean
}

export type MarketRecord = { nodes: HostedNodeRecord[]; fee_percent: number; overcommit_limits: Overcommit }
export type RegionAdminRecord = { id: string; code: string; name: string; enabled: boolean; nodes: number }

export type PendingAgentRecord = {
  id: string
  hostname: string
  agent_version: string
  runtimes: string[]
  remote_ip: string
  public_ipv4?: string
  first_seen_at: string
  last_seen_at: string
  online: boolean
  capacity?: { vcpu: number; ram_mb: number; disk_gb: number }
}

export type HostingRecord = {
  enabled: boolean
  fee_percent: number
  offline_hours: number
  overcommit_limits: Overcommit
  trade_fee_percent: number
  rules: string[]
  nodes: HostedNodeRecord[]
  regions: RegionRecord[]
  balance_minor: number
  currency: string
  install_command: string
  pending_agents: PendingAgentRecord[]
}
export type NodeImageRecord = { id: string; name: string; type?: string; virtualization?: string; description?: string }

export type ClearanceRecord = {
  node_id: string
  node_name: string
  multiplier: number
  reason: string
  currency: string
  refund_minor: number
  penalty_minor: number
  services: { service_id: string; instance_name: string; buyer_name: string; remaining_minor: number; refund_minor: number; penalty_minor: number }[]
}

export type ChatMessageRecord = {
  id: number
  node_id: string
  author_type: 'host' | 'buyer' | 'staff' | 'system'
  author_name: string
  mine: boolean
  body: string
  created_at: string
  author_account_id?: string
}
export type ChatRoomRecord = {
  node_id: string
  node_name: string
  host_name: string
  role: 'host' | 'buyer' | 'staff'
  retired: boolean
  members: number
  last_message?: ChatMessageRecord
}
export type ChatHistoryRecord = { messages: ChatMessageRecord[]; can_post: boolean; role: string }

export type CouponRecord = {
  id: string
  code: string
  description: string
  discount_type: 'percent' | 'amount'
  discount_value: number
  plan_ids: string[]
  max_uses: number
  used_count: number
  expires_at: string | null
  recurring: boolean
  enabled: boolean
  created_at: string
}

export type CouponQuoteRecord = {
  code: string
  description: string
  unit_minor: number
  discount_minor: number
  final_minor: number
  recurring: boolean
}

export type RefundQuoteRecord = {
  service_id: string
  instance_name: string
  available: boolean
  message?: string
  full: boolean
  early_refund: boolean
  paid_minor: number
  refund_minor: number
  currency: string
  traffic_bytes: number | null
  purchased_at: string
}

export type TradeListingRecord = {
  id: string
  service_id?: string
  instance_name?: string
  seller_name: string
  mine: boolean
  status: 'listed' | 'sold' | 'cancelled'
  available: boolean
  cancel_reason?: string
  price_minor: number
  currency: string
  note: string
  plan_name: string
  virtualization: string
  vcpu: number
  ram_mb: number
  disk_gb: number
  traffic_gb: number
  network_down_mbps: number
  port_mapping_count: number
  region_name: string
  host_name?: string
  node_online: boolean
  traffic_bytes: number
  traffic_rx_bytes: number | null
  traffic_tx_bytes: number | null
  fee_minor?: number
  seller_proceeds_minor?: number
  billing_cycle: string
  expires_at: string | null
  renewal_minor: number | null
  service_created_at: string
  created_at: string
  sold_at?: string
}

export type TradeRecord = { listings: TradeListingRecord[]; mine: TradeListingRecord[]; hold_days: number; fee_percent: number; min_remaining_days: number }

export type ReportRecord = {
  id: string
  target_type: 'node' | 'chat_message'
  node_id: string
  node_name: string
  host_name: string
  message_id?: number
  message_body?: string
  message_author?: string
  message_author_account_id?: string
  reason: string
  detail: string
  reporter_name: string
  status: 'open' | 'resolved' | 'dismissed'
  resolution?: string
  created_at: string
  resolved_at?: string
}

export type ChatMuteRecord = { account_id: string; account_name: string; until: string; reason: string }

export const reportReasons: Record<string, string> = {
  resources: '实际资源与宣传不符',
  oversell: '性能严重不足（超出公开的超售倍数）',
  false_info: '位置、线路等信息不实',
  abuse: '辱骂或骚扰',
  spam: '广告或刷屏',
  other: '其他',
}
