import { describe, expect, it } from 'vitest'

import {
  claudeAliasesFromModels,
  pickDefaultCodexModel,
  resolveClientSetupModels,
  showsClaudeModelSwitch,
  showsCodexModelSwitch
} from '../clientSetupModels'

describe('clientSetupModels', () => {
  it('prefers group models over platform fallbacks', () => {
    expect(resolveClientSetupModels('deepseek', [' deepseek-4-pro ', 'deepseek-4-pro'])).toEqual([
      'deepseek-4-pro'
    ])
  })

  it('falls back to DeepSeek catalog IDs when the group list is empty', () => {
    expect(resolveClientSetupModels('deepseek', [])).toEqual([
      'deepseek-v4-flash',
      'deepseek-v4-pro'
    ])
  })

  it('keeps current Codex defaults when they are in the group list', () => {
    expect(pickDefaultCodexModel(['deepseek-v4-flash', 'deepseek-v4-pro'], 'deepseek')).toBe(
      'deepseek-v4-pro'
    )
    expect(pickDefaultCodexModel(['gpt-5.4', 'gpt-5.5'], 'openai')).toBe('gpt-5.5')
    expect(pickDefaultCodexModel(['grok-build-0.1', 'grok-4.5'], 'grok')).toBe('grok-4.5')
    expect(pickDefaultCodexModel([], 'composite')).toBe('gpt-5.5')
  })

  it('maps DeepSeek Claude slots to pro and flash', () => {
    expect(
      claudeAliasesFromModels(['deepseek-v4-flash', 'deepseek-v4-pro'], 'deepseek')
    ).toEqual({
      default: 'deepseek-v4-pro',
      opus: 'deepseek-v4-pro',
      sonnet: 'deepseek-v4-pro',
      haiku: 'deepseek-v4-flash',
      fable: 'deepseek-v4-flash',
      subagent: 'deepseek-v4-flash'
    })
  })

  it('maps every Grok Claude slot to grok-4.5 when available', () => {
    expect(
      claudeAliasesFromModels(['grok-4.5', 'grok-build-0.1'], 'grok')
    ).toEqual({
      default: 'grok-4.5',
      opus: 'grok-4.5',
      sonnet: 'grok-4.5',
      haiku: 'grok-4.5',
      fable: 'grok-4.5',
      subagent: 'grok-4.5'
    })
  })

  it('shows Codex and Claude switching for the clients those groups support', () => {
    expect(showsCodexModelSwitch('deepseek')).toBe(true)
    expect(showsClaudeModelSwitch('deepseek')).toBe(true)
    expect(showsCodexModelSwitch('openai')).toBe(true)
    expect(showsCodexModelSwitch('composite')).toBe(true)
    expect(showsClaudeModelSwitch('openai')).toBe(false)
    expect(showsClaudeModelSwitch('openai', true)).toBe(true)
    expect(showsCodexModelSwitch('anthropic')).toBe(false)
    expect(showsClaudeModelSwitch('anthropic')).toBe(true)
  })
})
