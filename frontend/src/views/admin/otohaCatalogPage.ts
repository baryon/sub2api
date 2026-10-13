/**
 * Rules of the admin's Otoha model catalog page (TASK-63): which group it opens on, how groups are offered, and how
 * a model's abilities are summed up in the table.
 */

export interface CatalogGroup {
  id: number
  name: string
  platform: string
}

export interface CatalogGroupOption extends CatalogGroup {
  /** The group the server serves the Otoha app from. */
  isOtoha: boolean
}

/**
 * The group the page opens on: the one named in the address when it exists, else the server's Otoha group when it
 * exists, else none (the admin chooses).
 */
export function initialCatalogGroupId(
  groups: CatalogGroup[],
  otohaGroupId: number,
  requested: string | null | undefined
): number | null {
  const exists = (id: number) => groups.some((g) => g.id === id)
  const requestedId = Number(requested)
  if (requested && Number.isInteger(requestedId) && requestedId > 0 && exists(requestedId)) return requestedId
  if (otohaGroupId > 0 && exists(otohaGroupId)) return otohaGroupId
  return null
}

/** The groups to choose from, the Otoha group first. */
export function catalogGroupOptions(groups: CatalogGroup[], otohaGroupId: number): CatalogGroupOption[] {
  const options = groups.map((g) => ({ ...g, isOtoha: otohaGroupId > 0 && g.id === otohaGroupId }))
  return [...options.filter((o) => o.isOtoha), ...options.filter((o) => !o.isOtoha)]
}

export interface CatalogAbility {
  kind: 'image' | 'tools' | 'context' | 'reasoning'
  value?: string
}

interface AbilitySource {
  inputs: string[] | null
  tools: boolean
  context: number
  reasoning: string[] | null
}

/** What the model takes and can do, leaving out what is not known. */
export function catalogAbilities(entry: AbilitySource): CatalogAbility[] {
  const abilities: CatalogAbility[] = []
  if (entry.inputs?.includes('image')) abilities.push({ kind: 'image' })
  if (entry.tools) abilities.push({ kind: 'tools' })
  if (entry.context > 0) abilities.push({ kind: 'context', value: formatCatalogTokens(entry.context) })
  const reasoning = entry.reasoning ?? []
  if (reasoning.length === 1) abilities.push({ kind: 'reasoning', value: reasoning[0] })
  if (reasoning.length > 1) {
    abilities.push({ kind: 'reasoning', value: `${reasoning[0]}–${reasoning[reasoning.length - 1]}` })
  }
  return abilities
}

/** A token count in short form: 1.1M, 128K. */
export function formatCatalogTokens(tokens: number): string {
  if (tokens >= 1_000_000) return `${Math.round(tokens / 100_000) / 10}M`
  if (tokens >= 1_000) return `${Math.round(tokens / 1_000)}K`
  return String(tokens)
}

/** One price tier as the admin edits it: numbers are kept as typed until saved. */
export interface PriceTierRow {
  above: number | string
  inclusive: boolean
  input: number | string
  output: number | string
  cached: number | string
}

export interface PriceTierValue {
  aboveInputTokens: number
  inclusive: boolean
  input: number
  output: number
  cachedInput?: number
}

export const PRICE_TIERS_MAX = 8
const TOKENS_LIMIT = 100_000_000

export function priceTierRows(tiers: PriceTierValue[] | null | undefined): PriceTierRow[] {
  return (tiers ?? []).map((tier) => ({
    above: tier.aboveInputTokens,
    inclusive: tier.inclusive,
    input: tier.input,
    output: tier.output,
    cached: tier.cachedInput ?? ''
  }))
}

function typedNumber(value: number | string): number | null {
  if (value === '' || value === null || value === undefined) return null
  const n = typeof value === 'number' ? value : Number(value)
  return Number.isFinite(n) ? n : null
}

export type PriceTierResult = { tiers: PriceTierValue[] } | { error: 'empty' | 'base' | 'order' | 'number' }

/**
 * The tiers the admin typed, checked as the server checks them: the first is the base price (0 tokens), the bounds
 * rise, and every price is a number that is not negative. A cached price may be left empty.
 */
export function priceTiersFromRows(rows: PriceTierRow[]): PriceTierResult {
  if (rows.length === 0) return { error: 'empty' }
  const tiers: PriceTierValue[] = []
  for (const [index, row] of rows.entries()) {
    const above = typedNumber(row.above)
    const input = typedNumber(row.input)
    const output = typedNumber(row.output)
    const cached = typedNumber(row.cached)
    if (above === null || input === null || output === null) return { error: 'number' }
    if (!Number.isInteger(above) || above < 0 || above > TOKENS_LIMIT) return { error: 'number' }
    if (input < 0 || output < 0 || (cached !== null && cached < 0)) return { error: 'number' }
    if (index === 0 && (above !== 0 || row.inclusive)) return { error: 'base' }
    if (index > 0 && above <= tiers[index - 1].aboveInputTokens) return { error: 'order' }
    tiers.push({
      aboveInputTokens: above,
      inclusive: index === 0 ? false : row.inclusive,
      input,
      output,
      ...(cached !== null ? { cachedInput: cached } : {})
    })
  }
  return { tiers }
}

export type PriceTierRangeKind = 'all' | 'upTo' | 'below' | 'above' | 'from'

/** A tier's range for the admin to read: up to 100K, below 200K, above 100K, from 200K. */
export function priceTierRange(tiers: PriceTierValue[], index: number): { kind: PriceTierRangeKind; tokens: string } {
  const tier = tiers[index]
  if (tier.aboveInputTokens === 0) {
    const next = tiers[index + 1]
    if (!next) return { kind: 'all', tokens: '' }
    return { kind: next.inclusive ? 'below' : 'upTo', tokens: formatCatalogTokens(next.aboveInputTokens) }
  }
  return { kind: tier.inclusive ? 'from' : 'above', tokens: formatCatalogTokens(tier.aboveInputTokens) }
}

export interface PriceTierLine {
  range: { kind: PriceTierRangeKind; tokens: string }
  input: number
  output: number
  cachedInput?: number
}

/** Every tier with its range and prices, for showing them all; empty when there is a single price. */
export function priceTierLines(tiers: PriceTierValue[] | null | undefined): PriceTierLine[] {
  if (!tiers || tiers.length < 2) return []
  return tiers.map((tier, index) => ({
    range: priceTierRange(tiers, index),
    input: tier.input,
    output: tier.output,
    ...(tier.cachedInput !== undefined ? { cachedInput: tier.cachedInput } : {})
  }))
}

export const THINKING_SETTINGS = ['off', 'between_tools', 'adaptive', 'budget'] as const
export const THINKING_LEVELS_MAX = 32
const EFFORT_NAME = /^[a-z][a-z0-9_-]{0,31}$/

export interface ThinkingValue {
  canBeOff: boolean | null
  settings: { value: string; maxEffort?: string }[]
}

/** The thinking settings as the admin edits them: one row per setting, and "can it be off" as yes, no or unknown. */
export interface ThinkingForm {
  canBeOff: 'unknown' | 'yes' | 'no'
  settings: { value: string; on: boolean; maxEffort: string }[]
}

export function thinkingForm(thinking: ThinkingValue | null | undefined): ThinkingForm {
  const canBeOff = thinking?.canBeOff === true ? 'yes' : thinking?.canBeOff === false ? 'no' : 'unknown'
  return {
    canBeOff,
    settings: THINKING_SETTINGS.map((value) => {
      const found = thinking?.settings?.find((s) => s.value === value)
      return { value, on: !!found, maxEffort: found?.maxEffort ?? '' }
    })
  }
}

export type ThinkingResult = { thinking: ThinkingValue | null } | { error: 'offConflict' | 'effort' }

/** The thinking the admin set, checked as the server checks it; null when nothing is said (not known). */
export function thinkingFromForm(form: ThinkingForm): ThinkingResult {
  const settings: ThinkingValue['settings'] = []
  for (const setting of form.settings) {
    if (!setting.on) continue
    const effort = setting.maxEffort.trim().toLowerCase()
    if (effort && !EFFORT_NAME.test(effort)) return { error: 'effort' }
    settings.push(effort ? { value: setting.value, maxEffort: effort } : { value: setting.value })
  }
  const canBeOff = form.canBeOff === 'yes' ? true : form.canBeOff === 'no' ? false : null
  if (canBeOff === false && settings.some((s) => s.value === 'off')) return { error: 'offConflict' }
  if (canBeOff === null && settings.length === 0) return { thinking: null }
  return { thinking: { canBeOff, settings } }
}

export interface ThinkingLevelValue {
  setting: string
  effort?: string
  complexity: string
  extraOutput: string
}

export interface ThinkingLevelRow {
  setting: string
  effort: string
  complexity: string
  extraOutput: string
}

export function levelRows(levels: ThinkingLevelValue[] | null | undefined): ThinkingLevelRow[] {
  return (levels ?? []).map((level) => ({
    setting: level.setting,
    effort: level.effort ?? '',
    complexity: level.complexity,
    extraOutput: level.extraOutput
  }))
}

export type ThinkingLevelsResult = { levels: ThinkingLevelValue[] } | { error: 'incomplete' | 'effort' }

export function levelsFromRows(rows: ThinkingLevelRow[]): ThinkingLevelsResult {
  const levels: ThinkingLevelValue[] = []
  for (const row of rows) {
    if (!row.setting || !row.complexity || !row.extraOutput) return { error: 'incomplete' }
    const effort = row.effort.trim().toLowerCase()
    if (effort && !EFFORT_NAME.test(effort)) return { error: 'effort' }
    levels.push({
      setting: row.setting,
      ...(effort ? { effort } : {}),
      complexity: row.complexity,
      extraOutput: row.extraOutput
    })
  }
  return { levels }
}

export const IMAGE_TIERS = ['1K', '2K', '4K'] as const
export type ImageTier = (typeof IMAGE_TIERS)[number]
export const IMAGE_SIZES_MAX = 12
const IMAGE_EDGE_MIN = 16
const IMAGE_EDGE_MAX = 8192
const IMAGE_PRICE_MAX = 1000

/** A typed list of sizes or qualities: split at commas and spaces, lower case, each once. */
export function parseImageList(text: string): string[] {
  const seen = new Set<string>()
  for (const part of text.split(/[,，\s]+/)) {
    const value = part.trim().toLowerCase()
    if (value) seen.add(value)
  }
  return [...seen]
}

/** The price tier billing charges an image size in: by its longest side, up to 1024 is 1K, up to 2048 is 2K, else 4K. */
export function imageSizeTier(size: string): ImageTier | null {
  const match = /^(\d{2,5})x(\d{2,5})$/.exec(size.trim().toLowerCase())
  if (!match) return null
  const longest = Math.max(Number(match[1]), Number(match[2]))
  if (longest <= 1024) return '1K'
  if (longest <= 2048) return '2K'
  return '4K'
}

/** What is wrong with the typed sizes, as the server checks them; null when they are fine. */
export function checkImageSizes(sizes: string[]): 'none' | 'format' | 'many' | null {
  if (sizes.length === 0) return 'none'
  if (sizes.length > IMAGE_SIZES_MAX) return 'many'
  for (const size of sizes) {
    const match = /^(\d{2,5})x(\d{2,5})$/.exec(size)
    if (!match) return 'format'
    for (const edge of [match[1], match[2]]) {
      const n = Number(edge)
      if (n < IMAGE_EDGE_MIN || n > IMAGE_EDGE_MAX) return 'format'
    }
  }
  return null
}

/** A typed image price: empty follows billing (null); otherwise a number that is not negative. */
export function imagePriceFromText(value: number | string | null | undefined): { price: number | null } | { error: 'number' } {
  const n = typedNumber(value ?? '')
  if (value === '' || value === null || value === undefined) return { price: null }
  if (n === null || n < 0 || n > IMAGE_PRICE_MAX) return { error: 'number' }
  return { price: n }
}
