export interface ServiceTrafficSource {
  kind: string
  link_id: number
  user_id: number
  name: string
  up: number
  down: number
  total: number
  billable_total?: number
}

export interface ServiceTrafficUser {
  user_id: number
  name: string
  up: number
  down: number
  total: number
  direct_total: number
  relay_total: number
  billable_total?: number
}

export interface ServiceTraffic {
  total: number
  billable_total: number
  new_coverage_start: number
  user_coverage_complete: boolean
  observed_user_coverage_complete?: boolean
  attribution_ready?: boolean
  coverage_reasons?: string[]
  unallocated_total?: number
  users?: ServiceTrafficUser[]
  sources: ServiceTrafficSource[]
  outbound_links?: ServiceTrafficSource[]
  quality: { mode: string; status: string; gaps: number; pending_polls: number }
}
