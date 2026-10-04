import { describe, expect, it } from 'vitest'
import { formatCharge, usageRange, usageTokens } from '../otohaUsage'

describe('formatCharge', () => {
  it('shows cents for ordinary amounts', () => {
    expect(formatCharge(0)).toBe('$0.00')
    expect(formatCharge(1.5)).toBe('$1.50')
    expect(formatCharge(12.345)).toBe('$12.35')
    expect(formatCharge(0.25)).toBe('$0.25')
  })

  it('keeps small charges visible instead of rounding them to zero', () => {
    expect(formatCharge(0.0123)).toBe('$0.0123')
    expect(formatCharge(0.000123)).toBe('$0.000123')
    expect(formatCharge(0.00000004)).toBe('<$0.000001')
  })

  it('treats missing or broken values as zero', () => {
    expect(formatCharge(undefined)).toBe('$0.00')
    expect(formatCharge(Number.NaN)).toBe('$0.00')
    expect(formatCharge(-1)).toBe('$0.00')
  })
})

describe('usageTokens', () => {
  it('adds input, output and cached tokens', () => {
    expect(
      usageTokens({ input_tokens: 100, output_tokens: 50, cache_creation_tokens: 20, cache_read_tokens: 30 }),
    ).toEqual({ total: 200, input: 100, output: 50, cached: 50 })
  })

  it('treats missing counts as zero', () => {
    expect(usageTokens({ input_tokens: 7, output_tokens: 0, cache_creation_tokens: 0, cache_read_tokens: 0 })).toEqual({
      total: 7,
      input: 7,
      output: 0,
      cached: 0,
    })
    expect(usageTokens({} as never)).toEqual({ total: 0, input: 0, output: 0, cached: 0 })
  })
})

describe('usageRange', () => {
  it('covers the last n days including today, in local dates', () => {
    const now = new Date(2026, 9, 4, 15, 30)
    expect(usageRange(7, now)).toEqual({ start_date: '2026-09-28', end_date: '2026-10-04' })
    expect(usageRange(1, now)).toEqual({ start_date: '2026-10-04', end_date: '2026-10-04' })
    expect(usageRange(30, new Date(2026, 2, 1))).toEqual({ start_date: '2026-01-31', end_date: '2026-03-01' })
  })
})
