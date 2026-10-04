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

export interface OtohaModelPrice {
  currency: string
  per: string
  input: number
  output: number
  cachedInput?: number
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
}

export interface OtohaCatalogAdminView {
  group_id: number
  group_name: string
  group_platform: string
  rate_multiplier: number
  entries: OtohaCatalogAdminEntry[]
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
  reorder,
  prefill
}

export default otohaCatalogAPI
