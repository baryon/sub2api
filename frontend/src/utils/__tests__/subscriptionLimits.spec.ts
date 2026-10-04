import { describe, expect, it } from 'vitest'
import { subscriptionLimits, planAllowanceInput, planAllowancePayload } from '../subscriptionLimits'

describe('subscriptionLimits', () => {
  it('uses the limits the server says apply to the subscription (its plan allowance)', () => {
    const limits = subscriptionLimits({
      daily_limit_usd: 5,
      weekly_limit_usd: null,
      monthly_limit_usd: 40,
      group: { daily_limit_usd: 5, weekly_limit_usd: null, monthly_limit_usd: 100 },
    })
    expect(limits).toEqual({ daily: 5, weekly: null, monthly: 40 })
  })

  it("falls back to the group's limits for a response without them", () => {
    const limits = subscriptionLimits({ group: { daily_limit_usd: null, weekly_limit_usd: 30, monthly_limit_usd: 100 } })
    expect(limits).toEqual({ daily: null, weekly: 30, monthly: 100 })
  })

  it('treats zero or missing as no limit', () => {
    expect(subscriptionLimits({ monthly_limit_usd: 0 })).toEqual({ daily: null, weekly: null, monthly: null })
    expect(subscriptionLimits({})).toEqual({ daily: null, weekly: null, monthly: null })
  })
})

describe('plan allowance form values', () => {
  it('shows an empty field for a plan that follows the group', () => {
    expect(planAllowanceInput(null)).toBe('')
    expect(planAllowanceInput(undefined)).toBe('')
    expect(planAllowanceInput(0)).toBe('')
    expect(planAllowanceInput(40)).toBe(40)
  })

  it('sends 0 for an empty field (follow the group) and the amount otherwise', () => {
    expect(planAllowancePayload('')).toBe(0)
    expect(planAllowancePayload(null)).toBe(0)
    expect(planAllowancePayload(Number.NaN)).toBe(0)
    expect(planAllowancePayload(40)).toBe(40)
    expect(planAllowancePayload('12.5')).toBe(12.5)
  })

  it('passes a negative amount through so the server can refuse it', () => {
    expect(planAllowancePayload(-1)).toBe(-1)
  })
})
