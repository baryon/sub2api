import { describe, expect, it } from 'vitest'
import {
  AUTO_TOPUP_DEFAULTS,
  autoTopUpErrorKey,
  autoTopUpFormError,
  autoTopUpStopReasonKey,
  cardBrandName,
  monthlyRemaining,
} from '../otohaAutoTopUp'

const limits = { minAmount: 1, maxAmount: 1000 }
const valid = { threshold: 5, amount: 20, monthlyCap: 100 }

describe('autoTopUpFormError', () => {
  it('accepts the defaults and a cap equal to one top-up', () => {
    expect(AUTO_TOPUP_DEFAULTS).toEqual({ threshold: 5, amount: 20, monthlyCap: 100 })
    expect(autoTopUpFormError(valid, limits)).toBeNull()
    expect(autoTopUpFormError({ ...valid, monthlyCap: 20 }, limits)).toBeNull()
  })

  it('asks for a positive amount in every field', () => {
    expect(autoTopUpFormError({ ...valid, threshold: 0 }, limits)?.key).toBe('otoha.autoTopUp.errors.thresholdInvalid')
    expect(autoTopUpFormError({ ...valid, amount: -3 }, limits)?.key).toBe('otoha.autoTopUp.errors.amountInvalid')
    expect(autoTopUpFormError({ ...valid, monthlyCap: Number.NaN }, limits)?.key).toBe('otoha.autoTopUp.errors.monthlyCapInvalid')
  })

  it('refuses fractions of a cent', () => {
    expect(autoTopUpFormError({ ...valid, amount: 20.001 }, limits)?.key).toBe('otoha.autoTopUp.errors.amountInvalid')
    expect(autoTopUpFormError({ ...valid, amount: 20.25 }, limits)).toBeNull()
  })

  it('keeps one top-up inside what the site accepts', () => {
    expect(autoTopUpFormError({ ...valid, amount: 0.5 }, limits)).toEqual({ key: 'otoha.autoTopUp.errors.amountTooSmall', params: { min: 1 } })
    expect(autoTopUpFormError({ ...valid, amount: 1500, monthlyCap: 5000 }, limits)).toEqual({ key: 'otoha.autoTopUp.errors.amountTooLarge', params: { max: 1000 } })
    expect(autoTopUpFormError({ ...valid, amount: 20 }, { minAmount: 25, maxAmount: 0 })).toEqual({ key: 'otoha.autoTopUp.errors.amountTooSmall', params: { min: 25 } })
  })

  it('keeps the monthly limit at least one top-up and the threshold reasonable', () => {
    expect(autoTopUpFormError({ ...valid, monthlyCap: 19.99 }, limits)).toEqual({ key: 'otoha.autoTopUp.errors.monthlyCapTooSmall', params: { min: 20 } })
    expect(autoTopUpFormError({ ...valid, monthlyCap: 10001 }, limits)).toEqual({ key: 'otoha.autoTopUp.errors.monthlyCapTooLarge', params: { max: 10000 } })
    expect(autoTopUpFormError({ ...valid, threshold: 1001 }, limits)).toEqual({ key: 'otoha.autoTopUp.errors.thresholdTooLarge', params: { max: 1000 } })
  })
})

describe('autoTopUpStopReasonKey', () => {
  it('has plain words for each reason the system stops it, and a general one for the rest', () => {
    expect(autoTopUpStopReasonKey('authentication_required')).toBe('otoha.autoTopUp.stopped.authentication_required')
    expect(autoTopUpStopReasonKey('card_declined')).toBe('otoha.autoTopUp.stopped.card_declined')
    expect(autoTopUpStopReasonKey('payment_error')).toBe('otoha.autoTopUp.stopped.payment_error')
    expect(autoTopUpStopReasonKey('something_new')).toBe('otoha.autoTopUp.stopped.payment_error')
    expect(autoTopUpStopReasonKey('')).toBe('')
  })
})

describe('card and month helpers', () => {
  it('names the card brand', () => {
    expect(cardBrandName('visa')).toBe('Visa')
    expect(cardBrandName('amex')).toBe('American Express')
    expect(cardBrandName('')).toBe('')
    expect(cardBrandName('cartes_bancaires')).toBe('Cartes bancaires')
  })

  it('tells what the monthly limit still allows', () => {
    expect(monthlyRemaining({ monthly_cap_usd: 100, used_this_month_usd: 40 })).toBe(60)
    expect(monthlyRemaining({ monthly_cap_usd: 100, used_this_month_usd: 120 })).toBe(0)
  })
})

describe('autoTopUpErrorKey', () => {
  it('has its own text for the reasons the server gives and a fallback for the rest', () => {
    expect(autoTopUpErrorKey({ reason: 'AUTO_TOPUP_NEEDS_CARD' }, 'fallback')).toBe('otoha.autoTopUp.errors.AUTO_TOPUP_NEEDS_CARD')
    expect(autoTopUpErrorKey({ reason: 'AUTO_TOPUP_INVALID' }, 'fallback')).toBe('otoha.autoTopUp.errors.AUTO_TOPUP_INVALID')
    expect(autoTopUpErrorKey({ reason: 'SOMETHING_ELSE' }, 'fallback')).toBe('fallback')
    expect(autoTopUpErrorKey(null, 'fallback')).toBe('fallback')
  })
})
