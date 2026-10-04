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
