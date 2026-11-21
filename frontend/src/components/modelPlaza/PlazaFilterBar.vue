<template>
  <div class="min-w-0 border-b border-accent-200 pb-4 dark:border-dark-700">
    <nav :aria-label="t('modelPlaza.filters.typeLabel')" class="flex flex-wrap gap-2">
      <button
        v-for="item in typeOptions"
        :key="item"
        type="button"
        :aria-pressed="type === item"
        class="min-h-11 cursor-pointer rounded-lg px-3 text-sm font-semibold transition-colors"
        :class="type === item
          ? 'bg-primary-600 text-white hover:bg-primary-700'
          : 'bg-white text-accent-700 ring-1 ring-inset ring-accent-200 hover:bg-accent-100 dark:bg-dark-800 dark:text-dark-200 dark:ring-dark-600 dark:hover:bg-dark-700'"
        @click="$emit('update:type', item)"
      >
        {{ t(`modelPlaza.types.${item}`) }}
      </button>
    </nav>

    <div class="mt-4 grid max-w-2xl gap-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,0.7fr)]">
      <label class="block min-w-0 text-sm font-medium text-accent-700 dark:text-dark-200">
        {{ t('modelPlaza.filters.modelLabel') }}
        <span class="relative mt-1 block">
          <Icon
            name="search"
            size="sm"
            class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-accent-600 dark:text-dark-300"
            aria-hidden="true"
          />
          <input
            :value="search"
            type="search"
            :placeholder="t('modelPlaza.filters.searchPlaceholder')"
            class="input min-h-11 min-w-0 pl-10"
            @input="$emit('update:search', ($event.target as HTMLInputElement).value)"
          />
        </span>
      </label>
      <label class="block min-w-0 text-sm font-medium text-accent-700 dark:text-dark-200">
        {{ t('modelPlaza.filters.groupLabel') }}
        <select v-model="selectedGroup" class="input mt-1 min-h-11 w-full min-w-0 truncate">
          <option value="all">{{ t('modelPlaza.filters.allGroups') }}</option>
          <option v-for="group in groups" :key="group.id" :value="group.id">
            {{ group.name }}
          </option>
        </select>
      </label>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { ModelPlazaGroup, PlazaModelType } from '@/api/modelPlaza'
import type { PlazaGroupFilter, PlazaTypeFilter } from './catalog'

const props = defineProps<{
  types: PlazaModelType[]
  groups: ModelPlazaGroup[]
  type: PlazaTypeFilter
  groupId: PlazaGroupFilter
  search: string
}>()

const emit = defineEmits<{
  'update:type': [value: PlazaTypeFilter]
  'update:groupId': [value: PlazaGroupFilter]
  'update:search': [value: string]
}>()

const { t } = useI18n()
const typeOptions = computed<PlazaTypeFilter[]>(() => ['all', ...props.types])
const selectedGroup = computed({
  get: () => props.groupId,
  set: (value: PlazaGroupFilter) => emit('update:groupId', value)
})
</script>
