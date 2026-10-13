import { describe, expect, it } from 'vitest'
import { allowlistWithModels, mediaBlockedModels } from '../otohaCatalogMedia'

const blockedCandidate = { kind: 'video', model_id: 'grok-imagine-video-1.5', result: 'not_added', reason: 'blocked_by_group_models' }

describe('mediaBlockedModels', () => {
  it('names the models the group model list holds back, from entries and from the last check', () => {
    expect(
      mediaBlockedModels(
        [{ kind: 'quick', model_id: 'jev-latest', problem: 'not_allowed' }, { kind: 'video', model_id: 'grok-imagine-video', problem: '' }],
        { candidates: [blockedCandidate, { kind: 'video', model_id: 'x', result: 'not_added', reason: 'no_account' }] },
      ),
    ).toEqual(['jev-latest', 'grok-imagine-video-1.5'])
  })

  it('lists a model once and ignores voice, which names no model', () => {
    expect(
      mediaBlockedModels(
        [{ kind: 'video', model_id: 'grok-imagine-video-1.5', problem: 'not_allowed' }, { kind: 'voice', model_id: 'tts', problem: 'not_allowed' }],
        { candidates: [blockedCandidate] },
      ),
    ).toEqual(['grok-imagine-video-1.5'])
  })

  it('is empty without entries or a check', () => {
    expect(mediaBlockedModels(undefined, null)).toEqual([])
    expect(mediaBlockedModels([], { candidates: [] })).toEqual([])
  })
})

describe('allowlistWithModels', () => {
  it('keeps the models already allowed and adds the missing ones', () => {
    expect(allowlistWithModels({ enabled: true, models: ['gpt-6-luna', 'Jev-Latest'] }, ['jev-latest', 'grok-imagine-video'])).toEqual({
      enabled: true,
      models: ['gpt-6-luna', 'Jev-Latest', 'grok-imagine-video'],
    })
  })

  it('does not turn the list on when the group has none', () => {
    expect(allowlistWithModels({ enabled: false, models: [] }, ['jev-latest'])).toBeNull()
    expect(allowlistWithModels(undefined, ['jev-latest'])).toBeNull()
  })
})
