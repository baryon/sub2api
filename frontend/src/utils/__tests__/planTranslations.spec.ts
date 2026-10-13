import { describe, expect, it } from 'vitest'
import {
  PLAN_TEXT_LANGUAGES,
  planTranslationsForm,
  planTranslationsPayload,
} from '../planTranslations'

describe('plan translations form', () => {
  it('offers a blank text for every language the editor fills, and fills what the plan has', () => {
    const form = planTranslationsForm({ en: { name: 'Standard', features: 'A\nB' } })
    expect(Object.keys(form)).toEqual(PLAN_TEXT_LANGUAGES.map(l => l.code))
    expect(form.en).toEqual({ name: 'Standard', description: '', features: 'A\nB' })
    expect(form.ja).toEqual({ name: '', description: '', features: '' })
    expect(planTranslationsForm(undefined).en.name).toBe('')
    expect(planTranslationsForm(null).ja.features).toBe('')
  })

  it('sends only what was filled, trimmed, and drops languages left empty', () => {
    const form = planTranslationsForm({})
    form.en.name = '  Standard '
    form.en.features = ' A \n\n B '
    expect(planTranslationsPayload(form, {})).toEqual({ en: { name: 'Standard', features: 'A\nB' } })
    expect(planTranslationsPayload(planTranslationsForm({}), {})).toEqual({})
  })

  it('keeps the plan\'s languages the editor does not list', () => {
    const existing = { de: { name: 'Standard' }, en: { name: 'Old' } }
    const form = planTranslationsForm(existing)
    form.en.name = 'New'
    expect(planTranslationsPayload(form, existing)).toEqual({ de: { name: 'Standard' }, en: { name: 'New' } })
  })

  it('removes a language whose texts were all cleared', () => {
    const existing = { en: { name: 'Standard' } }
    const form = planTranslationsForm(existing)
    form.en.name = ''
    expect(planTranslationsPayload(form, existing)).toEqual({})
  })
})
