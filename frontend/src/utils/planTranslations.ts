/**
 * A plan's name, description and features per language. The server stores them as
 * { en: { name, description, features } } and shows a buyer the text of their language, falling back to the
 * plan's own text for anything left empty. The editor fills the languages listed here; any other language the
 * plan already has is kept as it is.
 */
export interface PlanText {
  name: string
  description: string
  features: string
}

export type PlanTranslations = Record<string, Partial<PlanText>>

export const PLAN_TEXT_LANGUAGES = [
  { code: 'en', label: 'English' },
  { code: 'ja', label: '日本語' },
] as const

export function planTranslationsForm(stored: PlanTranslations | null | undefined): Record<string, PlanText> {
  const form: Record<string, PlanText> = {}
  for (const { code } of PLAN_TEXT_LANGUAGES) {
    const text = stored?.[code] ?? {}
    form[code] = { name: text.name ?? '', description: text.description ?? '', features: text.features ?? '' }
  }
  return form
}

/** What the plan form sends: the filled texts of every language, with empty ones left out. */
export function planTranslationsPayload(
  form: Record<string, PlanText>,
  stored: PlanTranslations | null | undefined,
): PlanTranslations {
  const editable = new Set<string>(PLAN_TEXT_LANGUAGES.map(l => l.code))
  const payload: PlanTranslations = {}
  for (const [code, text] of Object.entries(stored ?? {})) {
    if (!editable.has(code)) payload[code] = text
  }
  for (const { code } of PLAN_TEXT_LANGUAGES) {
    const entry = form[code]
    if (!entry) continue
    const filled: Partial<PlanText> = {}
    const name = entry.name.trim()
    const description = entry.description.trim()
    const features = entry.features.split('\n').map(line => line.trim()).filter(Boolean).join('\n')
    if (name) filled.name = name
    if (description) filled.description = description
    if (features) filled.features = features
    if (Object.keys(filled).length > 0) payload[code] = filled
  }
  return payload
}
