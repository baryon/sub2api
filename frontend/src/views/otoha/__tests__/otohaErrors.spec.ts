import { describe, expect, it } from 'vitest'
import { otohaErrorMessage } from '../otohaErrors'

const t = ((key: string) => key) as never

describe('otohaErrorMessage', () => {
  it('maps known reasons and falls back otherwise', () => {
    expect(otohaErrorMessage({ reason: 'OTOHA_NO_ACCESS' }, t, 'fallback')).toBe('otoha.errors.OTOHA_NO_ACCESS')
    expect(otohaErrorMessage({ reason: 'SOMETHING_ELSE' }, t, 'fallback')).toBe('fallback')
    expect(otohaErrorMessage({ status: 403 }, t, 'fallback')).toBe('otoha.errors.OTOHA_NO_ACCESS')
  })

  it('does not send portal users to the keys page they cannot see', () => {
    expect(otohaErrorMessage({ reason: 'OTOHA_KEY_DISABLED' }, t, 'fallback')).toBe('otoha.errors.OTOHA_KEY_DISABLED')
    expect(otohaErrorMessage({ reason: 'OTOHA_KEY_DISABLED' }, t, 'fallback', { portal: true })).toBe(
      'otoha.errors.OTOHA_KEY_DISABLED_PORTAL',
    )
  })
})
