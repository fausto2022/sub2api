<template>
  <nav :aria-label="t('keys.providerLabel')" class="rounded-xl border border-accent-200 bg-white p-2 dark:border-dark-700 dark:bg-dark-800">
    <div class="grid grid-cols-2 gap-1 rounded-lg bg-accent-100 p-1 dark:bg-dark-950 sm:grid-cols-4">
      <button
        v-for="provider in KEY_GROUP_PROVIDERS"
        :key="provider"
        type="button"
        :data-plaza-provider="provider"
        :aria-pressed="provider === activeProvider"
        :disabled="!providers.includes(provider)"
        class="flex min-h-11 min-w-0 cursor-pointer flex-wrap items-center justify-center gap-x-2 rounded-md px-2 py-1 text-sm font-medium transition-colors motion-reduce:transition-none disabled:cursor-not-allowed disabled:opacity-60"
        :class="provider === activeProvider
          ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-200'
          : 'text-accent-700 enabled:hover:bg-accent-200 dark:text-dark-200 dark:enabled:hover:bg-dark-800'"
        @click="$emit('update:provider', provider)"
      >
        <span class="flex shrink-0 items-center gap-1" aria-hidden="true">
          <PlatformIcon v-for="platform in KEY_GROUP_PROVIDER_ICONS[provider]" :key="platform" :platform="platform" size="sm" />
        </span>
        {{ t(`keys.providers.${provider}`) }}
      </button>
    </div>
  </nav>

  <div v-if="groups.length" class="grid min-w-0 items-start gap-4 lg:grid-cols-[minmax(0,13rem)_minmax(0,1fr)]">
    <nav
      :aria-label="t('modelPlaza.filters.groupLabel')"
      class="hidden min-w-0 rounded-xl border border-accent-200 bg-white p-2 dark:border-dark-700 dark:bg-dark-800 lg:block"
    >
      <p class="px-3 pb-1 pt-2 text-sm font-semibold text-accent-700 dark:text-dark-200">
        {{ t('modelPlaza.filters.groupLabel') }}
      </p>
      <ul class="space-y-1">
        <li v-for="option in groupOptions" :key="option.id">
          <button
            type="button"
            :data-plaza-group="option.id"
            :aria-pressed="option.id === groupId"
            class="min-h-11 w-full cursor-pointer rounded-md px-3 py-2 text-left text-sm [overflow-wrap:anywhere] transition-colors motion-reduce:transition-none"
            :class="option.id === groupId
              ? 'bg-primary-50 font-semibold text-primary-700 dark:bg-primary-500/15 dark:text-primary-200'
              : 'text-accent-700 hover:bg-accent-100 dark:text-dark-200 dark:hover:bg-dark-900'"
            @click="$emit('update:groupId', option.id)"
          >
            {{ option.name }}
          </button>
        </li>
      </ul>
    </nav>

    <div class="min-w-0 space-y-4">
      <div class="grid min-w-0 gap-2 rounded-xl border border-accent-200 bg-white p-2.5 dark:border-dark-700 dark:bg-dark-800 sm:grid-cols-2 lg:grid-cols-1">
        <label class="block min-w-0 text-sm font-medium text-accent-700 dark:text-dark-200 lg:hidden">
          <span class="sr-only">{{ t('modelPlaza.filters.groupLabel') }}</span>
          <select v-model="selectedGroup" data-plaza-group-select class="input min-h-11 w-full min-w-0 truncate">
            <option v-for="option in groupOptions" :key="option.id" :value="option.id">{{ option.name }}</option>
          </select>
        </label>
        <label class="block min-w-0 text-sm font-medium text-accent-700 dark:text-dark-200">
          <span class="sr-only">{{ t('modelPlaza.filters.modelLabel') }}</span>
          <span class="relative block">
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
      </div>
      <slot />
    </div>
  </div>
  <slot v-else />
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import type { ModelPlazaGroup } from '@/api/modelPlaza'
import { KEY_GROUP_PROVIDERS, KEY_GROUP_PROVIDER_ICONS, type KeyGroupProvider } from '@/utils/keyGroupProviders'
import type { PlazaGroupFilter } from './catalog'

const props = defineProps<{
  activeProvider: KeyGroupProvider | null
  providers: KeyGroupProvider[]
  groups: ModelPlazaGroup[]
  groupId: PlazaGroupFilter
  search: string
}>()

const emit = defineEmits<{
  'update:provider': [value: KeyGroupProvider]
  'update:groupId': [value: PlazaGroupFilter]
  'update:search': [value: string]
}>()

const { t } = useI18n()
const groupOptions = computed<{ id: PlazaGroupFilter; name: string }[]>(() => [
  { id: 'all', name: t('modelPlaza.filters.allGroups') },
  ...props.groups
])
const selectedGroup = computed({
  get: () => props.groupId,
  set: (value: PlazaGroupFilter) => emit('update:groupId', value)
})
</script>
