import type { ModelPlazaGroup } from '@/api/modelPlaza'
import type { GroupPlatform } from '@/types'
import { getKeyGroupProvider, KEY_GROUP_PROVIDERS, type KeyGroupProvider } from '@/utils/keyGroupProviders'

export type PlazaGroupFilter = number | 'all'

/** Index only groups returned by the plaza API, including groups without models. */
export function buildPlazaCatalog(groups: readonly ModelPlazaGroup[]) {
  const groupRate = (group: ModelPlazaGroup) => group.user_rate_multiplier ?? group.rate_multiplier
  const sortedGroups = [...groups].sort(
    (a, b) => groupRate(a) - groupRate(b) || a.name.localeCompare(b.name) || a.id - b.id
  )
  const groupsByProvider = new Map<KeyGroupProvider, ModelPlazaGroup[]>()
  for (const group of sortedGroups) {
    const provider = getKeyGroupProvider(group.platform as GroupPlatform)
    const entries = groupsByProvider.get(provider) ?? []
    entries.push(group)
    groupsByProvider.set(provider, entries)
  }

  return { groupsByProvider, providers: KEY_GROUP_PROVIDERS.filter(provider => groupsByProvider.has(provider)) }
}

type PlazaCatalog = ReturnType<typeof buildPlazaCatalog>

export function resolvePlazaSelection(
  catalog: PlazaCatalog,
  provider: KeyGroupProvider | null,
  groupId: PlazaGroupFilter
): { provider: KeyGroupProvider | null; groupId: PlazaGroupFilter } {
  const groups = provider ? catalog.groupsByProvider.get(provider) : undefined
  if (!groups) return { provider: catalog.providers[0] ?? null, groupId: 'all' }
  return { provider, groupId: groups.some(group => group.id === groupId) ? groupId : 'all' }
}

export function filterPlazaCatalog(
  groups: readonly ModelPlazaGroup[],
  groupId: PlazaGroupFilter,
  search: string
) {
  const query = search.trim().toLowerCase()
  const selectedGroups = groupId === 'all' ? groups : groups.filter(group => group.id === groupId)
  const emptyGroups = selectedGroups.filter(group => !group.models?.length)
  const entries = selectedGroups.flatMap(group => {
    const models = group.models ?? []
    const matched = query ? models.filter(model => model.name.toLowerCase().includes(query)) : models
    return matched.length ? [{ group, models: matched }] : []
  })

  // When the scope itself is empty, keep the empty-group state even with a search term.
  const hasModels = emptyGroups.length < selectedGroups.length
  return {
    entries,
    emptyGroups: query && hasModels ? [] : emptyGroups,
    noMatch: query !== '' && hasModels && !entries.length
  }
}
