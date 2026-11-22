import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { describe, expect, it } from 'vitest'
import type { ModelPlazaGroup, PlazaModel } from '@/api/modelPlaza'
import en from '@/i18n/locales/en'
import ModelPlazaContent from '../ModelPlazaContent.vue'
import { buildPlazaCatalog, filterPlazaCatalog, resolvePlazaSelection } from '../catalog'

function model(name: string, modelType?: PlazaModel['model_type']): PlazaModel {
  return { name, model_type: modelType, platform: 'openai', pricing: null, official_pricing: null }
}

function group(id: number, platform: string, models: PlazaModel[]): ModelPlazaGroup {
  return {
    id, name: `group-${id}`, description: '', platform, subscription_type: 'standard',
    rate_multiplier: 1, peak_rate_enabled: false, peak_start: '', peak_end: '', peak_rate_multiplier: 1,
    is_exclusive: id === 2, image_rate_independent: false, image_rate_multiplier: 1,
    video_rate_independent: false, video_rate_multiplier: 1, long_context_pricing_enabled: false, models
  }
}

const empty = group(1, 'anthropic', [])
const general = { ...group(2, 'openai', [model('chat-a', 'text'), model('image-a', 'image')]), name: 'Shared plan' }
const alternate = { ...group(3, 'openai', [model('chat-b')]), name: 'Shared plan' }
const openAIEmpty = group(4, 'openai', [])
const domestic = { ...group(5, 'kimi', [model('custom-model')]), name: 'Anthropic lookalike' }
const other = group(6, 'future-provider', [model('another-model')])
const fixture = [other, alternate, domestic, general, openAIEmpty, empty]

function mountPlaza(groups = fixture) {
  return mount(ModelPlazaContent, {
    props: { response: { description: '', groups }, loading: false, authenticated: true },
    global: {
      plugins: [createI18n({ legacy: false, locale: 'en', messages: { en } })],
      stubs: {
        PlatformIcon: true,
        PlazaGroupSection: {
          props: ['group', 'models'],
          template: '<div :data-result-group="group.id">{{ group.name }} {{ models.map(model => model.name).join(" ") }}</div>'
        }
      }
    }
  })
}

describe('model plaza catalog', () => {
  it('indexes visible groups by configured group platform in key-management order', () => {
    const catalog = buildPlazaCatalog(fixture)
    expect(catalog.providers).toEqual(['anthropic', 'openai', 'domestic', 'other'])
    expect(catalog.groupsByProvider.get('anthropic')).toEqual([empty])
    expect(catalog.groupsByProvider.get('domestic')).toEqual([domestic])
    expect(catalog.groupsByProvider.get('other')).toEqual([other])
    expect(catalog.groupsByProvider.get('openai')?.map(({ id, name }) => [id, name])).toEqual([
      [4, 'group-4'], [2, 'Shared plan'], [3, 'Shared plan']
    ])
    expect(resolvePlazaSelection(catalog, null, 'all')).toEqual({ provider: 'anthropic', groupId: 'all' })
  })

  it('intersects group ID with model-name search and keeps all model types together', () => {
    const groups = [general, alternate, openAIEmpty]
    expect(filterPlazaCatalog(groups, 2, '').entries).toEqual([{ group: general, models: general.models }])
    expect(filterPlazaCatalog(groups, 2, ' IMAGE-A ').entries[0].models.map(entry => entry.name)).toEqual(['image-a'])
    expect(filterPlazaCatalog(groups, 2, 'chat-b').noMatch).toBe(true)
    expect(filterPlazaCatalog(groups, 'all', 'Shared plan').noMatch).toBe(true)
  })

  it('distinguishes model search misses from genuinely empty groups', () => {
    const groups = [general, openAIEmpty]
    expect(filterPlazaCatalog(groups, 'all', '').emptyGroups).toEqual([openAIEmpty])
    expect(filterPlazaCatalog(groups, 'all', 'missing')).toEqual({ entries: [], emptyGroups: [], noMatch: true })
    expect(filterPlazaCatalog(groups, 4, 'missing')).toEqual({ entries: [], emptyGroups: [openAIEmpty], noMatch: false })
  })

  it('keeps the desktop index and narrow select synchronized by ID, resetting group on vendor switch', async () => {
    const wrapper = mountPlaza()
    const provider = (id: string) => wrapper.get(`[data-plaza-provider="${id}"]`)
    const index = (id: number | 'all') => wrapper.get(`[data-plaza-group="${id}"]`)
    const select = wrapper.get<HTMLSelectElement>('[data-plaza-group-select]')

    expect(provider('anthropic').attributes('aria-pressed')).toBe('true')
    expect(wrapper.find('[data-result-group="1"]').exists()).toBe(true)
    await provider('openai').trigger('click')
    expect(provider('openai').text()).toBe(en.keys.providers.openai)
    expect(index('all').attributes('aria-pressed')).toBe('true')
    expect(index(2).text()).toBe(index(3).text())
    expect(wrapper.findAll('[data-result-group]')).toHaveLength(3)
    await index(2).trigger('click')
    expect(select.element.value).toBe('2')
    expect(wrapper.find('[data-result-group="3"]').exists()).toBe(false)
    await select.setValue('3')
    expect(index(3).attributes('aria-pressed')).toBe('true')
    expect(wrapper.find('[data-result-group="2"]').exists()).toBe(false)

    await provider('domestic').trigger('click')
    await provider('openai').trigger('click')
    expect(index('all').attributes('aria-pressed')).toBe('true')
    expect(select.element.value).toBe('all')
  })

  it('revalidates group and provider against the latest response only', async () => {
    const wrapper = mountPlaza()
    await wrapper.get('[data-plaza-provider="openai"]').trigger('click')
    await wrapper.get('[data-plaza-group="2"]').trigger('click')
    await wrapper.setProps({ response: { description: '', groups: [general, empty] } })
    expect(wrapper.get('[data-plaza-group="2"]').attributes('aria-pressed')).toBe('true')

    await wrapper.setProps({ response: { description: '', groups: [alternate, empty] } })
    expect(wrapper.find('[data-plaza-group="2"]').exists()).toBe(false)
    expect(wrapper.get('[data-plaza-group="all"]').attributes('aria-pressed')).toBe('true')

    await wrapper.setProps({ response: { description: '', groups: [empty] }, authenticated: false })
    expect(wrapper.get('[data-plaza-provider="openai"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-plaza-provider="anthropic"]').attributes('aria-pressed')).toBe('true')
    expect(wrapper.text()).not.toContain('Shared plan')
  })
})
