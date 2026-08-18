import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  buildCodexModelsManifestUrl,
  fetchCodexModelsManifest
} from '../codex'

describe('Codex models API', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('normalizes the configured API base and routes through the Codex manifest query', () => {
    expect(buildCodexModelsManifestUrl('https://example.com/api/v1/')).toBe(
      'https://example.com/api/v1/models?client_version=0.147.0'
    )
    expect(buildCodexModelsManifestUrl('/api')).toBe(
      '/api/v1/models?client_version=0.147.0'
    )
  })

  it('fetches and formats a Codex model manifest without changing its model metadata', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({
        models: [
          { slug: 'gpt-5.6-luna', display_name: 'GPT 5.6 Luna' },
          { slug: 'deepseek-v4-pro', display_name: 'DeepSeek V4 Pro' }
        ]
      })
    })
    vi.stubGlobal('fetch', fetchMock)

    const result = await fetchCodexModelsManifest('https://example.com/v1', 'sk-test')

    expect(fetchMock).toHaveBeenCalledWith(
      'https://example.com/v1/models?client_version=0.147.0',
      expect.objectContaining({
        method: 'GET',
        cache: 'no-store',
        headers: {
          Accept: 'application/json',
          Authorization: 'Bearer sk-test'
        }
      })
    )
    expect(result.modelCount).toBe(2)
    expect(JSON.parse(result.content)).toEqual({
      models: [
        { slug: 'gpt-5.6-luna', display_name: 'GPT 5.6 Luna' },
        { slug: 'deepseek-v4-pro', display_name: 'DeepSeek V4 Pro' }
      ]
    })
  })

  it('rejects a successful response that is not a model manifest', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ object: 'list', data: [] })
    }))

    await expect(fetchCodexModelsManifest('https://example.com/v1', 'sk-test'))
      .rejects.toThrow('valid manifest')
  })
})
