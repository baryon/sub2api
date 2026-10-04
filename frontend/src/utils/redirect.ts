/**
 * Returns the path when it stays on this site, or '' when it is missing or could lead elsewhere.
 * Used to carry a `redirect` target through login and registration.
 */
export function sanitizeRedirectPath(value: unknown): string {
  if (typeof value !== 'string') return ''
  if (!value.startsWith('/') || value.startsWith('//')) return ''
  if (value.includes('\\') || value.includes('://')) return ''
  if (/[\r\n\t]/.test(value)) return ''
  return value
}
