import { describe, expect, it } from 'vitest'
import {
  formatPerMillion,
  orderOutcome,
  otohaOpenUrl,
  otohaPlans,
  planAction,
  periodUsage,
  topUpAmountError,
} from '../otohaRules'
import { sanitizeRedirectPath } from '@/utils/redirect'
import type { SubscriptionPlan } from '@/types/payment'

function plan(partial: Partial<SubscriptionPlan>): SubscriptionPlan {
  return {
    id: 1,
    group_id: 7,
    name: 'Plus',
    description: '',
    price: 20,
    validity_days: 30,
    validity_unit: 'day',
    features: [],
    for_sale: true,
    sort_order: 0,
    ...partial,
  } as SubscriptionPlan
}

describe('otohaPlans', () => {
  it('keeps the Otoha group plans on sale, by sort order then price', () => {
    const plans = [
      plan({ id: 1, name: 'Max', price: 150, sort_order: 3 }),
      plan({ id: 2, name: 'Other', group_id: 9 }),
      plan({ id: 3, name: 'Pro', price: 50, sort_order: 2 }),
      plan({ id: 4, name: 'Hidden', for_sale: false }),
      plan({ id: 5, name: 'Plus', price: 20, sort_order: 2 }),
    ]
    expect(otohaPlans(plans, 7).map((p) => p.name)).toEqual(['Plus', 'Pro', 'Max'])
  })

  it('keeps checkout plans, which carry neither for_sale nor sort_order, in the server order', () => {
    const fromCheckout = [
      { ...plan({ id: 1, name: 'Plus', price: 20 }), for_sale: undefined, sort_order: undefined },
      { ...plan({ id: 2, name: 'Max', price: 150 }), for_sale: undefined, sort_order: undefined },
      { ...plan({ id: 3, name: 'Pro', price: 50 }), for_sale: undefined, sort_order: undefined },
    ] as unknown as SubscriptionPlan[]
    expect(otohaPlans(fromCheckout, 7).map((p) => p.name)).toEqual(['Plus', 'Max', 'Pro'])
  })

  it('shows nothing without a group', () => {
    expect(otohaPlans([plan({})], 0)).toEqual([])
    expect(otohaPlans(undefined, 7)).toEqual([])
  })
})

describe('periodUsage', () => {
  it('works out what is left of the period', () => {
    expect(periodUsage({ monthly_limit_usd: 50, monthly_used_usd: 12.5 })).toEqual({
      used: 12.5, limit: 50, remaining: 37.5, percent: 25,
    })
  })

  it('clamps when the plan is used up', () => {
    expect(periodUsage({ monthly_limit_usd: 50, monthly_used_usd: 61 })).toEqual({
      used: 61, limit: 50, remaining: 0, percent: 100,
    })
  })

  it('has no limit or percentage for an unlimited plan', () => {
    expect(periodUsage({ monthly_limit_usd: null, monthly_used_usd: 3 })).toEqual({
      used: 3, limit: null, remaining: null, percent: null,
    })
  })
})

describe('formatPerMillion', () => {
  it('shows dollars with two decimals, more for small prices', () => {
    expect(formatPerMillion(2)).toBe('$2.00')
    expect(formatPerMillion(12.5)).toBe('$12.50')
    expect(formatPerMillion(0.5)).toBe('$0.50')
    expect(formatPerMillion(0.075)).toBe('$0.075')
    expect(formatPerMillion(0.0375)).toBe('$0.0375')
    expect(formatPerMillion(0)).toBe('$0.00')
  })

  it('leaves out a missing price', () => {
    expect(formatPerMillion(undefined)).toBe('')
    expect(formatPerMillion(Number.NaN)).toBe('')
  })
})

describe('orderOutcome', () => {
  it('maps order states to what the result page says', () => {
    expect(orderOutcome('COMPLETED')).toBe('done')
    expect(orderOutcome('PAID')).toBe('processing')
    expect(orderOutcome('RECHARGING')).toBe('processing')
    expect(orderOutcome('PENDING')).toBe('waiting')
    expect(orderOutcome('FAILED')).toBe('failed')
    expect(orderOutcome('CANCELLED')).toBe('cancelled')
    expect(orderOutcome('EXPIRED')).toBe('cancelled')
    expect(orderOutcome('REFUNDED')).toBe('refunded')
    expect(orderOutcome('REFUND_PENDING')).toBe('refunded')
    expect(orderOutcome('something new')).toBe('waiting')
  })
})

describe('otohaOpenUrl', () => {
  it('builds the app link from a code', () => {
    expect(otohaOpenUrl('ABCDE-FGHJK-MNPQR-STVWX')).toBe('otoha://claim?code=ABCDE-FGHJK-MNPQR-STVWX')
    expect(otohaOpenUrl('a b&c')).toBe('otoha://claim?code=a%20b%26c')
  })
})

describe('topUpAmountError', () => {
  it('accepts amounts within the limits', () => {
    expect(topUpAmountError(50, 1, 500)).toBe('')
    expect(topUpAmountError(50, 0, 0)).toBe('')
  })

  it('names the problem', () => {
    expect(topUpAmountError(Number.NaN, 1, 500)).toBe('invalid')
    expect(topUpAmountError(0, 0, 0)).toBe('invalid')
    expect(topUpAmountError(0.5, 1, 500)).toBe('tooSmall')
    expect(topUpAmountError(501, 1, 500)).toBe('tooLarge')
  })
})

describe('sanitizeRedirectPath', () => {
  it('keeps paths on this site', () => {
    expect(sanitizeRedirectPath('/otoha/buy?from=app')).toBe('/otoha/buy?from=app')
  })

  it('drops anything that could leave the site', () => {
    for (const bad of ['https://evil.example', '//evil.example', '/\\evil.example', 'otoha/buy', '/a\nb', '', null, undefined, ['/x']]) {
      expect(sanitizeRedirectPath(bad as never)).toBe('')
    }
  })
})

describe('planAction', () => {
  const plus = plan({ id: 1, name: 'Plus', price: 20 })
  const pro = plan({ id: 2, name: 'Pro', price: 50 })
  const max = plan({ id: 3, name: 'Max', price: 150 })
  const plans = [plus, pro, max]
  const current = (planId: number | null, name = 'Pro') => ({
    plan_id: planId,
    name,
    expires_at: '2026-11-01T00:00:00Z',
    monthly_limit_usd: 120,
    monthly_used_usd: 0,
    period_resets_at: null,
  })

  it('offers a purchase without a current plan', () => {
    expect(planAction(pro, null, plans)).toBe('buy')
  })

  it('renews the current plan', () => {
    expect(planAction(pro, current(2), plans)).toBe('renew')
  })

  it('upgrades to a dearer plan at once', () => {
    expect(planAction(max, current(2), plans)).toBe('upgrade')
  })

  it('offers a cheaper plan only after the current one ends', () => {
    expect(planAction(plus, current(2), plans)).toBe('later')
  })

  it('offers another plan at the same price only after the current one ends', () => {
    const proPlus = plan({ id: 4, name: 'Pro Plus', price: 50 })
    expect(planAction(proPlus, current(2), [...plans, proPlus])).toBe('later')
  })

  it('matches by name for a subscription from before plans were recorded', () => {
    expect(planAction(pro, current(null, 'Pro'), plans)).toBe('renew')
    expect(planAction(max, current(null, 'Pro'), plans)).toBe('buy')
  })

  it('does not guess when the current plan is no longer on sale', () => {
    expect(planAction(plus, current(99, 'Old'), plans)).toBe('buy')
  })
})
