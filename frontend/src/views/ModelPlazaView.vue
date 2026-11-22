<template>
  <AppLayout v-if="isEmbedded" plain>
    <ModelPlazaContent v-bind="contentProps" embedded @retry="loadCatalog" />
  </AppLayout>

  <div v-else class="min-h-screen bg-[var(--ui-canvas)]">
    <PlazaNavBar :authenticated="authenticated" />
    <main class="mx-auto min-w-0 max-w-[76rem] px-4 py-6 sm:px-6 lg:px-8">
      <ModelPlazaContent v-bind="contentProps" @retry="loadCatalog" />
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import PlazaNavBar from '@/components/modelPlaza/PlazaNavBar.vue'
import ModelPlazaContent from '@/components/modelPlaza/ModelPlazaContent.vue'
import { getModelPlaza, type ModelPlazaResponse } from '@/api/modelPlaza'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'

const route = useRoute()
const appStore = useAppStore()
const authStore = useAuthStore()

// Browser storage may be cleared by logout in a different tab before Pinia changes here.
const storageLoggedOut = ref(false)
const authenticated = computed(() => authStore.isAuthenticated && !storageLoggedOut.value)
const isEmbedded = computed(() => route.query.embedded === '1' && authenticated.value)

const data = ref<ModelPlazaResponse | null>(null)
const loading = ref(true)
const loadFailed = ref(false)
const contentProps = computed(() => ({
  response: data.value,
  loading: loading.value,
  error: loadFailed.value,
  authenticated: authenticated.value
}))

let request: AbortController | undefined

async function loadCatalog() {
  request?.abort()
  const controller = new AbortController()
  request = controller
  data.value = null
  loading.value = true
  loadFailed.value = false

  // Pinia changes before the auth store finishes persisting/clearing the token used by Axios.
  await Promise.resolve()
  if (controller.signal.aborted) return

  try {
    const response = await getModelPlaza({ signal: controller.signal })
    if (!controller.signal.aborted) data.value = response
  } catch {
    if (!controller.signal.aborted) loadFailed.value = true
  } finally {
    if (!controller.signal.aborted) loading.value = false
  }
}

watch([() => authStore.token, () => authStore.user?.id], () => {
  storageLoggedOut.value = false
  void loadCatalog()
}, { immediate: true, flush: 'sync' })

function onSessionStorageChange(event: StorageEvent) {
  if (event.storageArea !== window.localStorage || (event.key !== null && event.key !== 'auth_token')) return
  storageLoggedOut.value = !window.localStorage.getItem('auth_token')
  void loadCatalog()
}

onMounted(() => {
  void appStore.fetchPublicSettings()
  window.addEventListener('storage', onSessionStorageChange)
})

onScopeDispose(() => {
  request?.abort()
  window.removeEventListener('storage', onSessionStorageChange)
})
</script>
