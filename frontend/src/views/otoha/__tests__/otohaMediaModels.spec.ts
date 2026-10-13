import { describe, expect, it } from 'vitest'
import { quickModelRows, videoModelRows, voiceModelRows } from '../otohaRules'
import type { OtohaQuickModel, OtohaVideoModel, OtohaVoiceModel } from '@/api/otoha'

const video: OtohaVideoModel = {
  id: 'grok-imagine-video-1.5', name: 'Grok Imagine Video 1.5', api: 'grok-videos', endpoint: 'POST /v1/videos/generations', app: false,
  currency: 'USD', per: 'second',
  resolutions: [{ resolution: '480p', price: 0.12 }, { resolution: '720p', price: 0.21 }, { resolution: '1080p', price: 0.375 }],
}
const tts: OtohaVoiceModel = {
  id: 'tts', name: 'Grok text to speech', api: 'grok-voice', endpoint: 'POST /v1/tts', app: false, mode: 'tts',
  currency: 'USD', per: '1M characters', price: 22.5,
}
const stt: OtohaVoiceModel = { ...tts, id: 'stt', name: 'Grok speech to text', endpoint: 'POST /v1/stt', mode: 'stt', per: 'hour', price: 0.15 }
const jev: OtohaQuickModel = {
  id: 'jev-latest', name: 'Jev', description: 'Quick judgements.', api: 'typesafe-systemone', endpoint: 'POST /v1/systemone', app: false,
  currency: 'USD', per: '1M tokens', billed: 'input', price: 0.063,
}

describe('videoModelRows', () => {
  it('lists each resolution with the price of one second of video', () => {
    expect(videoModelRows([video])).toEqual([{
      id: 'grok-imagine-video-1.5', name: 'Grok Imagine Video 1.5', description: '', endpoint: 'POST /v1/videos/generations', app: false,
      prices: [{ key: '480p', price: '$0.12' }, { key: '720p', price: '$0.21' }, { key: '1080p', price: '$0.375' }],
    }])
  })

  it('is empty for no models, a catalog from before video models, or a model without a priced resolution', () => {
    expect(videoModelRows(undefined)).toEqual([])
    expect(videoModelRows([])).toEqual([])
    expect(videoModelRows([{ ...video, resolutions: [{ resolution: '480p', price: Number.NaN }] }])).toEqual([])
  })

  it('shows the id when the model has no name, and keeps the description', () => {
    const rows = videoModelRows([{ ...video, name: '', description: 'Short clips' }])
    expect(rows[0]?.name).toBe('grok-imagine-video-1.5')
    expect(rows[0]?.description).toBe('Short clips')
  })
})

describe('voiceModelRows', () => {
  it('gives each voice function its price per unit, keyed by the function', () => {
    expect(voiceModelRows([tts, stt])).toEqual([
      { id: 'tts', name: 'Grok text to speech', description: '', endpoint: 'POST /v1/tts', app: false, prices: [{ key: 'tts', price: '$22.50' }] },
      { id: 'stt', name: 'Grok speech to text', description: '', endpoint: 'POST /v1/stt', app: false, prices: [{ key: 'stt', price: '$0.15' }] },
    ])
  })

  it('leaves out a function without a price and tolerates a missing list', () => {
    expect(voiceModelRows([{ ...tts, price: Number.NaN }])).toEqual([])
    expect(voiceModelRows(undefined)).toEqual([])
  })
})

describe('quickModelRows', () => {
  it('gives the price per million input tokens', () => {
    expect(quickModelRows([jev])).toEqual([{
      id: 'jev-latest', name: 'Jev', description: 'Quick judgements.', endpoint: 'POST /v1/systemone', app: false,
      prices: [{ key: 'input', price: '$0.063' }],
    }])
  })

  it('adds the output price only when output tokens are charged too', () => {
    expect(quickModelRows([{ ...jev, outputPrice: 0.5 }])[0]?.prices).toEqual([{ key: 'input', price: '$0.063' }, { key: 'output', price: '$0.50' }])
  })

  it('is empty when the catalog has none', () => {
    expect(quickModelRows(undefined)).toEqual([])
    expect(quickModelRows([])).toEqual([])
  })
})
