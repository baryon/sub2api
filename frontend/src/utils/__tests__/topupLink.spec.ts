import { describe, expect, it } from 'vitest'
import { parseTopupAmount, recallTopupReturn, rememberTopupReturn, sanitizeReturnUrl } from '../topupLink'

const ORIGIN = 'https://account.otohaai.com'

describe('parseTopupAmount', () => {
  it('accepts plain amounts inside the limits', () => {
    expect(parseTopupAmount('20', 1, 5000)).toBe(20)
    expect(parseTopupAmount('12.5', 1, 5000)).toBe(12.5)
    expect(parseTopupAmount('20', 0, 0)).toBe(20)
  })
  it('ignores amounts outside the limits', () => {
    expect(parseTopupAmount('0.5', 1, 5000)).toBeNull()
    expect(parseTopupAmount('6000', 1, 5000)).toBeNull()
  })
  it('ignores malformed values', () => {
    for (const v of ['', 'abc', '-5', '0', '1e3', '1,5', '5.123', ' ', 'NaN', 'Infinity', '20; drop']) {
      expect(parseTopupAmount(v, 0, 0)).toBeNull()
    }
    expect(parseTopupAmount(undefined, 0, 0)).toBeNull()
    expect(parseTopupAmount(['20'], 0, 0)).toBeNull()
  })
})

describe('sanitizeReturnUrl', () => {
  it('accepts otoha links', () => {
    expect(sanitizeReturnUrl('otoha://topup-done', ORIGIN)).toBe('otoha://topup-done')
    expect(sanitizeReturnUrl('otoha://topup-done?x=1', ORIGIN)).toBe('otoha://topup-done?x=1')
    expect(sanitizeReturnUrl('OTOHA://topup-done', ORIGIN)).toContain('topup-done')
  })
  it('accepts this site only', () => {
    expect(sanitizeReturnUrl('https://account.otohaai.com/orders', ORIGIN)).toBe('https://account.otohaai.com/orders')
    expect(sanitizeReturnUrl('/orders', ORIGIN)).toBe('https://account.otohaai.com/orders')
  })
  it('rejects everything else', () => {
    for (const v of [
      '', 'javascript:alert(1)', 'data:text/html,x', 'https://evil.com/', 'http://account.otohaai.com.evil.com/',
      '//evil.com', 'https://account.otohaai.com@evil.com/', 'otoha:', 'otoha:/x', 'otoha:\\\\x',
      'file:///etc/passwd', 'vbscript:x', 'otoha://a b\nc', '/\\evil.com', 'otoha-evil://x', 'http://account.otohaai.com/',
    ]) {
      expect(sanitizeReturnUrl(v, ORIGIN)).toBe('')
    }
    expect(sanitizeReturnUrl(undefined, ORIGIN)).toBe('')
    expect(sanitizeReturnUrl(['otoha://x'], ORIGIN)).toBe('')
  })
})

describe('remembered return address', () => {
  const makeStorage = (): Storage => {
    const m = new Map<string, string>()
    return {
      getItem: (k: string) => m.get(k) ?? null,
      setItem: (k: string, v: string) => void m.set(k, v),
      removeItem: (k: string) => void m.delete(k),
      clear: () => m.clear(),
      key: () => null,
      length: 0,
    } as Storage
  }
  it('is recalled while fresh and dropped when old', () => {
    const s = makeStorage()
    rememberTopupReturn(s, 'otoha://topup-done', 1000)
    expect(recallTopupReturn(s, ORIGIN, 2000)).toBe('otoha://topup-done')
    expect(recallTopupReturn(s, ORIGIN, 1000 + 25 * 3600 * 1000)).toBe('')
  })
  it('re-validates what was stored', () => {
    const s = makeStorage()
    s.setItem('otoha_topup_return', JSON.stringify({ url: 'https://evil.com', at: 1000 }))
    expect(recallTopupReturn(s, ORIGIN, 1500)).toBe('')
    s.setItem('otoha_topup_return', 'not json')
    expect(recallTopupReturn(s, ORIGIN, 1500)).toBe('')
  })
})
