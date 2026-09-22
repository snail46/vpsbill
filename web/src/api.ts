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
}

export type NodeRecord = {
  id: string
  region_code: string
  region_name: string
  name: string
	provider_type: string
  base_url: string
  status: string
  virtualization_types: string[]
  capacity: Record<string, unknown>
  capacity_vcpu: number
  capacity_ram_mb: number
  capacity_disk_gb: number
  last_seen_at: string | null
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
}

export type PlanRecord = {
  id: string
  code: string
  name: string
  virtualization: 'lxc' | 'kvm'
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
}

export type AvailableTemplateRecord = {
  id: string
  name: string
  virtualization: 'lxc' | 'kvm'
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
  kind: 'initial' | 'renewal'
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
  primary_ipv4?: string
  primary_ipv6?: string
  next_due_at?: string
  last_reconciled_at?: string
  last_reconcile_error?: string
}

export type PortMappingRecord = { container_port: number; host_port: number; host_ip?: string; protocol: string; description: string }
export type ServiceRuntimeRecord = {
  container: {
    id: number; name: string; virtualization: string; status: string; template: string; ip: string; ipv6: string
    vcpu: number; ram_mb: number; disk_gb: number; ssh_port: number; port_mapping_limit: number
    port_mappings: PortMappingRecord[]; monthly_traffic_gb: number; network_down_mbps: number; network_up_mbps: number
  }
  usage?: Record<string, number>
  history?: Array<Record<string, number | string>>
  traffic?: Record<string, number | string>
  templates: AvailableTemplateRecord[]
  errors: Record<string, string>
}
export type ServiceCredentialRecord = { username: string; password: string; stored: boolean }
export type ConsoleTicketRecord = { ticket: string; websocket_path: string }

export type CustomerInvoiceRecord = {
  id: string
  number: string
  status: string
  kind: 'initial' | 'renewal'
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
  message_count: number
  last_reply_at: string
  created_at: string
}

export type TicketMessageRecord = { id: string; author_type: string; author_name: string; body: string; internal: boolean; created_at: string }
export type TicketDetailRecord = { ticket: TicketRecord; messages: TicketMessageRecord[] }
export type AuditLogRecord = { id: number; actor_type: string; actor_id?: string; action: string; target_type: string; target_id?: string; ip?: string; user_agent?: string; metadata: Record<string, unknown>; created_at: string }

type Envelope<T> = { data: T; message?: string; error?: string }

function csrfToken() {
  const row = document.cookie
    .split('; ')
    .find((item) => item.startsWith('cb_csrf='))
  return row ? decodeURIComponent(row.split('=').slice(1).join('=')) : ''
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  headers.set('Accept', 'application/json')
  if (init.body) headers.set('Content-Type', 'application/json')
  if (init.method && !['GET', 'HEAD'].includes(init.method.toUpperCase())) {
    headers.set('X-CSRF-Token', csrfToken())
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
