import { describe, expect, it } from 'vitest'

import { getModelsByPlatform, getPresetMappingsByPlatform } from '../useModelWhitelist'

describe('DeepSeek model whitelist presets', () => {
  // The list was deliberately widened after this test was written (V4 vision/flash aliases, V3, R1 and
  // distill models), so assert the V4 models are present rather than the exact two-item list.
  it('exposes the official harness V4 models', () => {
    expect(getModelsByPlatform('deepseek')).toEqual(
      expect.arrayContaining(['deepseek-v4-flash', 'deepseek-v4-pro'])
    )
  })

  it('uses DeepSeek-specific presets instead of falling back to Anthropic', () => {
    expect(getPresetMappingsByPlatform('deepseek')).toEqual([
      expect.objectContaining({ from: 'deepseek-v4-flash', to: 'deepseek-v4-flash' }),
      expect.objectContaining({ from: 'deepseek-v4-pro', to: 'deepseek-v4-pro' }),
    ])
  })
})
