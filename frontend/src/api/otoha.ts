/**
 * Otoha AI purchase pages: claim codes, the app configuration file and the account summary.
 */
import { apiClient } from './client'

export interface OtohaClaimCode {
  code: string
  expires_at: string
  open_url: string
}

export interface OtohaModelPrice {
  currency: string
  per: string
  input: number
  output: number
  cachedInput?: number
}

export interface OtohaCatalogModel {
  id: string
  name: string
  description?: string
  inputs: string[]
  images: boolean
  tools: boolean
  context?: number
  maxOutput?: number
  reasoning?: string[]
  cost: string
  price: OtohaModelPrice
  speed?: string
  use?: string[]
}

export interface OtohaCatalog {
  schema: number
  revision: string
  models: OtohaCatalogModel[]
}

export interface OtohaAccountPlan {
  /** The plan the current period was bought with; null for a subscription from before plans were recorded. */
  plan_id: number | null
  /** The subscription carries its plan's own allowance (a tier), so dearer tiers are upgrades. */
  has_own_allowance: boolean
  name: string
  expires_at: string
  monthly_limit_usd: number | null
  monthly_used_usd: number
  period_resets_at: string | null
}

export interface OtohaAccount {
  group_id: number
  email: string
  balance: number
  plan: OtohaAccountPlan | null
  /** The group's model catalog; null until the catalog is published (TASK-54). */
  catalog: OtohaCatalog | null
}

export const otohaAPI = {
  getAccount() {
    return apiClient.get<OtohaAccount>('/otoha/account')
  },

  createClaim() {
    return apiClient.post<OtohaClaimCode>('/otoha/claims')
  },

  /** The configuration file as a blob, for saving as config.json. */
  downloadConfig() {
    return apiClient.get<Blob>('/otoha/config', { responseType: 'blob' })
  },
}
