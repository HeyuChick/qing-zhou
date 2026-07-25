// Shared rendering helpers for a user's plan buckets (planView from the API),
// so the user dashboard and the admin user detail present status/timing the same
// way. `status` comes from the backend (active | queued | exhausted | expired);
// `activate_by` is a queued plan's estimated latest activation time (unix, 0 =
// unknown).

export type PlanTagType = 'success' | 'info' | 'warning' | 'error' | 'default'

/** Status label + Naive-UI tag type for a plan bucket. */
export function planStatusMeta(p: any): { label: string; type: PlanTagType } {
  if (p.status === 'queued') return { label: '排队中', type: 'info' }
  if (p.status === 'expired' || (p.status !== 'queued' && p.expiry_at && p.expiry_at * 1000 < Date.now())) {
    return { label: '已过期', type: 'error' }
  }
  if (p.status === 'exhausted' || (p.traffic_limit > 0 && p.used >= p.traffic_limit)) {
    return { label: '已用尽', type: 'warning' }
  }
  return { label: '使用中', type: 'success' }
}

/** The date line for a plan row: expiry for active plans, estimated activation
 *  for queued ones. fmtDate formats a unix seconds value. */
export function planTimeText(p: any, fmtDate: (t: number) => string): string {
  if (p.status === 'queued') {
    return p.activate_by ? `预计 ${fmtDate(p.activate_by)} 前生效` : '前一份用完后生效'
  }
  return p.expiry_at ? fmtDate(p.expiry_at) : '不过期'
}

/** Sort key so a list reads active first, then queued, then finished. */
export function planSortKey(p: any): number {
  const m = planStatusMeta(p)
  if (m.label === '使用中') return 0
  if (m.label === '排队中') return 1
  return 2
}
