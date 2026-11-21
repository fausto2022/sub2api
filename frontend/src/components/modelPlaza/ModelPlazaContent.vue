<template>
  <div class="mx-auto min-w-0 max-w-7xl space-y-4">
    <header v-if="!embedded || !authenticated" class="flex flex-wrap items-end justify-between gap-x-4 gap-y-2">
      <div v-if="!embedded">
        <h1 class="text-2xl font-bold tracking-tight text-accent-900 dark:text-dark-50">{{ t('modelPlaza.title') }}</h1>
        <p class="mt-1 text-sm text-accent-600 dark:text-dark-300">{{ t('modelPlaza.description') }}</p>
      </div>
      <p v-if="!authenticated" class="flex min-w-0 items-start gap-1.5 text-xs text-accent-600 dark:text-dark-300">
        <Icon name="infoCircle" size="xs" class="mt-0.5 shrink-0" aria-hidden="true" />
        {{ t('modelPlaza.anonymousHint') }}
      </p>
    </header>

    <div
      v-if="loading"
      role="status"
      class="flex min-h-32 items-center justify-center gap-2 text-sm text-accent-600 dark:text-dark-300"
    >
      <span aria-hidden="true" class="h-5 w-5 animate-spin rounded-full border-2 border-primary-600/25 border-t-primary-600 motion-reduce:animate-none dark:border-primary-400/25 dark:border-t-primary-400"></span>
      {{ t('modelPlaza.loading') }}
    </div>
    <div
      v-else-if="error"
      class="rounded-xl border border-red-200 bg-red-50 px-4 py-6 text-center text-sm text-red-700 dark:border-red-500/30 dark:bg-red-500/10 dark:text-red-300"
    >
      <p role="alert">{{ t('modelPlaza.loadFailed') }}</p>
      <button type="button" class="btn-secondary mt-3 min-h-11" @click="$emit('retry')">
        {{ t('modelPlaza.retry') }}
      </button>
    </div>
    <template v-else>
      <div
        v-if="descriptionHtml"
        class="plaza-description min-w-0 overflow-x-auto rounded-xl border border-accent-200 bg-white px-4 py-3 text-sm dark:border-dark-700 dark:bg-dark-800"
        v-html="descriptionHtml"
      ></div>
      <PlazaFilterBar
        v-if="catalog.groups.length"
        v-model:type="selectedType"
        v-model:group-id="selectedGroupId"
        v-model:search="searchQuery"
        :types="catalog.types"
        :groups="catalog.groups"
      />

      <div
        v-if="!catalog.types.length || results.noMatch"
        class="rounded-xl border border-dashed border-accent-300 px-4 py-6 text-center text-sm text-accent-600 dark:border-dark-600 dark:text-dark-300"
      >
        <p role="status">{{ t(results.noMatch ? 'modelPlaza.noSearchResult' : 'modelPlaza.empty') }}</p>
        <button v-if="results.noMatch" type="button" class="btn-secondary mt-3 min-h-11" @click="clearFilters">
          {{ t('modelPlaza.clearFilters') }}
        </button>
      </div>

      <div v-if="results.sections.length" class="space-y-7">
        <section v-for="section in results.sections" :key="section.type" class="min-w-0 space-y-3">
          <h2 class="border-l-2 border-primary-500 pl-2.5 text-xl font-semibold text-accent-900 dark:border-primary-400 dark:text-dark-50">
            {{ t(`modelPlaza.types.${section.type}`) }}
          </h2>
          <PlazaGroupSection
            v-for="entry in section.groups"
            :key="entry.group.id"
            :group="entry.group"
            :models="entry.models"
          />
        </section>
      </div>

      <section v-if="results.emptyGroups.length" class="space-y-3">
        <h2 class="border-l-2 border-accent-300 pl-2.5 text-lg font-semibold text-accent-900 dark:border-dark-600 dark:text-dark-50">
          {{ t('modelPlaza.emptyGroups') }}
        </h2>
        <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
          <PlazaGroupSection v-for="group in results.emptyGroups" :key="group.id" :group="group" :models="[]" />
        </div>
      </section>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import Icon from '@/components/icons/Icon.vue'
import PlazaFilterBar from './PlazaFilterBar.vue'
import PlazaGroupSection from './PlazaGroupSection.vue'
import type { ModelPlazaResponse } from '@/api/modelPlaza'
import { buildPlazaCatalog, filterPlazaCatalog, type PlazaGroupFilter, type PlazaTypeFilter } from './catalog'

const props = defineProps<{
  response: ModelPlazaResponse | null
  loading: boolean
  error?: boolean
  authenticated: boolean
  embedded?: boolean
}>()

defineEmits<{ retry: [] }>()

const { t } = useI18n()
const selectedType = ref<PlazaTypeFilter>('all')
const selectedGroupId = ref<PlazaGroupFilter>('all')
const searchQuery = ref('')

function clearFilters() {
  selectedType.value = 'all'
  selectedGroupId.value = 'all'
  searchQuery.value = ''
}

const catalog = computed(() => buildPlazaCatalog(props.response?.groups ?? []))
const results = computed(() =>
  filterPlazaCatalog(catalog.value, selectedType.value, selectedGroupId.value, searchQuery.value)
)

watch(catalog, ({ groups, types }) => {
  if (selectedType.value !== 'all' && !types.includes(selectedType.value)) selectedType.value = 'all'
  if (selectedGroupId.value !== 'all' && !groups.some(group => group.id === selectedGroupId.value)) {
    selectedGroupId.value = 'all'
  }
})

const descriptionHtml = computed(() => {
  const markdown = props.response?.description?.trim()
  return markdown ? DOMPurify.sanitize(marked.parse(markdown, { async: false })) : ''
})
</script>

<style scoped>
.plaza-description {
  line-height: 1.7;
  overflow-wrap: anywhere;
}

.plaza-description :deep(h1),
.plaza-description :deep(h2),
.plaza-description :deep(h3) {
  @apply mb-2 mt-3 font-semibold text-accent-900 first:mt-0 dark:text-dark-50;
}

.plaza-description :deep(p),
.plaza-description :deep(li) {
  @apply mb-2 text-accent-700 last:mb-0 dark:text-dark-200;
}

.plaza-description :deep(a) {
  @apply text-primary-600 underline underline-offset-4 hover:text-primary-700 dark:text-primary-300;
}

.plaza-description :deep(ul) {
  @apply mb-2 list-disc pl-5;
}

.plaza-description :deep(ol) {
  @apply mb-2 list-decimal pl-5;
}

.plaza-description :deep(code) {
  @apply rounded bg-accent-100 px-1.5 py-0.5 font-mono text-xs dark:bg-dark-900;
}

.plaza-description :deep(pre) {
  @apply whitespace-pre-wrap;
}

.plaza-description :deep(blockquote) {
  @apply my-2 border-l-4 border-accent-300 pl-3 text-accent-600 dark:border-dark-600 dark:text-dark-300;
}
</style>
