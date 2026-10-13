import { describe, expect, it } from 'vitest'
import {
  catalogAbilities,
  catalogGroupOptions,
  formatCatalogTokens,
  initialCatalogGroupId,
  priceTierLines,
  priceTierRange,
  priceTierRows,
  priceTiersFromRows,
  thinkingForm,
  thinkingFromForm,
  levelRows,
  levelsFromRows
} from '../otohaCatalogPage'

const groups = [
  { id: 3, name: 'Coding', platform: 'anthropic' },
  { id: 9, name: 'Otoha', platform: 'composite' },
  { id: 5, name: 'Archive', platform: 'openai' }
]

describe('initialCatalogGroupId', () => {
  it('opens on the group named in the address when it exists', () => {
    expect(initialCatalogGroupId(groups, 9, '3')).toBe(3)
  })

  it('opens on the Otoha group the server is configured with', () => {
    expect(initialCatalogGroupId(groups, 9, undefined)).toBe(9)
    expect(initialCatalogGroupId(groups, 9, 'abc')).toBe(9)
    expect(initialCatalogGroupId(groups, 9, '404')).toBe(9)
  })

  it('opens on no group when the server has none or it is gone', () => {
    expect(initialCatalogGroupId(groups, 0, undefined)).toBeNull()
    expect(initialCatalogGroupId(groups, 77, undefined)).toBeNull()
    expect(initialCatalogGroupId([], 9, '9')).toBeNull()
  })
})

describe('catalogGroupOptions', () => {
  it('puts the Otoha group first and marks it', () => {
    const options = catalogGroupOptions(groups, 9)
    expect(options.map((o) => o.id)).toEqual([9, 3, 5])
    expect(options.map((o) => o.isOtoha)).toEqual([true, false, false])
  })

  it('keeps the order when no Otoha group is set', () => {
    expect(catalogGroupOptions(groups, 0).map((o) => o.id)).toEqual([3, 9, 5])
  })
})

describe('catalogAbilities', () => {
  it('lists what the model takes and can do', () => {
    expect(
      catalogAbilities({ inputs: ['text', 'image'], tools: true, context: 400_000, reasoning: ['low', 'medium', 'high'] })
    ).toEqual([
      { kind: 'image' },
      { kind: 'tools' },
      { kind: 'context', value: '400K' },
      { kind: 'reasoning', value: 'low–high' }
    ])
  })

  it('leaves out what is not known', () => {
    expect(catalogAbilities({ inputs: ['text'], tools: false, context: 0, reasoning: [] })).toEqual([])
    expect(catalogAbilities({ inputs: null, tools: false, context: 0, reasoning: ['high'] })).toEqual([
      { kind: 'reasoning', value: 'high' }
    ])
  })
})

describe('formatCatalogTokens', () => {
  it('shortens token counts', () => {
    expect(formatCatalogTokens(1_050_000)).toBe('1.1M')
    expect(formatCatalogTokens(1_000_000)).toBe('1M')
    expect(formatCatalogTokens(128_000)).toBe('128K')
    expect(formatCatalogTokens(512)).toBe('512')
  })
})

describe('price tiers', () => {
  const base = { above: 0, inclusive: false, input: 1, output: 5, cached: '' }

  it('reads tiers into rows and back', () => {
    const tiers = [
      { aboveInputTokens: 0, inclusive: false, input: 1, output: 5 },
      { aboveInputTokens: 100000, inclusive: false, input: 5, output: 25, cachedInput: 0.5 }
    ]
    expect(priceTiersFromRows(priceTierRows(tiers))).toEqual({ tiers })
  })

  it('accepts typed text and leaves the cached price out when empty', () => {
    const result = priceTiersFromRows([base, { above: '200000', inclusive: true, input: '4', output: '12', cached: '' }])
    expect(result).toEqual({
      tiers: [
        { aboveInputTokens: 0, inclusive: false, input: 1, output: 5 },
        { aboveInputTokens: 200000, inclusive: true, input: 4, output: 12 }
      ]
    })
  })

  it('refuses tiers the server would refuse', () => {
    expect(priceTiersFromRows([])).toEqual({ error: 'empty' })
    expect(priceTiersFromRows([{ ...base, above: 1000 }])).toEqual({ error: 'base' })
    expect(priceTiersFromRows([{ ...base, inclusive: true }])).toEqual({ error: 'base' })
    expect(priceTiersFromRows([base, { ...base, above: 5000 }, { ...base, above: 5000 }])).toEqual({ error: 'order' })
    expect(priceTiersFromRows([{ ...base, input: -1 }])).toEqual({ error: 'number' })
    expect(priceTiersFromRows([{ ...base, output: '' }])).toEqual({ error: 'number' })
    expect(priceTiersFromRows([base, { ...base, above: 1.5 }])).toEqual({ error: 'number' })
  })

  it('names each tier by its range', () => {
    const tiers = [
      { aboveInputTokens: 0, inclusive: false, input: 1, output: 5 },
      { aboveInputTokens: 100000, inclusive: false, input: 5, output: 25 }
    ]
    expect(priceTierRange(tiers, 0)).toEqual({ kind: 'upTo', tokens: '100K' })
    expect(priceTierRange(tiers, 1)).toEqual({ kind: 'above', tokens: '100K' })
    const xai = [tiers[0], { aboveInputTokens: 200000, inclusive: true, input: 4, output: 12 }]
    expect(priceTierRange(xai, 0)).toEqual({ kind: 'below', tokens: '200K' })
    expect(priceTierRange(xai, 1)).toEqual({ kind: 'from', tokens: '200K' })
    expect(priceTierRange([tiers[0]], 0)).toEqual({ kind: 'all', tokens: '' })
  })

  it('lists every tier with its range and prices, and nothing for a single price', () => {
    const haiku = [
      { aboveInputTokens: 0, inclusive: false, input: 0.15, output: 0.75, cachedInput: 0.015 },
      { aboveInputTokens: 100000, inclusive: false, input: 0.75, output: 3.75 }
    ]
    expect(priceTierLines(haiku)).toEqual([
      { range: { kind: 'upTo', tokens: '100K' }, input: 0.15, output: 0.75, cachedInput: 0.015 },
      { range: { kind: 'above', tokens: '100K' }, input: 0.75, output: 3.75 }
    ])
    expect(priceTierLines([haiku[0]])).toEqual([])
    expect(priceTierLines(null)).toEqual([])
    expect(priceTierLines(undefined)).toEqual([])
  })
})

describe('thinking settings', () => {
  it('shows a model nobody described as unknown with nothing chosen', () => {
    const form = thinkingForm(null)
    expect(form.canBeOff).toBe('unknown')
    expect(form.settings.map((s) => [s.value, s.on])).toEqual([
      ['off', false],
      ['between_tools', false],
      ['adaptive', false],
      ['budget', false]
    ])
    expect(thinkingFromForm(form)).toEqual({ thinking: null })
  })

  it('reads settings into the form and back', () => {
    const thinking = {
      canBeOff: false,
      settings: [{ value: 'between_tools', maxEffort: 'high' }, { value: 'adaptive' }]
    }
    const form = thinkingForm(thinking)
    expect(form.canBeOff).toBe('no')
    expect(form.settings.find((s) => s.value === 'between_tools')).toMatchObject({ on: true, maxEffort: 'high' })
    expect(thinkingFromForm(form)).toEqual({ thinking })
  })

  it('keeps an unknown can-be-off as null when settings are known', () => {
    const form = thinkingForm({ canBeOff: null, settings: [{ value: 'budget' }] })
    expect(thinkingFromForm(form)).toEqual({ thinking: { canBeOff: null, settings: [{ value: 'budget' }] } })
  })

  it('refuses what the server refuses', () => {
    const form = thinkingForm({ canBeOff: false, settings: [{ value: 'off' }] })
    expect(thinkingFromForm(form)).toEqual({ error: 'offConflict' })
    const bad = thinkingForm({ canBeOff: true, settings: [{ value: 'off', maxEffort: 'Very High!' }] })
    expect(thinkingFromForm(bad)).toEqual({ error: 'effort' })
  })
})

describe('thinking levels', () => {
  const level = { setting: 'adaptive', effort: 'high', complexity: 'complex', extraOutput: 'high' }

  it('reads levels into rows and back, leaving an empty effort out', () => {
    const levels = [level, { setting: 'off', complexity: 'simple', extraOutput: 'low' }]
    expect(levelsFromRows(levelRows(levels))).toEqual({ levels })
  })

  it('refuses a level missing its parts or with a bad effort', () => {
    expect(levelsFromRows([{ setting: '', effort: '', complexity: 'simple', extraOutput: 'low' }])).toEqual({ error: 'incomplete' })
    expect(levelsFromRows([{ ...level, extraOutput: '' }])).toEqual({ error: 'incomplete' })
    expect(levelsFromRows([{ ...level, effort: 'A B' }])).toEqual({ error: 'effort' })
  })

  it('has no levels for a model nobody described', () => {
    expect(levelsFromRows(levelRows(null))).toEqual({ levels: [] })
  })
})
