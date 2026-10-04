import type { SubscriptionPlan } from '@/types/payment'
import type { OtohaAccountPlan } from '@/api/otoha'

/**
 * The Otoha group's plans on sale, in the admin's order, cheapest first among equals. Plans from the checkout
 * endpoint carry neither for_sale (all are on sale) nor sort_order (already in order); they keep that order.
 */
export function otohaPlans(plans: SubscriptionPlan[] | undefined, groupId: number): SubscriptionPlan[] {
  if (!plans || groupId <= 0) return []
  return plans
    .filter((p) => p.group_id === groupId && p.for_sale !== false)
    .sort((a, b) => {
      if (typeof a.sort_order !== 'number' || typeof b.sort_order !== 'number') return 0
      return (a.sort_order - b.sort_order) || (a.price - b.price)
    })
}

export type PlanAction = 'buy' | 'renew' | 'upgrade' | 'later'

/**
 * What buying a plan does for the user's current Otoha plan, following the server's rules: renew the same plan
 * (adds a period); upgrade to a dearer plan that has its own allowance (starts now; the old plan's unused
 * allowance goes to the balance); anything else while the current plan runs — a cheaper plan, another at the
 * same price, one without its own allowance or in another currency — only once the current plan ends. A current
 * plan without its own allowance makes every plan a plain purchase; when the current plan is not on sale, the
 * server decides.
 */
export function planAction(
  plan: SubscriptionPlan,
  current: Pick<OtohaAccountPlan, 'plan_id' | 'name' | 'has_own_allowance'> | null | undefined,
  plans: SubscriptionPlan[],
): PlanAction {
  if (!current) return 'buy'
  if (current.plan_id == null) {
    return current.name === plan.name ? 'renew' : 'buy'
  }
  if (current.plan_id === plan.id) return 'renew'
  if (!current.has_own_allowance) return 'buy'
  const currentPlan = plans.find((p) => p.id === current.plan_id)
  if (!currentPlan) return 'buy'
  const sameCurrency = (plan.currency || '').toUpperCase() === (currentPlan.currency || '').toUpperCase()
  if (!plan.has_own_allowance || !sameCurrency) return 'later'
  return plan.price > currentPlan.price ? 'upgrade' : 'later'
}

export interface PeriodUsage {
  used: number
  limit: number | null
  remaining: number | null
  percent: number | null
}

/** This period's use of the plan; an unlimited plan has no limit, remainder or percentage. */
export function periodUsage(plan: Pick<OtohaAccountPlan, 'monthly_limit_usd' | 'monthly_used_usd'>): PeriodUsage {
  const used = Math.max(0, plan.monthly_used_usd || 0)
  const limit = plan.monthly_limit_usd
  if (limit == null || !(limit > 0)) {
    return { used, limit: null, remaining: null, percent: null }
  }
  return {
    used,
    limit,
    remaining: Math.max(0, limit - used),
    percent: Math.min(100, Math.round((used / limit) * 1000) / 10),
  }
}

/** A price per million tokens in US dollars: two decimals, up to four for prices under a dollar. */
export function formatPerMillion(value: number | undefined): string {
  if (value == null || !Number.isFinite(value)) return ''
  if (value >= 1 || value === 0) return `$${value.toFixed(2)}`
  const text = value.toFixed(4).replace(/0+$/, '')
  const [, decimals = ''] = text.split('.')
  return decimals.length < 2 ? `$${value.toFixed(2)}` : `$${text}`
}

export type OrderOutcome = 'done' | 'processing' | 'waiting' | 'failed' | 'cancelled' | 'refunded'

/** What the result page tells the user about an order. */
export function orderOutcome(status: string): OrderOutcome {
  switch (status) {
    case 'COMPLETED':
      return 'done'
    case 'PAID':
    case 'RECHARGING':
      return 'processing'
    case 'FAILED':
      return 'failed'
    case 'CANCELLED':
    case 'EXPIRED':
      return 'cancelled'
    default:
      if (status.startsWith('REFUND') || status === 'PARTIALLY_REFUNDED') return 'refunded'
      return 'waiting'
  }
}

/** The link that opens the Otoha app with a claim code. */
export function otohaOpenUrl(code: string): string {
  return `otoha://claim?code=${encodeURIComponent(code)}`
}

export type TopUpAmountError = '' | 'invalid' | 'tooSmall' | 'tooLarge'

/** Checks a top-up amount against the site's limits (0 means no limit). */
export function topUpAmountError(amount: number, min: number, max: number): TopUpAmountError {
  if (!Number.isFinite(amount) || amount <= 0) return 'invalid'
  if (min > 0 && amount < min) return 'tooSmall'
  if (max > 0 && amount > max) return 'tooLarge'
  return ''
}
