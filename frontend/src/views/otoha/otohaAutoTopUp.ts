import type { OtohaAutoTopUp } from '@/api/otoha'

const MAX_THRESHOLD = 1000
const MAX_MONTHLY_CAP = 10000

export interface AutoTopUpForm {
  threshold: number
  amount: number
  monthlyCap: number
}

/** What a user starts with; the same as the server's defaults. */
export const AUTO_TOPUP_DEFAULTS: Readonly<AutoTopUpForm> = { threshold: 5, amount: 20, monthlyCap: 100 }

export interface AutoTopUpLimits {
  /** The smallest top-up the site accepts; 0 when there is no minimum. */
  minAmount: number
  /** The largest top-up the site accepts; 0 when there is no maximum. */
  maxAmount: number
}

export interface FormError {
  key: string
  params?: Record<string, number>
}

function isMoney(value: number): boolean {
  return Number.isFinite(value) && value > 0 && Math.abs(value * 100 - Math.round(value * 100)) < 1e-6
}

/**
 * The first thing wrong with the chosen amounts, following the server's rules, or null. The server checks
 * again; this only saves a round trip.
 */
export function autoTopUpFormError(form: AutoTopUpForm, limits: AutoTopUpLimits): FormError | null {
  if (!isMoney(form.threshold)) return { key: 'otoha.autoTopUp.errors.thresholdInvalid' }
  if (!isMoney(form.amount)) return { key: 'otoha.autoTopUp.errors.amountInvalid' }
  if (!isMoney(form.monthlyCap)) return { key: 'otoha.autoTopUp.errors.monthlyCapInvalid' }
  if (form.threshold > MAX_THRESHOLD) return { key: 'otoha.autoTopUp.errors.thresholdTooLarge', params: { max: MAX_THRESHOLD } }
  const min = Math.max(1, limits.minAmount || 0)
  const max = limits.maxAmount > 0 ? Math.min(limits.maxAmount, MAX_MONTHLY_CAP) : MAX_MONTHLY_CAP
  if (form.amount < min) return { key: 'otoha.autoTopUp.errors.amountTooSmall', params: { min } }
  if (form.amount > max) return { key: 'otoha.autoTopUp.errors.amountTooLarge', params: { max } }
  if (form.monthlyCap < form.amount) return { key: 'otoha.autoTopUp.errors.monthlyCapTooSmall', params: { min: form.amount } }
  if (form.monthlyCap > MAX_MONTHLY_CAP) return { key: 'otoha.autoTopUp.errors.monthlyCapTooLarge', params: { max: MAX_MONTHLY_CAP } }
  return null
}

/** The text key for why the system turned automatic top-up off; empty when it did not. */
export function autoTopUpStopReasonKey(reason: string): string {
  if (!reason) return ''
  if (reason === 'authentication_required' || reason === 'card_declined') return `otoha.autoTopUp.stopped.${reason}`
  return 'otoha.autoTopUp.stopped.payment_error'
}

const BRAND_NAMES: Record<string, string> = {
  visa: 'Visa',
  mastercard: 'Mastercard',
  amex: 'American Express',
  discover: 'Discover',
  jcb: 'JCB',
  diners: 'Diners Club',
  unionpay: 'UnionPay',
}

/** A card brand as people write it. */
export function cardBrandName(brand: string): string {
  if (!brand) return ''
  const known = BRAND_NAMES[brand.toLowerCase()]
  if (known) return known
  const spaced = brand.replace(/_/g, ' ')
  return spaced.charAt(0).toUpperCase() + spaced.slice(1)
}

/** What this month's limit still allows. */
export function monthlyRemaining(state: Pick<OtohaAutoTopUp, 'monthly_cap_usd' | 'used_this_month_usd'>): number {
  return Math.max(0, state.monthly_cap_usd - state.used_this_month_usd)
}

const KNOWN_ERRORS = ['AUTO_TOPUP_INVALID', 'AUTO_TOPUP_NEEDS_CARD', 'AUTO_TOPUP_UNAVAILABLE', 'AUTO_TOPUP_CARD_NOT_SAVED'] as const

/** The text key for an error from the automatic top-up endpoints; the given fallback for anything else. */
export function autoTopUpErrorKey(err: unknown, fallbackKey: string): string {
  const reason = (err as { reason?: unknown } | null)?.reason
  if (typeof reason === 'string' && (KNOWN_ERRORS as readonly string[]).includes(reason)) {
    return `otoha.autoTopUp.errors.${reason}`
  }
  return fallbackKey
}
