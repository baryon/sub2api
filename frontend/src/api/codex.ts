export interface CodexModelsManifestResult {
  content: string
  modelCount: number
}

const DEFAULT_CODEX_CLIENT_VERSION = '0.147.0'

function normalizeCodexBaseUrl(baseUrl: string): string {
  const fallback = typeof window !== 'undefined' ? window.location.origin : ''
  const value = (baseUrl || fallback).trim().replace(/\/+$/, '')
  if (!value) return '/v1'
  return /\/v1$/i.test(value) ? value : `${value}/v1`
}

export function buildCodexModelsManifestUrl(
  baseUrl: string,
  clientVersion = DEFAULT_CODEX_CLIENT_VERSION
): string {
  const url = normalizeCodexBaseUrl(baseUrl)
  const params = new URLSearchParams({ client_version: clientVersion })
  return `${url}/models?${params.toString()}`
}

function isCodexModelsManifest(value: unknown): value is { models: unknown[] } {
  if (typeof value !== 'object' || value === null) return false
  return Array.isArray((value as { models?: unknown }).models)
}

export async function fetchCodexModelsManifest(
  baseUrl: string,
  apiKey: string,
  signal?: AbortSignal
): Promise<CodexModelsManifestResult> {
  const response = await fetch(buildCodexModelsManifestUrl(baseUrl), {
    method: 'GET',
    headers: {
      Accept: 'application/json',
      Authorization: `Bearer ${apiKey}`
    },
    cache: 'no-store',
    signal
  })

  if (!response.ok) {
    throw new Error(`Codex models request failed with status ${response.status}`)
  }

  const payload: unknown = await response.json()
  if (!isCodexModelsManifest(payload)) {
    throw new Error('Codex models response is not a valid manifest')
  }

  const content = JSON.stringify(payload, null, 2)
  if (!content) {
    throw new Error('Codex models response is empty')
  }

  return {
    content,
    modelCount: payload.models.length
  }
}
