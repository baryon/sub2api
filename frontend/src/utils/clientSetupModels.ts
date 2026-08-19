import type { GroupPlatform } from '@/types'

export interface ClaudeModelAliases {
  default: string
  opus: string
  sonnet: string
  haiku: string
  fable: string
  subagent: string
}

const DEEPSEEK_FLASH_MODEL = 'deepseek-v4-flash'
const DEEPSEEK_PRO_MODEL = 'deepseek-v4-pro'
const GROK_DEFAULT_MODEL = 'grok-4.5'
const OPENAI_DEFAULT_MODEL = 'gpt-5.5'

const PLATFORM_FALLBACK_MODELS: Partial<Record<GroupPlatform, string[]>> = {
  deepseek: [DEEPSEEK_FLASH_MODEL, DEEPSEEK_PRO_MODEL],
  grok: [GROK_DEFAULT_MODEL, 'grok-4.3', 'grok-build-0.1', 'grok-4.20-multi-agent-0309'],
  openai: [OPENAI_DEFAULT_MODEL],
  gemini: ['gemini-2.0-flash']
}

const PLATFORM_PREFERRED_CODEX_MODEL: Partial<Record<GroupPlatform, string>> = {
  deepseek: DEEPSEEK_PRO_MODEL,
  grok: GROK_DEFAULT_MODEL,
  openai: OPENAI_DEFAULT_MODEL,
  composite: OPENAI_DEFAULT_MODEL
}

export function uniqueModelIDs(modelIDs: readonly string[] | undefined): string[] {
  const seen = new Set<string>()
  const models: string[] = []
  for (const raw of modelIDs ?? []) {
    const modelID = raw.trim()
    if (!modelID || seen.has(modelID)) {
      continue
    }
    seen.add(modelID)
    models.push(modelID)
  }
  return models
}

export function fallbackModelsForPlatform(platform: GroupPlatform | null | undefined): string[] {
  if (!platform) {
    return []
  }
  return [...(PLATFORM_FALLBACK_MODELS[platform] ?? [])]
}

export function resolveClientSetupModels(
  platform: GroupPlatform | null | undefined,
  modelIDs?: readonly string[]
): string[] {
  const requested = uniqueModelIDs(modelIDs)
  if (requested.length > 0) {
    return requested
  }
  return fallbackModelsForPlatform(platform)
}

export function pickDefaultCodexModel(
  models: readonly string[],
  platform: GroupPlatform | null | undefined
): string {
  const preferred = platform ? PLATFORM_PREFERRED_CODEX_MODEL[platform] : undefined
  if (preferred && models.includes(preferred)) {
    return preferred
  }
  return models[0] ?? preferred ?? ''
}

function modelQuality(modelID: string): number {
  const id = modelID.toLowerCase()
  if (id.includes('opus') || id.includes('-pro') || id.includes('max')) {
    return 4
  }
  if (id.includes('sonnet') || id.includes('gpt-5') || id.includes('4.5')) {
    return 3
  }
  if (id.includes('haiku') || id.includes('flash') || id.includes('mini') || id.includes('nano')) {
    return 1
  }
  return 2
}

function pickByName(models: readonly string[], pattern: RegExp): string | undefined {
  return models.find((modelID) => pattern.test(modelID))
}

function pickHighestQuality(models: readonly string[]): string {
  return [...models].sort((left, right) => modelQuality(right) - modelQuality(left) || left.localeCompare(right))[0] ?? ''
}

function pickLowestQuality(models: readonly string[]): string {
  return [...models].sort((left, right) => modelQuality(left) - modelQuality(right) || left.localeCompare(right))[0] ?? ''
}

function aliasesFromSameModel(modelID: string): ClaudeModelAliases {
  return {
    default: modelID,
    opus: modelID,
    sonnet: modelID,
    haiku: modelID,
    fable: modelID,
    subagent: modelID
  }
}

export function claudeAliasesFromModels(
  models: readonly string[],
  platform: GroupPlatform | null | undefined
): ClaudeModelAliases | null {
  if (models.length === 0) {
    return null
  }
  if (platform === 'grok') {
    return aliasesFromSameModel(pickDefaultCodexModel(models, 'grok'))
  }

  const highest = pickHighestQuality(models)
  const lowest = pickLowestQuality(models)
  const opus = pickByName(models, /opus|(?:^|[-_])pro(?:$|[-_])/i) ?? highest
  const sonnet = pickByName(models, /sonnet/i) ?? highest
  const haiku = pickByName(models, /haiku|flash|mini|nano/i) ?? lowest
  const fable = pickByName(models, /fable/i) ?? haiku
  return {
    default: sonnet,
    opus,
    sonnet,
    haiku,
    fable,
    subagent: haiku
  }
}

export function showsCodexModelSwitch(platform: GroupPlatform | null | undefined): boolean {
  return platform === 'openai' || platform === 'grok' || platform === 'deepseek' || platform === 'composite'
}

export function showsClaudeModelSwitch(
  platform: GroupPlatform | null | undefined,
  allowMessagesDispatch = false
): boolean {
  if (platform === 'anthropic' || platform === 'grok' || platform === 'deepseek' || platform === 'antigravity' || platform === 'composite') {
    return true
  }
  return platform === 'openai' && allowMessagesDispatch
}
