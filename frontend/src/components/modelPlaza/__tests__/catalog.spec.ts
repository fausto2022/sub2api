import { describe, expect, it } from 'vitest'
import type { ModelPlazaGroup, PlazaModel, PlazaModelType } from '@/api/modelPlaza'
import { buildPlazaCatalog, filterPlazaCatalog } from '../catalog'

function model(name: string, model_type?: PlazaModelType): PlazaModel {
  return { name, model_type, platform: 'openai', pricing: null, official_pricing: null }
}

function group(id: number, models: PlazaModel[]): ModelPlazaGroup {
  return {
    id, name: `group-${id}`, description: '', platform: 'openai', subscription_type: 'standard',
    rate_multiplier: 1, peak_rate_enabled: false, peak_start: '', peak_end: '', peak_rate_multiplier: 1,
    is_exclusive: id === 3, image_rate_independent: false, image_rate_multiplier: 1,
    video_rate_independent: false, video_rate_multiplier: 1, long_context_pricing_enabled: false, models
  }
}

const catalog = buildPlazaCatalog([
  group(1, [model('chat-a', 'text'), model('image-a', 'image')]),
  group(2, []),
  group(3, [model('chat-b', 'text'), model('custom-unknown')])
])

describe('model plaza catalog', () => {
  it('groups by type first and intersects type, group and model search', () => {
    expect(catalog.types).toEqual(['text', 'image', 'other'])
    const result = filterPlazaCatalog(catalog, 'text', 3, ' CHAT-B ')
    expect(result.sections.map(section => [section.type, section.groups[0].group.id])).toEqual([['text', 3]])
    expect(result.sections[0].groups[0].models.map(entry => entry.name)).toEqual(['chat-b'])
    expect(filterPlazaCatalog(catalog, 'image', 3, '').noMatch).toBe(true)
  })

  it('keeps empty groups separate when browsing or selecting a specific group', () => {
    const filtered = filterPlazaCatalog(catalog, 'image', 2, '')
    expect(filtered.sections).toEqual([])
    expect(filtered.emptyGroups.map(entry => entry.id)).toEqual([2])
    expect(filtered.noMatch).toBe(false)
    expect(filterPlazaCatalog(catalog, 'all', 'all', 'no-model').emptyGroups.map(entry => entry.id)).toEqual([2])
  })
})
