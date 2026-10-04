/**
 * Admin API for a group's Otoha model catalog: the models the Otoha app lists, with their abilities, profile
 * and price.
 */

import { apiClient } from '../client'

export type OtohaCatalogProblem = '' | 'disabled' | 'not_allowed' | 'no_account' | 'no_price'

export interface OtohaModelPrice {
  currency: string
  per: string
  input: number
  output: number
  cachedInput?: number
}

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

export const otohaCatalogAPI = {
  getCatalog,
  createEntry,
  updateEntry,
  deleteEntry,
  reorder,
  prefill
}

export default otohaCatalogAPI
