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
      { id: 'gpt-image-2', name: 'GPT Image 2', edits: true, sizes: [{ size: '1024×1024', price: '$0.3015' }, { size: '1536×1024', price: '$0.3015' }] },
      { id: 'grok-imagine-image', name: 'grok-imagine-image', edits: false, sizes: [{ size: '1024×1024', price: '$0.03' }] },
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
      { id: 'gpt-image-2', name: 'GPT Image 2', edits: true, sizes: [{ size: '1024×1024', price: '$0.3015' }] },
    ])
  })

  it('shows the id when the model has no name', () => {
    expect(imageModelRows([{ ...grok, name: '' }])[0]?.name).toBe('grok-imagine-image')
  })
})
