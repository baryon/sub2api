// Top-up link from the Otoha desktop app: /purchase?tab=recharge&amount=<USD>&return=<address>

const RETURN_STORAGE_KEY = 'otoha_topup_return'
const RETURN_MAX_AGE_MS = 24 * 60 * 60 * 1000

/**
 * Preset amount from the link. Returns null (ignore) unless it is a plain positive
 * number with at most 2 decimals inside [min, max]; a limit of 0 means no limit.
 */
export function parseTopupAmount(raw: unknown, min: number, max: number): number | null {
  if (typeof raw !== 'string') return null
  const text = raw.trim()
  if (!/^\d{1,9}(\.\d{1,2})?$/.test(text)) return null
  const value = Number(text)
  if (!Number.isFinite(value) || value <= 0) return null
  if (min > 0 && value < min) return null
  if (max > 0 && value > max) return null
  return value
}

function hasUnsafeCharacter(text: string): boolean {
  for (const ch of text) {
    const code = ch.charCodeAt(0)
    if (code <= 0x1f || code === 0x7f || ch === '\\') return true
  }
  return false
}

/**
 * Return address from the link. Only `otoha://…` and this site are accepted;
 * anything else yields an empty string.
 */
export function sanitizeReturnUrl(raw: unknown, origin: string): string {
  if (typeof raw !== 'string') return ''
  const text = raw.trim()
  if (!text || text.length > 2048 || hasUnsafeCharacter(text)) return ''
  if (/^otoha:\/\/[^\s]/i.test(text)) {
    try {
      const url = new URL(text)
      return url.protocol === 'otoha:' ? url.toString() : ''
    } catch {
      return ''
    }
  }
  // Same site: absolute URL on this origin, or a path on this site.
  if (text.startsWith('//')) return ''
  try {
    const url = new URL(text, origin)
    if (url.origin !== new URL(origin).origin) return ''
    if (url.protocol !== 'https:' && url.protocol !== 'http:') return ''
    return url.toString()
  } catch {
    return ''
  }
}

export function rememberTopupReturn(storage: Storage, url: string, now = Date.now()): void {
  try {
    storage.setItem(RETURN_STORAGE_KEY, JSON.stringify({ url, at: now }))
  } catch {
    // Storage may be unavailable; the query string still carries the address.
  }
}

export function recallTopupReturn(storage: Storage, origin: string, now = Date.now()): string {
  try {
    const parsed = JSON.parse(storage.getItem(RETURN_STORAGE_KEY) || 'null')
    if (!parsed || typeof parsed.at !== 'number' || now - parsed.at > RETURN_MAX_AGE_MS) return ''
    return sanitizeReturnUrl(parsed.url, origin)
  } catch {
    return ''
  }
}
