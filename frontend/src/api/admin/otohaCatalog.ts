/**
 * Admin API for a group's Otoha model catalog: the models the Otoha app lists, with their abilities, profile
 * and price.
 */

import { apiClient } from '../client'

export type OtohaCatalogProblem =
  | ''
  | 'disabled'
  | 'not_allowed'
  | 'no_route'
  | 'no_native_api'
  | 'api_unreachable'
  | 'channel_restricted'
  | 'no_account'
  | 'no_price'
  | 'upstream_missing'

export interface OtohaModelPrice {
  currency: string
  per: string
  input: number
  output: number
  cachedInput?: number
}

/** The price of requests whose input is longer than a bound; the first tier (bound 0) is the base price. */
export interface OtohaPriceTier {
  aboveInputTokens: number
  /** The tier applies at the bound itself, not only above it. */
  inclusive: boolean
  input: number
  output: number
  cachedInput?: number
}

export type OtohaCompactionKind = 'none' | 'anthropic' | 'openai'

/** Whether the model service compacts a conversation itself. */
export interface OtohaCompaction {
  kind: OtohaCompactionKind
  minTrigger?: number
}

/** A thinking setting a model accepts; maxEffort is the highest effort it is accepted at (empty: any). */
export interface OtohaThinkingSetting {
  value: string
  maxEffort?: string
}

/** What a model accepts for thinking; null (on the entry) when not known. */
export interface OtohaThinking {
  canBeOff: boolean | null
  settings: OtohaThinkingSetting[]
}

/** The profile of one thinking level. */
export interface OtohaThinkingLevel {
  setting: string
  effort?: string
  complexity: string
  extraOutput: string
}

/** The format the app calls a model in; '' derives it from the provider. */
export type OtohaCatalogAPI = '' | 'anthropic-messages' | 'deepseek-responses' | 'openai-responses'

export interface OtohaCatalogEntryInput {
  model_id: string
  name: string
  description: string
  enabled: boolean
  inputs: string[]
  tools: boolean
  context: number
  max_output: number
  reasoning: string[]
  default_reasoning: string
  speed: string
  strengths: Record<string, string>
  complexity: string
  roles: string[]
  use: string[]
  profile_source: string
  cost_tier: string
  api: OtohaCatalogAPI
  /** The admin's price tiers (sale prices); empty derives them from billing. */
  price_tiers: OtohaPriceTier[]
  compaction: OtohaCompaction
  /** The thinking settings the model accepts; null when not known. */
  thinking: OtohaThinking | null
  /** The profile per thinking level; empty when not known. */
  levels: OtohaThinkingLevel[]
}

export interface OtohaCatalogEntry extends OtohaCatalogEntryInput {
  id: number
  group_id: number
  sort_order: number
  created_at: string
  updated_at: string
}

export interface OtohaCatalogAdminEntry extends OtohaCatalogEntry {
  upstream_price: OtohaModelPrice | null
  sale_price: OtohaModelPrice | null
  effective_cost: string
  effective_use: string[] | null
  in_catalog: boolean
  problem: OtohaCatalogProblem
  /** In a mixed group: the provider the app's requests for the model go to ('' when it cannot be told). */
  route_platform: string
  /** In a mixed group: the model the requests are forwarded as, when a route renames it. */
  route_model: string
  /** The model the price is that of, when accounts bill the model under another name. */
  billed_model: string
  /** The group's accounts bill the model at different prices; the highest is shown. */
  price_varies: boolean
  /** The format the app calls the model in: the admin's choice, else the provider's own ('' when none). */
  effective_api: OtohaCatalogAPI
  /** The price tiers the app receives: the admin's, else derived from billing. */
  effective_price_tiers: OtohaPriceTier[] | null
}

/** What the admin sets on an image model of the catalog; a price left null follows billing. */
export interface OtohaImageEntryInput {
  model_id: string
  name: string
  description: string
  enabled: boolean
  sizes: string[]
  qualities: string[]
  edits: boolean
  price_1k: number | null
  price_2k: number | null
  price_4k: number | null
}

/** The price of one image of a size tier: what billing charges before the group's rate and after it. */
export interface OtohaImageTierPrice {
  tier: '1K' | '2K' | '4K'
  upstream: number
  sale: number
  /** The admin set it on the model (else it follows billing). */
  set?: boolean
}

export interface OtohaImageAdminEntry extends OtohaImageEntryInput {
  id: number
  group_id: number
  sort_order: number
  created_at: string
  updated_at: string
  prices: OtohaImageTierPrice[]
  in_catalog: boolean
  problem: OtohaCatalogProblem | 'images_not_allowed'
  route_platform: string
  /** 'auto' entries were found from the group's accounts; 'manual' ones were added by an admin. */
  source: 'manual' | 'auto'
  /** On an auto entry: the fields an admin changed, which later checks leave alone. */
  manual_fields: string[]
  /** Since when the accounts' model lists no longer show the model; null while they do. */
  unavailable_since: string | null
}

/** How the accounts' model lists marked a model as an image model. */
export type OtohaImageEvidence = 'declared' | 'known_family' | 'builtin_catalog'
export type OtohaImageDeclaredBy =
  | ''
  | 'output_modalities'
  | 'image_generation_models'
  | 'image_price'
  | 'type'
  | 'capabilities'
export type OtohaImageDiscoveryResult = 'added' | 'updated' | 'unchanged' | 'manual_kept' | 'restored' | 'not_added'
export type OtohaImageDiscoveryReason =
  | ''
  | 'gateway_not_supported'
  | 'blocked_by_group_models'
  | 'no_account'
  | 'channel_restricted'
  | 'no_route'

export interface OtohaImageDiscoveryAccount {
  id: number
  name: string
  platform: string
  type: string
  listed: number
  image_models: number
  error: '' | 'list_failed' | 'unsupported'
}

export interface OtohaImageDiscoveryCandidate {
  model_id: string
  name: string
  /** Names of the accounts that list or serve the model. */
  accounts: string[]
  evidence: OtohaImageEvidence
  declared_by: OtohaImageDeclaredBy
  result: OtohaImageDiscoveryResult
  reason: OtohaImageDiscoveryReason
}

/** What the last check of the group's accounts found, and why something was not added. */
export interface OtohaImageDiscovery {
  /** When the check ran (RFC 3339); null when it never ran. */
  ran_at: string | null
  trigger: 'admin' | 'schedule' | ''
  /** Set when the whole check was skipped. */
  skipped_reason: '' | 'group_platform' | 'no_accounts'
  accounts: OtohaImageDiscoveryAccount[]
  candidates: OtohaImageDiscoveryCandidate[]
  /** Models marked unavailable by this check because the accounts no longer list them. */
  missing: string[]
}

/** An image model as the app receives it: each size with its price tier and the price of one image. */
export interface OtohaImageModel {
  id: string
  name: string
  description?: string
  api: string
  edits: boolean
  qualities: string[]
  currency: string
  per: string
  sizes: { size: string; tier: '1K' | '2K' | '4K'; price: number }[]
}

export interface OtohaCatalogModel {
  id: string
  name: string
  [key: string]: unknown
}

export interface OtohaCatalog {
  schema: number
  revision: string
  models: OtohaCatalogModel[]
  imageModels?: OtohaImageModel[]
}

export interface OtohaCatalogAdminView {
  group_id: number
  group_name: string
  group_platform: string
  rate_multiplier: number
  entries: OtohaCatalogAdminEntry[]
  image_entries: OtohaImageAdminEntry[]
  image_discovery: OtohaImageDiscovery | null
  /** The group's own switch for drawing (stays an explicit admin choice). */
  group_allow_image_generation: boolean
  preview: OtohaCatalog | null
}

export interface OtohaCatalogPrefill {
  entry: OtohaCatalogEntry
  metadata_found: boolean
  upstream_price: OtohaModelPrice | null
  sale_price: OtohaModelPrice | null
  cost_tier: string
  route_platform: string
  /** The format the app would call the model in, from the provider. */
  route_api: OtohaCatalogAPI
}

export interface OtohaCatalogSettings {
  /** The group the server serves the Otoha app from; 0 when none is set. */
  otoha_group_id: number
}

export async function getCatalog(groupId: number): Promise<OtohaCatalogAdminView> {
  const { data } = await apiClient.get<OtohaCatalogAdminView>(`/admin/groups/${groupId}/otoha-catalog`)
  return data
}

export async function createEntry(groupId: number, entry: OtohaCatalogEntryInput): Promise<OtohaCatalogEntry> {
  const { data } = await apiClient.post<OtohaCatalogEntry>(`/admin/groups/${groupId}/otoha-catalog/entries`, entry)
  return data
}

export async function updateEntry(
  groupId: number,
  entryId: number,
  entry: OtohaCatalogEntryInput
): Promise<OtohaCatalogEntry> {
  const { data } = await apiClient.put<OtohaCatalogEntry>(
    `/admin/groups/${groupId}/otoha-catalog/entries/${entryId}`,
    entry
  )
  return data
}

export async function deleteEntry(groupId: number, entryId: number): Promise<void> {
  await apiClient.delete(`/admin/groups/${groupId}/otoha-catalog/entries/${entryId}`)
}

export async function reorder(groupId: number, entryIds: number[]): Promise<void> {
  await apiClient.put(`/admin/groups/${groupId}/otoha-catalog/order`, { entry_ids: entryIds })
}

export async function prefill(groupId: number, modelId: string): Promise<OtohaCatalogPrefill> {
  const { data } = await apiClient.post<OtohaCatalogPrefill>(`/admin/groups/${groupId}/otoha-catalog/prefill`, {
    model_id: modelId
  })
  return data
}

export async function createImageEntry(groupId: number, entry: OtohaImageEntryInput): Promise<OtohaImageAdminEntry> {
  const { data } = await apiClient.post<OtohaImageAdminEntry>(`/admin/groups/${groupId}/otoha-catalog/image-entries`, entry)
  return data
}

export async function updateImageEntry(
  groupId: number,
  entryId: number,
  entry: OtohaImageEntryInput
): Promise<OtohaImageAdminEntry> {
  const { data } = await apiClient.put<OtohaImageAdminEntry>(
    `/admin/groups/${groupId}/otoha-catalog/image-entries/${entryId}`,
    entry
  )
  return data
}

export async function deleteImageEntry(groupId: number, entryId: number): Promise<void> {
  await apiClient.delete(`/admin/groups/${groupId}/otoha-catalog/image-entries/${entryId}`)
}

export async function discoverImages(groupId: number): Promise<OtohaImageDiscovery> {
  const { data } = await apiClient.post<OtohaImageDiscovery>(`/admin/groups/${groupId}/otoha-catalog/image-discovery`)
  return data
}

export async function getSettings(): Promise<OtohaCatalogSettings> {
  const { data } = await apiClient.get<OtohaCatalogSettings>('/admin/otoha-catalog/settings')
  return data
}

export const otohaCatalogAPI = {
  getSettings,
  getCatalog,
  createEntry,
  updateEntry,
  deleteEntry,
  createImageEntry,
  updateImageEntry,
  deleteImageEntry,
  discoverImages,
  reorder,
  prefill
}

export default otohaCatalogAPI
