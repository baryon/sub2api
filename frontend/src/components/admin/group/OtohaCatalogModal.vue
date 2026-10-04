<template>
  <BaseDialog
    :show="show"
    :title="group ? t('admin.otohaCatalog.titleWithGroup', { name: group.name }) : t('admin.otohaCatalog.title')"
    width="full"
    @close="emit('close')"
  >
    <div v-if="group" class="space-y-4" data-testid="otoha-catalog">
      <p class="text-sm text-gray-600 dark:text-gray-400">{{ t('admin.otohaCatalog.intro') }}</p>

      <div class="flex flex-wrap items-center gap-3 rounded-lg bg-gray-50 px-4 py-2.5 text-sm dark:bg-dark-700">
        <span class="font-medium text-gray-900 dark:text-white">{{ group.name }}</span>
        <span class="text-gray-400">|</span>
        <span class="text-gray-600 dark:text-gray-400">
          {{ t('admin.otohaCatalog.groupRate', { rate: view?.rate_multiplier ?? group.rate_multiplier }) }}
        </span>
        <span class="text-gray-400">|</span>
        <span class="text-gray-600 dark:text-gray-400">
          {{ t('admin.otohaCatalog.shownCount', { count: view?.preview?.models.length ?? 0 }) }}
        </span>
        <button
          type="button"
          class="ml-auto inline-flex items-center gap-1 text-gray-500 hover:text-primary-600 disabled:opacity-50"
          :disabled="loading"
          @click="load"
        >
          <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
          {{ t('admin.otohaCatalog.refresh') }}
        </button>
      </div>

      <form class="flex flex-col gap-2 sm:flex-row" @submit.prevent="openCreate">
        <input
          v-model="newModelId"
          type="text"
          list="otoha-catalog-model-candidates"
          autocomplete="off"
          class="input flex-1"
          :placeholder="t('admin.otohaCatalog.addPlaceholder')"
          data-testid="otoha-catalog-new-model"
        />
        <datalist id="otoha-catalog-model-candidates">
          <option v-for="model in addableCandidates" :key="model" :value="model" />
        </datalist>
        <button type="submit" class="btn btn-primary shrink-0" :disabled="!newModelId.trim() || prefilling">
          <Icon name="plus" size="sm" class="mr-1" />
          {{ prefilling ? t('admin.otohaCatalog.editor.prefilling') : t('admin.otohaCatalog.add') }}
        </button>
      </form>

      <div v-if="loading && !view" class="py-10 text-center text-sm text-gray-500">
        <Icon name="refresh" size="md" class="mx-auto mb-2 animate-spin" />
      </div>
      <div v-else-if="loadFailed" class="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-800 dark:bg-red-900/20 dark:text-red-300">
        {{ t('admin.otohaCatalog.loadFailed') }}
      </div>
      <div
        v-else-if="entries.length === 0"
        class="rounded-lg border border-dashed border-gray-300 px-4 py-8 text-center dark:border-dark-500"
      >
        <p class="font-medium text-gray-700 dark:text-gray-200">{{ t('admin.otohaCatalog.empty') }}</p>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.otohaCatalog.emptyHint') }}</p>
      </div>
      <div v-else class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-600">
        <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-600">
          <thead class="bg-gray-50 dark:bg-dark-700">
            <tr class="text-left text-xs font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400">
              <th class="px-3 py-2">{{ t('admin.otohaCatalog.columns.order') }}</th>
              <th class="px-3 py-2">{{ t('admin.otohaCatalog.columns.model') }}</th>
              <th class="px-3 py-2">{{ t('admin.otohaCatalog.columns.shown') }}</th>
              <th class="px-3 py-2">
                {{ t('admin.otohaCatalog.columns.salePrice') }}
                <span class="block font-normal normal-case">{{ t('admin.otohaCatalog.perMillion') }}</span>
              </th>
              <th class="px-3 py-2">{{ t('admin.otohaCatalog.columns.tier') }}</th>
              <th class="px-3 py-2">{{ t('admin.otohaCatalog.columns.status') }}</th>
              <th class="px-3 py-2 text-right">{{ t('admin.otohaCatalog.columns.actions') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
            <tr v-for="(entry, index) in entries" :key="entry.id" :data-testid="`otoha-catalog-row-${entry.model_id}`">
              <td class="whitespace-nowrap px-3 py-2">
                <div class="flex items-center gap-1">
                  <button
                    type="button"
                    class="rounded p-1 text-gray-500 hover:bg-gray-100 disabled:opacity-30 dark:hover:bg-dark-600"
                    :title="t('admin.otohaCatalog.moveUp')"
                    :aria-label="t('admin.otohaCatalog.moveUp')"
                    :disabled="index === 0 || ordering"
                    @click="move(index, -1)"
                  >
                    <Icon name="arrowUp" size="sm" />
                  </button>
                  <button
                    type="button"
                    class="rounded p-1 text-gray-500 hover:bg-gray-100 disabled:opacity-30 dark:hover:bg-dark-600"
                    :title="t('admin.otohaCatalog.moveDown')"
                    :aria-label="t('admin.otohaCatalog.moveDown')"
                    :disabled="index === entries.length - 1 || ordering"
                    @click="move(index, 1)"
                  >
                    <Icon name="arrowDown" size="sm" />
                  </button>
                </div>
              </td>
              <td class="whitespace-nowrap px-3 py-2">
                <div class="font-medium text-gray-900 dark:text-white">{{ entry.name || entry.model_id }}</div>
                <div class="font-mono text-xs text-gray-500 dark:text-gray-400">{{ entry.model_id }}</div>
              </td>
              <td class="px-3 py-2">
                <Toggle
                  :model-value="entry.enabled"
                  :aria-label="t('admin.otohaCatalog.editor.enabled')"
                  :disabled="busyIds.has(entry.id)"
                  @update:model-value="toggleEnabled(entry, $event)"
                />
              </td>
              <td class="whitespace-nowrap px-3 py-2">
                <template v-if="entry.sale_price">
                  <div class="text-gray-900 dark:text-white">{{ formatPrice(entry.sale_price) }}</div>
                  <div v-if="entry.upstream_price" class="text-xs text-gray-500 dark:text-gray-400">
                    {{ t('admin.otohaCatalog.upstreamPrice', { price: formatPrice(entry.upstream_price) }) }}
                  </div>
                </template>
                <span v-else class="text-gray-400">{{ t('admin.otohaCatalog.noPrice') }}</span>
              </td>
              <td class="whitespace-nowrap px-3 py-2">
                <span v-if="entry.effective_cost">{{ t('admin.otohaCatalog.tier.' + entry.effective_cost) }}</span>
                <span v-else class="text-gray-400">-</span>
              </td>
              <td class="px-3 py-2">
                <span :class="['badge', statusBadge(entry)]">{{ statusLabel(entry) }}</span>
                <p v-if="statusHint(entry)" class="mt-1 max-w-xs text-xs text-gray-500 dark:text-gray-400">
                  {{ statusHint(entry) }}
                </p>
              </td>
              <td class="whitespace-nowrap px-3 py-2 text-right">
                <button
                  type="button"
                  class="inline-flex items-center gap-1 rounded-lg px-2 py-1 text-gray-600 hover:bg-gray-100 hover:text-primary-600 dark:text-gray-300 dark:hover:bg-dark-600"
                  @click="openEdit(entry)"
                >
                  <Icon name="edit" size="sm" />{{ t('common.edit') }}
                </button>
                <button
                  type="button"
                  class="inline-flex items-center gap-1 rounded-lg px-2 py-1 text-gray-600 hover:bg-red-50 hover:text-red-600 dark:text-gray-300 dark:hover:bg-red-900/20"
                  @click="pendingDelete = entry"
                >
                  <Icon name="trash" size="sm" />{{ t('common.delete') }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <section v-if="view" class="rounded-lg border border-gray-200 p-4 dark:border-dark-600" data-testid="otoha-catalog-preview">
        <div class="mb-2 flex flex-wrap items-baseline gap-2">
          <h4 class="text-sm font-medium text-gray-800 dark:text-gray-100">{{ t('admin.otohaCatalog.preview.title') }}</h4>
          <span v-if="view.preview" class="font-mono text-xs text-gray-500">
            {{ t('admin.otohaCatalog.preview.revision', { revision: view.preview.revision }) }}
          </span>
        </div>
        <p v-if="!view.preview" class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.otohaCatalog.preview.none') }}</p>
        <p v-else-if="view.preview.models.length === 0" class="text-sm text-gray-500 dark:text-gray-400">
          {{ t('admin.otohaCatalog.preview.noModels') }}
        </p>
        <ol v-else class="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
          <li
            v-for="model in previewModels"
            :key="model.id"
            class="rounded-md bg-gray-50 px-3 py-2 text-xs text-gray-600 dark:bg-dark-700 dark:text-gray-300"
          >
            <div class="text-sm font-medium text-gray-900 dark:text-white">{{ model.name }}</div>
            <div class="font-mono text-gray-500">{{ model.id }}</div>
            <div class="mt-1">{{ formatPrice(model.price) }} · {{ t('admin.otohaCatalog.tier.' + model.cost) }}</div>
            <div v-if="model.abilities.length">{{ t('admin.otohaCatalog.preview.abilities') }}: {{ model.abilities.join(t('admin.otohaCatalog.preview.separator')) }}</div>
            <div v-if="model.uses.length">{{ t('admin.otohaCatalog.preview.use') }}: {{ model.uses.join(t('admin.otohaCatalog.preview.separator')) }}</div>
          </li>
        </ol>
      </section>
    </div>

    <template #footer>
      <div class="flex justify-end">
        <button type="button" class="btn btn-secondary" @click="emit('close')">{{ t('common.close') }}</button>
      </div>
    </template>
  </BaseDialog>

  <BaseDialog
    :show="editorOpen"
    :title="editingId ? t('admin.otohaCatalog.editor.editTitle', { model: form.model_id }) : t('admin.otohaCatalog.editor.createTitle')"
    width="wide"
    :z-index="60"
    @close="closeEditor"
  >
    <form id="otoha-catalog-editor" class="space-y-6" data-testid="otoha-catalog-editor" @submit.prevent="save">
      <section class="space-y-3">
        <div class="flex items-center justify-between">
          <h4 class="text-sm font-semibold text-gray-800 dark:text-gray-100">{{ t('admin.otohaCatalog.editor.basics') }}</h4>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="prefilling || !form.model_id.trim()" @click="fillFromUpstream">
            <Icon name="sparkles" size="sm" class="mr-1" />
            {{ prefilling ? t('admin.otohaCatalog.editor.prefilling') : t('admin.otohaCatalog.editor.prefill') }}
          </button>
        </div>
        <p v-if="prefillNotice" class="rounded-md bg-blue-50 px-3 py-2 text-xs text-blue-700 dark:bg-blue-900/20 dark:text-blue-300">
          {{ prefillNotice }}
        </p>
        <div class="grid gap-3 sm:grid-cols-2">
          <label class="block">
            <span class="input-label">{{ t('admin.otohaCatalog.editor.modelId') }}</span>
            <input v-model="form.model_id" type="text" class="input w-full font-mono" :readonly="!!editingId" autocomplete="off" />
          </label>
          <label class="block">
            <span class="input-label">{{ t('admin.otohaCatalog.editor.name') }}</span>
            <input v-model="form.name" type="text" class="input w-full" :placeholder="form.model_id" autocomplete="off" />
          </label>
        </div>
        <label class="block">
          <span class="input-label">{{ t('admin.otohaCatalog.editor.description') }}</span>
          <textarea v-model="form.description" rows="2" class="input w-full"></textarea>
        </label>
        <div class="flex items-center gap-3">
          <Toggle v-model="form.enabled" />
          <span class="text-sm text-gray-700 dark:text-gray-300">{{ t('admin.otohaCatalog.editor.enabled') }}</span>
        </div>
      </section>

      <section class="space-y-3">
        <h4 class="text-sm font-semibold text-gray-800 dark:text-gray-100">{{ t('admin.otohaCatalog.editor.abilities') }}</h4>
        <div>
          <span class="input-label">{{ t('admin.otohaCatalog.editor.inputs') }}</span>
          <div class="flex flex-wrap gap-4">
            <label v-for="kind in INPUT_KINDS" :key="kind" class="inline-flex items-center gap-1.5 text-sm">
              <input v-model="form.inputs" type="checkbox" :value="kind" class="rounded border-gray-300" />
              {{ t('admin.otohaCatalog.editor.input.' + kind) }}
            </label>
          </div>
        </div>
        <div class="flex items-center gap-3">
          <Toggle v-model="form.tools" />
          <span class="text-sm text-gray-700 dark:text-gray-300">{{ t('admin.otohaCatalog.editor.tools') }}</span>
        </div>
        <div class="grid gap-3 sm:grid-cols-2">
          <label class="block">
            <span class="input-label">{{ t('admin.otohaCatalog.editor.context') }}</span>
            <input v-model.number="form.context" type="number" min="0" :max="TOKENS_MAX" step="1" class="input w-full" />
          </label>
          <label class="block">
            <span class="input-label">{{ t('admin.otohaCatalog.editor.maxOutput') }}</span>
            <input v-model.number="form.max_output" type="number" min="0" :max="TOKENS_MAX" step="1" class="input w-full" />
          </label>
          <label class="block">
            <span class="input-label">{{ t('admin.otohaCatalog.editor.reasoning') }}</span>
            <input v-model="reasoningText" type="text" class="input w-full" placeholder="low, medium, high" autocomplete="off" />
          </label>
          <label class="block">
            <span class="input-label">{{ t('admin.otohaCatalog.editor.defaultReasoning') }}</span>
            <select v-model="form.default_reasoning" class="input w-full">
              <option value="">{{ t('admin.otohaCatalog.editor.notSet') }}</option>
              <option v-for="level in reasoningLevels" :key="level" :value="level">{{ level }}</option>
            </select>
          </label>
        </div>
      </section>

      <section class="space-y-3">
        <h4 class="text-sm font-semibold text-gray-800 dark:text-gray-100">{{ t('admin.otohaCatalog.editor.profile') }}</h4>
        <div class="grid gap-3 sm:grid-cols-3">
          <label class="block">
            <span class="input-label">{{ t('admin.otohaCatalog.editor.speed') }}</span>
            <select v-model="form.speed" class="input w-full">
              <option value="">{{ t('admin.otohaCatalog.editor.notSet') }}</option>
              <option v-for="speed in SPEEDS" :key="speed" :value="speed">{{ t('admin.otohaCatalog.editor.speeds.' + speed) }}</option>
            </select>
          </label>
          <label class="block">
            <span class="input-label">{{ t('admin.otohaCatalog.editor.complexity') }}</span>
            <select v-model="form.complexity" class="input w-full">
              <option value="">{{ t('admin.otohaCatalog.editor.notSet') }}</option>
              <option v-for="c in COMPLEXITIES" :key="c" :value="c">{{ t('admin.otohaCatalog.editor.complexities.' + c) }}</option>
            </select>
          </label>
          <label class="block">
            <span class="input-label">{{ t('admin.otohaCatalog.editor.profileSource') }}</span>
            <select v-model="form.profile_source" class="input w-full">
              <option value="">{{ t('admin.otohaCatalog.editor.notSet') }}</option>
              <option v-for="source in PROFILE_SOURCES" :key="source" :value="source">
                {{ t('admin.otohaCatalog.editor.profileSources.' + source) }}
              </option>
            </select>
          </label>
        </div>
        <div>
          <span class="input-label">{{ t('admin.otohaCatalog.editor.roles') }}</span>
          <div class="flex flex-wrap gap-4">
            <label v-for="role in ROLES" :key="role" class="inline-flex items-center gap-1.5 text-sm">
              <input v-model="form.roles" type="checkbox" :value="role" class="rounded border-gray-300" />
              {{ t('admin.otohaCatalog.editor.roleOptions.' + role) }}
            </label>
          </div>
        </div>
        <div>
          <span class="input-label">{{ t('admin.otohaCatalog.editor.strengths') }}</span>
          <div class="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
            <label v-for="domain in DOMAINS" :key="domain" class="block">
              <span class="text-xs text-gray-600 dark:text-gray-400">{{ t('admin.otohaCatalog.editor.domains.' + domain) }}</span>
              <select v-model="strengths[domain]" class="input w-full">
                <option value="">{{ t('admin.otohaCatalog.editor.levels.unknown') }}</option>
                <option v-for="level in LEVELS" :key="level" :value="level">{{ t('admin.otohaCatalog.editor.levels.' + level) }}</option>
              </select>
            </label>
          </div>
        </div>
        <div>
          <span class="input-label">{{ t('admin.otohaCatalog.editor.use') }}</span>
          <div class="flex flex-wrap gap-4">
            <label v-for="use in USES" :key="use" class="inline-flex items-center gap-1.5 text-sm">
              <input v-model="form.use" type="checkbox" :value="use" class="rounded border-gray-300" />
              {{ t('admin.otohaCatalog.editor.uses.' + use) }}
            </label>
          </div>
          <p class="input-hint">{{ t('admin.otohaCatalog.editor.useHint') }}</p>
        </div>
      </section>

      <section class="space-y-3">
        <h4 class="text-sm font-semibold text-gray-800 dark:text-gray-100">{{ t('admin.otohaCatalog.editor.price') }}</h4>
        <div class="rounded-md bg-gray-50 px-3 py-2 text-sm text-gray-700 dark:bg-dark-700 dark:text-gray-300">
          <div v-if="editorUpstream">{{ t('admin.otohaCatalog.editor.upstreamNow', { price: formatPrice(editorUpstream) }) }}</div>
          <div v-else>{{ t('admin.otohaCatalog.editor.noUpstreamPrice') }}</div>
          <div v-if="editorSale" class="font-medium">{{ t('admin.otohaCatalog.editor.saleNow', { price: formatPrice(editorSale) }) }}</div>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.otohaCatalog.editor.priceHint') }}</p>
        </div>
        <label class="block sm:w-1/3">
          <span class="input-label">{{ t('admin.otohaCatalog.editor.tier') }}</span>
          <select v-model="form.cost_tier" class="input w-full">
            <option value="">{{ t('admin.otohaCatalog.tier.auto') }}</option>
            <option v-for="tier in TIERS" :key="tier" :value="tier">{{ t('admin.otohaCatalog.tier.' + tier) }}</option>
          </select>
        </label>
      </section>

      <p v-if="formError" class="input-error-text" role="alert">{{ formError }}</p>
    </form>

    <template #footer>
      <div class="flex justify-end gap-2">
        <button type="button" class="btn btn-secondary" @click="closeEditor">{{ t('common.cancel') }}</button>
        <button type="submit" form="otoha-catalog-editor" class="btn btn-primary" :disabled="saving">
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </div>
    </template>
  </BaseDialog>

  <ConfirmDialog
    :show="pendingDelete !== null"
    :title="t('admin.otohaCatalog.deleteTitle')"
    :message="t('admin.otohaCatalog.deleteMessage', { model: pendingDelete?.name || pendingDelete?.model_id || '' })"
    :confirm-text="t('common.delete')"
    danger
    @confirm="confirmDelete"
    @cancel="pendingDelete = null"
  />
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type {
  OtohaCatalogAdminEntry,
  OtohaCatalogAdminView,
  OtohaCatalogEntryInput,
  OtohaModelPrice
} from '@/api/admin/otohaCatalog'
import type { AdminGroup } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'
import { extractApiErrorCode, extractApiErrorMessage } from '@/utils/apiError'

const INPUT_KINDS = ['text', 'image', 'audio', 'video', 'file'] as const
const SPEEDS = ['fast', 'standard', 'slow'] as const
const COMPLEXITIES = ['simple', 'medium', 'complex'] as const
const ROLES = ['lead', 'execute'] as const
const DOMAINS = ['planning', 'writing', 'coding', 'research', 'data', 'summarize', 'vision', 'translation'] as const
const LEVELS = ['strong', 'usable', 'avoid'] as const
const USES = ['default', 'writing', 'planning', 'fast', 'summarize', 'deep', 'coding', 'web'] as const
const PROFILE_SOURCES = ['vendor', 'evaluation', 'admin'] as const
const TIERS = ['low', 'standard', 'high'] as const
const TOKENS_MAX = 100_000_000
const MODEL_ID_MAX = 200
const NAME_MAX = 200
const DESCRIPTION_MAX = 2000
const REASONING_LEVEL = /^[a-z][a-z0-9_-]{0,31}$/

const props = defineProps<{
  show: boolean
  group: AdminGroup | null
}>()

const emit = defineEmits<{
  close: []
}>()

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(false)
const loadFailed = ref(false)
const view = ref<OtohaCatalogAdminView | null>(null)
const candidates = ref<string[]>([])
const newModelId = ref('')
const ordering = ref(false)
const busyIds = reactive(new Set<number>())
const pendingDelete = ref<OtohaCatalogAdminEntry | null>(null)

const editorOpen = ref(false)
const editingId = ref<number | null>(null)
const saving = ref(false)
const prefilling = ref(false)
const prefillNotice = ref('')
const formError = ref('')
const form = reactive<OtohaCatalogEntryInput>(emptyInput(''))
const strengths = reactive<Record<string, string>>({})
const reasoningText = ref('')
const editorUpstream = ref<OtohaModelPrice | null>(null)

const entries = computed(() => view.value?.entries ?? [])

const addableCandidates = computed(() => {
  const present = new Set(entries.value.map((e) => e.model_id))
  return candidates.value.filter((model) => !model.includes('*') && !present.has(model))
})

const reasoningLevels = computed(() => parseList(reasoningText.value))

const previewModels = computed(() =>
  (view.value?.preview?.models ?? []).map((model) => {
    const abilities: string[] = []
    if (model.images) abilities.push(t('admin.otohaCatalog.preview.image'))
    if (model.tools) abilities.push(t('admin.otohaCatalog.preview.tools'))
    if (typeof model.context === 'number' && model.context > 0) {
      abilities.push(t('admin.otohaCatalog.preview.context', { tokens: formatTokens(model.context) }))
    }
    const uses = Array.isArray(model.use)
      ? (model.use as string[]).map((use) => t('admin.otohaCatalog.editor.uses.' + use))
      : []
    return {
      id: model.id,
      name: model.name,
      price: model.price as OtohaModelPrice,
      cost: String(model.cost ?? ''),
      abilities,
      uses
    }
  })
)

// The price the app shows: what billing charges, the model's price in the group times the group's rate.
const editorSale = computed<OtohaModelPrice | null>(() => {
  const upstream = editorUpstream.value
  if (!upstream) return null
  const rate = view.value?.rate_multiplier ?? props.group?.rate_multiplier ?? 1
  const scale = (v: number) => Math.round(v * rate * 1e6) / 1e6
  return {
    currency: 'USD',
    per: '1M tokens',
    input: scale(upstream.input),
    output: scale(upstream.output),
    ...(upstream.cachedInput !== undefined ? { cachedInput: scale(upstream.cachedInput) } : {})
  }
})

watch(
  () => [props.show, props.group?.id] as const,
  ([show]) => {
    if (show && props.group) {
      view.value = null
      newModelId.value = ''
      load()
      loadCandidates()
    }
  },
  { immediate: true }
)

function emptyInput(modelId: string): OtohaCatalogEntryInput {
  return {
    model_id: modelId,
    name: '',
    description: '',
    enabled: true,
    inputs: ['text'],
    tools: false,
    context: 0,
    max_output: 0,
    reasoning: [],
    default_reasoning: '',
    speed: '',
    strengths: {},
    complexity: '',
    roles: [],
    use: [],
    profile_source: '',
    cost_tier: ''
  }
}

async function load() {
  if (!props.group) return
  loading.value = true
  loadFailed.value = false
  try {
    view.value = await adminAPI.otohaCatalog.getCatalog(props.group.id)
  } catch (error) {
    loadFailed.value = true
    console.error('Failed to load the Otoha catalog:', error)
  } finally {
    loading.value = false
  }
}

async function loadCandidates() {
  if (!props.group) return
  try {
    candidates.value = await adminAPI.groups.getModelAllowlistCandidates(props.group.id)
  } catch {
    candidates.value = []
  }
}

function parseList(text: string): string[] {
  const seen = new Set<string>()
  for (const part of text.split(/[,，\s]+/)) {
    const value = part.trim().toLowerCase()
    if (value) seen.add(value)
  }
  return [...seen]
}

function toNumber(value: string | number | null | undefined): number | null {
  if (value === null || value === undefined || value === '') return null
  const n = typeof value === 'number' ? value : Number(value)
  return Number.isFinite(n) ? n : null
}

function formatAmount(value: number): string {
  return '$' + String(Math.round(value * 1e6) / 1e6)
}

function formatPrice(price: OtohaModelPrice | null | undefined): string {
  if (!price) return t('admin.otohaCatalog.noPrice')
  return `${formatAmount(price.input)} / ${formatAmount(price.output)}`
}

function formatTokens(tokens: number): string {
  if (tokens >= 1_000_000) return `${Math.round(tokens / 100_000) / 10}M`
  if (tokens >= 1_000) return `${Math.round(tokens / 1_000)}K`
  return String(tokens)
}

function statusLabel(entry: OtohaCatalogAdminEntry): string {
  return entry.in_catalog ? t('admin.otohaCatalog.status.shown') : t('admin.otohaCatalog.status.' + entry.problem)
}

function statusBadge(entry: OtohaCatalogAdminEntry): string {
  if (entry.in_catalog) return 'badge-success'
  if (entry.problem === 'disabled') return 'badge-gray'
  return 'badge-warning'
}

function statusHint(entry: OtohaCatalogAdminEntry): string {
  if (entry.problem === 'not_allowed' || entry.problem === 'no_account' || entry.problem === 'no_price') {
    return t('admin.otohaCatalog.statusHint.' + entry.problem)
  }
  return ''
}

function inputFromEntry(entry: OtohaCatalogEntryInput): OtohaCatalogEntryInput {
  return {
    model_id: entry.model_id,
    name: entry.name ?? '',
    description: entry.description ?? '',
    enabled: entry.enabled,
    inputs: [...(entry.inputs ?? [])],
    tools: entry.tools,
    context: entry.context ?? 0,
    max_output: entry.max_output ?? 0,
    reasoning: [...(entry.reasoning ?? [])],
    default_reasoning: entry.default_reasoning ?? '',
    speed: entry.speed ?? '',
    strengths: { ...(entry.strengths ?? {}) },
    complexity: entry.complexity ?? '',
    roles: [...(entry.roles ?? [])],
    use: [...(entry.use ?? [])],
    profile_source: entry.profile_source ?? '',
    cost_tier: entry.cost_tier ?? ''
  }
}

function fillForm(input: OtohaCatalogEntryInput) {
  Object.assign(form, input)
  for (const domain of DOMAINS) {
    strengths[domain] = input.strengths[domain] ?? ''
  }
  reasoningText.value = input.reasoning.join(', ')
}

// Upstream values replace the form's abilities; what upstream does not know keeps the admin's value.
function applyUpstreamAbilities(source: OtohaCatalogEntryInput) {
  if (source.name && source.name !== source.model_id) form.name = source.name
  if (source.description) form.description = source.description
  form.inputs = [...source.inputs]
  form.tools = source.tools
  if (source.context > 0) form.context = source.context
  if (source.max_output > 0) form.max_output = source.max_output
  if (source.reasoning.length > 0) {
    reasoningText.value = source.reasoning.join(', ')
    form.default_reasoning = source.default_reasoning
  }
}

async function openCreate() {
  if (!props.group) return
  const modelId = newModelId.value.trim()
  if (!modelId) return
  const existing = entries.value.find((e) => e.model_id === modelId)
  if (existing) {
    newModelId.value = ''
    openEdit(existing)
    return
  }
  prefilling.value = true
  formError.value = ''
  prefillNotice.value = ''
  try {
    const draft = await adminAPI.otohaCatalog.prefill(props.group.id, modelId)
    fillForm(inputFromEntry(draft.entry))
    editorUpstream.value = draft.upstream_price
    prefillNotice.value = draft.metadata_found
      ? t('admin.otohaCatalog.editor.prefillDone')
      : t('admin.otohaCatalog.editor.prefillMissing')
  } catch (error) {
    console.error('Failed to prefill an Otoha catalog entry:', error)
    fillForm(emptyInput(modelId))
    editorUpstream.value = null
    prefillNotice.value = t('admin.otohaCatalog.editor.prefillFailed')
  } finally {
    prefilling.value = false
  }
  editingId.value = null
  editorOpen.value = true
}

function openEdit(entry: OtohaCatalogAdminEntry) {
  formError.value = ''
  prefillNotice.value = ''
  fillForm(inputFromEntry(entry))
  editorUpstream.value = entry.upstream_price
  editingId.value = entry.id
  editorOpen.value = true
}

function closeEditor() {
  editorOpen.value = false
  editingId.value = null
}

async function fillFromUpstream() {
  if (!props.group || !form.model_id.trim()) return
  prefilling.value = true
  prefillNotice.value = ''
  try {
    const draft = await adminAPI.otohaCatalog.prefill(props.group.id, form.model_id.trim())
    editorUpstream.value = draft.upstream_price
    if (draft.metadata_found) {
      applyUpstreamAbilities(inputFromEntry(draft.entry))
      prefillNotice.value = t('admin.otohaCatalog.editor.prefillDone')
    } else {
      prefillNotice.value = t('admin.otohaCatalog.editor.prefillMissing')
    }
  } catch (error) {
    console.error('Failed to read upstream model information:', error)
    prefillNotice.value = t('admin.otohaCatalog.editor.prefillFailed')
  } finally {
    prefilling.value = false
  }
}

function wholeTokens(value: unknown): number | null {
  const n = toNumber(value as string | number | null | undefined) ?? 0
  return Number.isInteger(n) && n >= 0 && n <= TOKENS_MAX ? n : null
}

function buildInput(): OtohaCatalogEntryInput | null {
  const fail = (message: string) => {
    formError.value = message
    return null
  }
  const modelId = form.model_id.trim()
  if (!modelId) return fail(t('admin.otohaCatalog.editor.required'))
  if (/\s/.test(modelId) || modelId.length > MODEL_ID_MAX) return fail(t('admin.otohaCatalog.editor.modelIdInvalid'))
  const name = form.name.trim()
  const description = form.description.trim()
  if (name.length > NAME_MAX || description.length > DESCRIPTION_MAX) {
    return fail(t('admin.otohaCatalog.editor.tooLong'))
  }
  const context = wholeTokens(form.context)
  const maxOutput = wholeTokens(form.max_output)
  if (context === null || maxOutput === null) return fail(t('admin.otohaCatalog.editor.tokensInvalid'))
  const reasoning = reasoningLevels.value
  if (reasoning.some((level) => !REASONING_LEVEL.test(level))) {
    return fail(t('admin.otohaCatalog.editor.reasoningInvalid'))
  }
  if (form.default_reasoning && !reasoning.includes(form.default_reasoning)) {
    return fail(t('admin.otohaCatalog.editor.defaultNotInList'))
  }
  const chosenStrengths: Record<string, string> = {}
  for (const domain of DOMAINS) {
    if (strengths[domain]) chosenStrengths[domain] = strengths[domain]
  }
  return {
    ...form,
    model_id: modelId,
    name,
    description,
    context,
    max_output: maxOutput,
    reasoning,
    strengths: chosenStrengths
  }
}

// Plain words for the admin; the server's own message goes to the console.
function saveErrorMessage(error: unknown): string {
  console.error('Failed to save an Otoha catalog entry:', extractApiErrorMessage(error, 'unknown error'))
  switch (extractApiErrorCode(error)) {
    case 'OTOHA_CATALOG_ENTRY_EXISTS':
      return t('admin.otohaCatalog.exists')
    case 'OTOHA_CATALOG_ENTRY_INVALID':
      return t('admin.otohaCatalog.invalid')
    case 'OTOHA_CATALOG_ENTRY_NOT_FOUND':
      return t('admin.otohaCatalog.gone')
    default:
      return t('admin.otohaCatalog.saveFailed')
  }
}

async function save() {
  if (!props.group) return
  formError.value = ''
  const input = buildInput()
  if (!input) return
  saving.value = true
  try {
    if (editingId.value) {
      await adminAPI.otohaCatalog.updateEntry(props.group.id, editingId.value, input)
    } else {
      await adminAPI.otohaCatalog.createEntry(props.group.id, input)
      newModelId.value = ''
    }
    appStore.showSuccess(t('admin.otohaCatalog.saved'))
    closeEditor()
    await load()
  } catch (error) {
    formError.value = saveErrorMessage(error)
  } finally {
    saving.value = false
  }
}

async function toggleEnabled(entry: OtohaCatalogAdminEntry, enabled: boolean) {
  if (!props.group) return
  busyIds.add(entry.id)
  try {
    await adminAPI.otohaCatalog.updateEntry(props.group.id, entry.id, { ...inputFromEntry(entry), enabled })
    await load()
  } catch (error) {
    appStore.showError(saveErrorMessage(error))
  } finally {
    busyIds.delete(entry.id)
  }
}

async function move(index: number, step: number) {
  if (!props.group || !view.value) return
  const target = index + step
  const list = [...view.value.entries]
  if (target < 0 || target >= list.length) return
  ;[list[index], list[target]] = [list[target], list[index]]
  view.value = { ...view.value, entries: list }
  ordering.value = true
  try {
    await adminAPI.otohaCatalog.reorder(props.group.id, list.map((e) => e.id))
    await load()
  } catch (error) {
    console.error('Failed to save the Otoha catalog order:', error)
    appStore.showError(t('admin.otohaCatalog.orderFailed'))
    await load()
  } finally {
    ordering.value = false
  }
}

async function confirmDelete() {
  const entry = pendingDelete.value
  pendingDelete.value = null
  if (!props.group || !entry) return
  try {
    await adminAPI.otohaCatalog.deleteEntry(props.group.id, entry.id)
    appStore.showSuccess(t('admin.otohaCatalog.deleted'))
    await load()
  } catch (error) {
    console.error('Failed to remove an Otoha catalog entry:', error)
    appStore.showError(t('admin.otohaCatalog.deleteFailed'))
  }
}
</script>
