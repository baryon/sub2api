/**
 * The daily, weekly and monthly limits (USD) that apply to a subscription. The server sends them on the
 * subscription — the allowance of the plan it was bought with, or the group's limit — and older responses only
 * on the group. null means no limit for that window.
 */
export interface SubscriptionLimitValues {
  daily: number | null
  weekly: number | null
  monthly: number | null
}

interface LimitFields {
  daily_limit_usd?: number | null
  weekly_limit_usd?: number | null
  monthly_limit_usd?: number | null
}

function positive(value: number | null | undefined): number | null {
  return typeof value === 'number' && value > 0 ? value : null
}

export function subscriptionLimits(sub: LimitFields & { group?: LimitFields | null }): SubscriptionLimitValues {
  const pick = (own: number | null | undefined, group: number | null | undefined) =>
    positive(own !== undefined ? own : group)
  return {
    daily: pick(sub.daily_limit_usd, sub.group?.daily_limit_usd),
    weekly: pick(sub.weekly_limit_usd, sub.group?.weekly_limit_usd),
    monthly: pick(sub.monthly_limit_usd, sub.group?.monthly_limit_usd),
  }
}

/** A plan's own allowance as the plan form shows it: empty when the plan follows the group's limit. */
export function planAllowanceInput(value: number | null | undefined): number | '' {
  return typeof value === 'number' && value > 0 ? value : ''
}

/** What the plan form sends for an allowance field: 0 (follow the group's limit) when empty. */
export function planAllowancePayload(value: number | string | null | undefined): number {
  if (value === '' || value == null) return 0
  const n = typeof value === 'number' ? value : Number(value)
  return Number.isFinite(n) ? n : 0
}
