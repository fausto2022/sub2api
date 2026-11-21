<template>
  <section
    class="w-full rounded-xl border border-accent-200 p-4 dark:border-dark-700"
    data-testid="group-margin-protect"
  >
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h3 class="text-sm font-semibold text-accent-900 dark:text-dark-50">
        {{ t('admin.opsmgmt.groupMargin.title') }}
      </h3>
      <span
        v-if="minMarginSource"
        class="badge badge-gray"
        data-testid="group-margin-source"
      >
        {{ sourceLabel }}
      </span>
    </div>
    <p class="mt-1 text-xs text-accent-600 dark:text-dark-300">
      {{ t('admin.opsmgmt.groupMargin.description') }}
    </p>

    <div class="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
      <div>
        <label class="input-label" for="opsmgmt-group-margin-percent">
          {{ t('admin.opsmgmt.groupMargin.minMargin') }}
        </label>
        <div class="flex items-center gap-2">
          <input
            id="opsmgmt-group-margin-percent"
            v-model.number="percent"
            type="number"
            min="0"
            max="99"
            step="0.01"
            class="input tabular-nums"
            :disabled="inheritGlobal || !groupId || loading"
            data-testid="group-margin-input"
          />
          <span class="text-sm text-accent-600 dark:text-dark-300">%</span>
        </div>
        <p class="input-hint">{{ t('admin.opsmgmt.groupMargin.inputHint') }}</p>
      </div>

      <div class="flex flex-col justify-start gap-1">
        <label class="flex cursor-pointer items-center gap-2 text-sm text-accent-900 dark:text-dark-100">
          <input
            v-model="inheritGlobal"
            type="checkbox"
            class="h-4 w-4 rounded border-accent-300 text-primary-600 focus-visible:ring-2 focus-visible:ring-primary-500 dark:border-dark-600 dark:bg-dark-800"
            :disabled="!groupId || loading"
            data-testid="group-margin-inherit"
          />
          {{ t('admin.opsmgmt.groupMargin.inherit') }}
        </label>
        <p class="text-xs text-accent-600 dark:text-dark-300" data-testid="group-margin-inherit-hint">
          {{ inheritHint }}
        </p>
      </div>
    </div>

    <p v-if="error" class="mt-2 text-xs text-red-700 dark:text-red-300" data-testid="group-margin-error">
      {{ error }}
    </p>
  </section>
</template>

<script setup lang="ts">
/**
 * 分组编辑对话框的「运营管理 · 利润保护」区块（design §2.5.1）。
 *
 * 数据自持：挂载时读 GET /admin/opsmgmt/group-margins（含生效值与来源），
 * 保存由父级在提交分组表单时调用 expose 出去的 save()，避免与分组 PUT 抢请求。
 * 语义：inheritGlobal=true 时提交 basis_points=null（= 继承全局设置）。
 */
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { opsMgmtAPI } from '@/api/admin/opsmgmt'
import { formatBasisPoints } from './format'

const props = withDefaults(
  defineProps<{
    /** 正在编辑的分组 id；新增分组时为 null（此时区块禁用）。 */
    groupId: number | null
    /** 全局最低利润率基点（父级可选传入，缺省时自行读取 settings）。 */
    globalBasisPoints?: number | null
  }>(),
  {
    globalBasisPoints: null
  }
)

const emit = defineEmits<{
  (event: 'saved', basisPoints: number | null): void
}>()

const { t } = useI18n()

const loading = ref(false)
const inheritGlobal = ref(true)
const percent = ref<number>(0)
const savedInherited = ref(true)
const savedPercent = ref<number>(0)
const minMarginSource = ref('')
const globalBP = ref<number | null>(props.globalBasisPoints)
const error = ref('')

const isDirty = computed(
  () => inheritGlobal.value !== savedInherited.value || (!inheritGlobal.value && percent.value !== savedPercent.value)
)

const inheritHint = computed(() => {
  const globalValue = globalBP.value
  return globalValue === null
    ? t('admin.opsmgmt.groupMargin.inheritHintUnknown')
    : t('admin.opsmgmt.groupMargin.inheritHint', { value: formatBasisPoints(globalValue) })
})

const sourceLabel = computed(() => {
  switch (minMarginSource.value) {
    case 'group':
      return t('admin.opsmgmt.groupMargin.source.group')
    case 'upstream':
      return t('admin.opsmgmt.groupMargin.source.upstream')
    case 'global':
      return t('admin.opsmgmt.groupMargin.source.global')
    default:
      return ''
  }
})

function applyRow(row: {
  opsmgmt_min_margin_basis_points: number | null
  effective_min_margin_basis_points: number
  min_margin_source: string
}): void {
  const own = row.opsmgmt_min_margin_basis_points
  const inherited = own === null || own === undefined
  inheritGlobal.value = inherited
  percent.value = inherited
    ? Math.round((row.effective_min_margin_basis_points / 100) * 100) / 100
    : Math.round(((own as number) / 100) * 100) / 100
  savedInherited.value = inheritGlobal.value
  savedPercent.value = percent.value
  minMarginSource.value = inherited ? row.min_margin_source : 'group'
}

async function loadMargin(): Promise<void> {
  error.value = ''
  if (!props.groupId) {
    inheritGlobal.value = true
    percent.value = 0
    savedInherited.value = true
    savedPercent.value = 0
    return
  }
  loading.value = true
  try {
    const [margins, settings] = await Promise.all([
      opsMgmtAPI.listGroupMargins(),
      opsMgmtAPI.getSettings().catch(() => null)
    ])
    if (settings) globalBP.value = settings.minimum_margin_basis_points
    const row = (margins.items || []).find((item) => item.id === props.groupId)
    if (row) applyRow(row)
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : t('admin.opsmgmt.groupMargin.loadFailed')
  } finally {
    loading.value = false
  }
}

/** 提交（幂等）：未改动直接成功；inheritGlobal 时提交 null。 */
async function save(): Promise<boolean> {
  if (!props.groupId) return true
  if (!isDirty.value) return true
  if (!inheritGlobal.value) {
    const value = Number(percent.value)
    if (!Number.isFinite(value) || value < 0 || value >= 100) {
      error.value = t('admin.opsmgmt.groupMargin.rangeError')
      return false
    }
  }
  const basisPoints = inheritGlobal.value ? null : Math.round(Number(percent.value) * 100)
  try {
    const updated = await opsMgmtAPI.setGroupMargin(props.groupId, basisPoints)
    applyRow(updated)
    emit('saved', basisPoints)
    return true
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : t('admin.opsmgmt.groupMargin.saveFailed')
    return false
  }
}

/** 供父级在分组表单提交时重置外部状态。 */
function reload(): Promise<void> {
  return loadMargin()
}

watch(
  () => props.groupId,
  () => {
    void loadMargin()
  },
  { immediate: true }
)

defineExpose({ save, reload })
</script>
