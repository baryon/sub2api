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

/** A picture model and what one picture costs in each size (TASK-73). */
export interface OtohaImageModel {
  id: string
  name: string
  api: string
  edits: boolean
  qualities: string[]
  currency: string
  per: string
  sizes: { size: string; tier: string; price: number }[]
}

/** A video model and what one second of video costs at each resolution (TASK-81). The Otoha app cannot use it yet. */
export interface OtohaVideoModel {
  id: string
  name: string
  description?: string
  api: string
  /** The call an API key makes, such as "POST /v1/videos/generations". */
  endpoint: string
  /** Whether the Otoha app can use the model. */
  app: boolean
  currency: string
  per: string
  resolutions: { resolution: string; price: number }[]
}

/** A voice function ("tts" speech from text, "stt" text from speech) and its price per unit (TASK-81). */
export interface OtohaVoiceModel {
  id: string
  name: string
  description?: string
  api: string
  endpoint: string
  app: boolean
  mode: string
  currency: string
  /** The unit the price is for: "1M characters" for tts, "hour" for stt. */
  per: string
  price: number
}

/** A quick-judgement model, billed on input tokens, price per million (TASK-81). */
export interface OtohaQuickModel {
  id: string
  name: string
  description?: string
  api: string
  endpoint: string
  app: boolean
  currency: string
  per: string
  billed: string
  price: number
  /** Only when output tokens are charged too. */
  outputPrice?: number
}

export interface OtohaCatalog {
  schema: number
  revision: string
  models: OtohaCatalogModel[]
  /** Absent in catalogs published before picture models were listed. */
  imageModels?: OtohaImageModel[]
  /** Absent in catalogs published before video, voice and quick-judgement models were listed. */
  videoModels?: OtohaVideoModel[]
  voiceModels?: OtohaVoiceModel[]
  quickModels?: OtohaQuickModel[]
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

export interface OtohaAutoTopUpCard {
  brand: string
  last4: string
  exp_month: number
  exp_year: number
}

export interface OtohaAutoTopUpLast {
  at: string
  amount_usd: number
  /** pending, succeeded, failed or requires_action. */
  result: string
}

/** Automatic top-up: never carries a card number, only its brand and last four digits. */
export interface OtohaAutoTopUp {
  enabled: boolean
  threshold_usd: number
  amount_usd: number
  monthly_cap_usd: number
  used_this_month_usd: number
  card: OtohaAutoTopUpCard | null
  /** Why the system turned it off (authentication_required, card_declined, payment_error); empty otherwise. */
  disabled_reason: string
  disabled_at: string | null
  last_top_up: OtohaAutoTopUpLast | null
  manage_url: string
  /** Only on the settings endpoint: whether paying by bank card works now. */
  card_payments_available?: boolean
}

export interface OtohaAutoTopUpSettings {
  enabled: boolean
  threshold_usd: number
  amount_usd: number
  monthly_cap_usd: number
}

export interface OtohaCardSetupStart {
  client_secret: string
  publishable_key: string
}

export interface OtohaAccount {
  group_id: number
  email: string
  balance: number
  plan: OtohaAccountPlan | null
  /** The group's model catalog; null until the catalog is published (TASK-54). */
  catalog: OtohaCatalog | null
  /** Null when the server has no automatic top-up. */
  auto_topup: OtohaAutoTopUp | null
}

export const otohaAPI = {
  getAccount() {
    return apiClient.get<OtohaAccount>('/otoha/account')
  },

  createClaim() {
    return apiClient.post<OtohaClaimCode>('/otoha/claims')
  },

  getAutoTopUp() {
    return apiClient.get<OtohaAutoTopUp>('/otoha/auto-topup')
  },

  updateAutoTopUp(settings: OtohaAutoTopUpSettings) {
    return apiClient.put<OtohaAutoTopUp>('/otoha/auto-topup', settings)
  },

  /** Starts saving a card: what Stripe's card form needs. Nothing is charged. */
  startAutoTopUpCardSetup() {
    return apiClient.post<OtohaCardSetupStart>('/otoha/auto-topup/card-setup')
  },

  /** Keeps the card Stripe just saved. */
  finishAutoTopUpCardSetup(setupId: string) {
    return apiClient.post<OtohaAutoTopUp>('/otoha/auto-topup/card', { setup_id: setupId })
  },

  removeAutoTopUpCard() {
    return apiClient.delete<OtohaAutoTopUp>('/otoha/auto-topup/card')
  },

  /** The configuration file as a blob, for saving as config.json. */
  downloadConfig() {
    return apiClient.get<Blob>('/otoha/config', { responseType: 'blob' })
  },
}
