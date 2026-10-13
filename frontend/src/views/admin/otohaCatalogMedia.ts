/** Rules for the admin page of the Otoha catalog's video, voice and quick-judgement models (TASK-81). */

interface BlockableEntry {
  kind: string
  model_id: string
  problem: string
}

interface BlockableDiscovery {
  candidates: { kind: string; model_id: string; result: string; reason: string }[]
}

/**
 * The models the group's own model list holds back: entries the list keeps out of the catalog, and models the last
 * check did not add for that reason. A voice request names no model, so the list never holds voice back.
 */
export function mediaBlockedModels(entries: BlockableEntry[] | undefined, discovery: BlockableDiscovery | null | undefined): string[] {
  const blocked: string[] = []
  const add = (modelId: string) => {
    if (!blocked.includes(modelId)) blocked.push(modelId)
  }
  for (const entry of entries ?? []) {
    if (entry.kind !== 'voice' && entry.problem === 'not_allowed') add(entry.model_id)
  }
  for (const candidate of discovery?.candidates ?? []) {
    if (candidate.kind !== 'voice' && candidate.result === 'not_added' && candidate.reason === 'blocked_by_group_models') add(candidate.model_id)
  }
  return blocked
}

/**
 * The group's model list with the given models added, keeping what it holds; null when the group has no model list
 * switched on, which the admin must not be made to turn on by this.
 */
export function allowlistWithModels(
  current: { enabled: boolean; models: string[] } | undefined,
  add: string[],
): { enabled: true; models: string[] } | null {
  if (!current?.enabled) return null
  const models = [...current.models]
  for (const modelId of add) {
    if (!models.some((existing) => existing.toLowerCase() === modelId.toLowerCase())) models.push(modelId)
  }
  return { enabled: true, models }
}
