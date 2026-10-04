import type { ComposerTranslation } from 'vue-i18n'

const KNOWN_REASONS = ['OTOHA_NO_ACCESS', 'OTOHA_KEY_DISABLED', 'OTOHA_NOT_CONFIGURED'] as const

/**
 * The user-facing message for an Otoha endpoint error: the known reasons have their own text, anything else
 * the given fallback. A configuration download that fails comes back as a blob without a reason; its 403 is
 * the "no plan or balance" case. In the Otoha portal users cannot open the keys page, so a disabled key gets
 * text that does not send them there.
 */
export function otohaErrorMessage(
  err: unknown,
  t: ComposerTranslation,
  fallbackKey: string,
  options: { portal?: boolean } = {},
): string {
  const e = (err ?? {}) as { reason?: unknown; status?: unknown }
  const reason = typeof e.reason === 'string' ? e.reason : ''
  if (reason === 'OTOHA_KEY_DISABLED' && options.portal) {
    return t('otoha.errors.OTOHA_KEY_DISABLED_PORTAL')
  }
  if ((KNOWN_REASONS as readonly string[]).includes(reason)) {
    return t(`otoha.errors.${reason}`)
  }
  if (e.status === 403) return t('otoha.errors.OTOHA_NO_ACCESS')
  if (e.status === 503) return t('otoha.errors.OTOHA_NOT_CONFIGURED')
  return t(fallbackKey)
}
