import { PLAZA_MODEL_TYPES, type ModelPlazaGroup, type PlazaModel, type PlazaModelType } from '@/api/modelPlaza'

export type PlazaTypeFilter = PlazaModelType | 'all'
export type PlazaGroupFilter = number | 'all'

interface CatalogGroup {
  group: ModelPlazaGroup
  models: PlazaModel[]
}

/** Index the authorized response before applying filters; type navigation stays stable while filtering. */
export function buildPlazaCatalog(groups: readonly ModelPlazaGroup[]) {
  const groupRate = (group: ModelPlazaGroup) => group.user_rate_multiplier ?? group.rate_multiplier
  const sortedGroups = [...groups].sort(
    (a, b) => groupRate(a) - groupRate(b) || a.name.localeCompare(b.name) || a.id - b.id
  )
  const typeGroups = new Map<PlazaModelType, CatalogGroup[]>()
  const emptyGroups: ModelPlazaGroup[] = []

  for (const group of sortedGroups) {
    // Older responses can encode an empty model list as null.
    const models = group.models ?? []
    if (!models.length) {
      emptyGroups.push(group)
      continue
    }
    const modelsByType = new Map<PlazaModelType, PlazaModel[]>()
    for (const model of models) {
      const value = model.model_type
      const type = value && PLAZA_MODEL_TYPES.includes(value) ? value : 'other'
      const typedModels = modelsByType.get(type) ?? []
      typedModels.push(model)
      modelsByType.set(type, typedModels)
    }
    for (const [type, typedModels] of modelsByType) {
      const entries = typeGroups.get(type) ?? []
      entries.push({ group, models: typedModels })
      typeGroups.set(type, entries)
    }
  }

  const types = PLAZA_MODEL_TYPES.filter(type => typeGroups.has(type))
  return { groups: sortedGroups, emptyGroups, types, typeGroups }
}

export function filterPlazaCatalog(
  catalog: ReturnType<typeof buildPlazaCatalog>,
  selectedType: PlazaTypeFilter,
  selectedGroup: PlazaGroupFilter,
  search: string
) {
  const query = search.trim().toLowerCase()
  const matchesGroup = (group: ModelPlazaGroup) => selectedGroup === 'all' || group.id === selectedGroup
  const sections = catalog.types.flatMap(type => {
    if (selectedType !== 'all' && selectedType !== type) return []
    const groups = (catalog.typeGroups.get(type) ?? []).flatMap(({ group, models }) => {
      if (!matchesGroup(group)) return []
      const matched = query ? models.filter(model => model.name.toLowerCase().includes(query)) : models
      return matched.length ? [{ group, models: matched }] : []
    })
    return groups.length ? [{ type, groups }] : []
  })

  const emptyGroups = catalog.emptyGroups.filter(matchesGroup)
  const noMatch = !sections.length && catalog.types.length > 0 &&
    (selectedGroup === 'all' || emptyGroups.length === 0 || query !== '')

  return { sections, emptyGroups, noMatch }
}
