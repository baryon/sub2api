import type { UsageLog } from '@/types'

/**
 * A usage charge in US dollars. Cents from a dollar up; a single request often costs a fraction of a cent, so
 * amounts under a dollar keep up to six decimals instead of rounding to $0.00.
 */
export function formatCharge(amount: number | undefined): string {
  const value = amount != null && Number.isFinite(amount) && amount > 0 ? amount : 0
  if (value === 0 || value >= 1) return `$${value.toFixed(2)}`
  if (value < 0.000001) return '<$0.000001'
  const text = value.toFixed(6).replace(/0+$/, '')
  const [, decimals = ''] = text.split('.')
  return decimals.length < 2 ? `$${value.toFixed(2)}` : `$${text}`
}

export interface UsageTokens {
  total: number
  input: number
  output: number
  cached: number
}

type TokenCounts = Pick<UsageLog, 'input_tokens' | 'output_tokens' | 'cache_creation_tokens' | 'cache_read_tokens'>

/** A request's tokens: what was sent, what came back and what was served from or written to the cache. */
export function usageTokens(log: TokenCounts): UsageTokens {
  const input = log.input_tokens || 0
  const output = log.output_tokens || 0
  const cached = (log.cache_creation_tokens || 0) + (log.cache_read_tokens || 0)
  return { total: input + output + cached, input, output, cached }
}

function localDate(date: Date): string {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}

/** The last `days` days including today, as the usage API's local start_date and end_date. */
export function usageRange(days: number, now: Date = new Date()): { start_date: string; end_date: string } {
  const start = new Date(now.getFullYear(), now.getMonth(), now.getDate() - (days - 1))
  return { start_date: localDate(start), end_date: localDate(now) }
}
