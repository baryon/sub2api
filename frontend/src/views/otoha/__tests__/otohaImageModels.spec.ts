import { describe, expect, it } from 'vitest'
import { imageModelRows } from '../otohaRules'
import type { OtohaImageModel } from '@/api/otoha'

const gpt: OtohaImageModel = {
  id: 'gpt-image-2', name: 'GPT Image 2', api: 'openai-images', edits: true, qualities: ['low', 'medium', 'high'],
  currency: 'USD', per: 'image',
  sizes: [{ size: '1024x1024', tier: '2K', price: 0.3015 }, { size: '1536x1024', tier: '2K', price: 0.3015 }],
}
const grok: OtohaImageModel = {
  id: 'grok-imagine-image', name: 'grok-imagine-image', api: 'openai-images', edits: false, qualities: [],
  currency: 'USD', per: 'image', sizes: [{ size: '1024x1024', tier: '1K', price: 0.03 }],
}

describe('imageModelRows', () => {
  it('lists each picture model with its price per picture for every size, in the catalog order', () => {
    expect(imageModelRows([gpt, grok])).toEqual([
      { id: 'gpt-image-2', name: 'GPT Image 2', edits: true, app: true, endpoint: '', sizes: [{ size: '1024×1024', price: '$0.3015' }, { size: '1536×1024', price: '$0.3015' }] },
      { id: 'grok-imagine-image', name: 'grok-imagine-image', edits: false, app: true, endpoint: '', sizes: [{ size: '1024×1024', price: '$0.03' }] },
    ])
  })

  it('is empty when the catalog has no picture models or predates them', () => {
    expect(imageModelRows(undefined)).toEqual([])
    expect(imageModelRows([])).toEqual([])
  })

  it('leaves out a model without any priced size, and a size without a price', () => {
    const unpriced: OtohaImageModel = { ...grok, id: 'x', name: '', sizes: [{ size: '1024x1024', tier: '1K', price: Number.NaN }] }
    const partly: OtohaImageModel = { ...gpt, sizes: [{ size: '1024x1024', tier: '2K', price: 0.3015 }, { size: '1536x1024', tier: '2K', price: Number.NaN }] }
    expect(imageModelRows([unpriced, partly])).toEqual([
      { id: 'gpt-image-2', name: 'GPT Image 2', edits: true, app: true, endpoint: '', sizes: [{ size: '1024×1024', price: '$0.3015' }] },
    ])
  })

  it('says when the Otoha app cannot use a picture model, and which call an API key makes', () => {
    const gemini: OtohaImageModel = {
      id: 'gemini-3.1-flash-image', name: 'Nano Banana 2', api: 'gemini-generate-content',
      endpoint: 'POST /v1beta/models/gemini-3.1-flash-image:generateContent', app: false, edits: true, qualities: [],
      currency: 'USD', per: 'image', sizes: [{ size: '1024x1024', tier: '1K', price: 0.1008 }],
    }
    expect(imageModelRows([{ ...gpt, endpoint: 'POST /v1/images/generations', app: true }, gemini])).toEqual([
      { id: 'gpt-image-2', name: 'GPT Image 2', edits: true, app: true, endpoint: 'POST /v1/images/generations', sizes: [{ size: '1024×1024', price: '$0.3015' }, { size: '1536×1024', price: '$0.3015' }] },
      { id: 'gemini-3.1-flash-image', name: 'Nano Banana 2', edits: true, app: false, endpoint: 'POST /v1beta/models/gemini-3.1-flash-image:generateContent', sizes: [{ size: '1024×1024', price: '$0.1008' }] },
    ])
  })

  it('shows the id when the model has no name', () => {
    expect(imageModelRows([{ ...grok, name: '' }])[0]?.name).toBe('grok-imagine-image')
  })
})
