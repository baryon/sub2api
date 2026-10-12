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
