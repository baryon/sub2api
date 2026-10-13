<template>
  <section class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-600" data-testid="otoha-catalog-images">
    <div>
      <h4 class="text-sm font-semibold text-gray-800 dark:text-gray-100">{{ t('admin.otohaCatalog.images.title') }}</h4>
      <p class="mt-1 text-sm text-gray-600 dark:text-gray-400">{{ t('admin.otohaCatalog.images.intro') }}</p>
    </div>

    <div
      v-if="showSwitchNotice"
      class="flex flex-col gap-3 rounded-lg border border-amber-300 bg-amber-50 p-3 sm:flex-row sm:items-center sm:justify-between dark:border-amber-700 dark:bg-amber-900/20"
      role="status"
      data-testid="otoha-catalog-image-switch-notice"
    >
      <div class="min-w-0">
        <p class="font-medium text-amber-900 dark:text-amber-200">{{ t('admin.otohaCatalog.images.switchNotice.title') }}</p>
        <p class="mt-1 text-sm text-amber-800 dark:text-amber-300">{{ t('admin.otohaCatalog.images.switchNotice.body') }}</p>
      </div>
      <button type="button" class="btn btn-primary shrink-0" :disabled="enablingImages" data-testid="otoha-catalog-image-switch-on" @click="turnOnDrawing">
        {{ enablingImages ? t('admin.otohaCatalog.images.switchNotice.turningOn') : t('admin.otohaCatalog.images.switchNotice.turnOn') }}
      </button>
    </div>

    <div class="space-y-3 rounded-lg bg-gray-50 p-3 dark:bg-dark-700" data-testid="otoha-catalog-image-discovery">
      <div class="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
        <div class="min-w-0">
          <h5 class="text-sm font-semibold text-gray-800 dark:text-gray-100">{{ t('admin.otohaCatalog.images.discovery.title') }}</h5>
          <p class="mt-1 text-sm text-gray-600 dark:text-gray-400">{{ t('admin.otohaCatalog.images.discovery.intro') }}</p>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ checkedLine }}</p>
        </div>
        <button type="button" class="btn btn-secondary shrink-0" :disabled="checking" data-testid="otoha-catalog-image-discover" @click="checkUpstream">
          {{ checking ? t('admin.otohaCatalog.images.discovery.checking') : t('admin.otohaCatalog.images.discovery.check') }}
        </button>
      </div>

      <template v-if="discovery && discovery.ran_at">
        <p v-if="skippedText" class="text-sm text-gray-700 dark:text-gray-300">{{ skippedText }}</p>
        <template v-else>
          <ul v-if="discovery.accounts.length" class="space-y-1 text-sm text-gray-700 dark:text-gray-300">
            <li v-for="account in discovery.accounts" :key="account.id">
              <span class="font-medium">{{ account.name }}</span>
              <span class="text-gray-500 dark:text-gray-400">
                {{ account.error ? ' · ' + accountErrorText(account.error) : ' · ' + t('admin.otohaCatalog.images.discovery.accountCounts', { listed: account.listed, images: account.image_models }) }}
              </span>
            </li>
          </ul>
          <p v-if="discovery.candidates.length === 0" class="text-sm text-gray-600 dark:text-gray-400">{{ t('admin.otohaCatalog.images.discovery.nothingFound') }}</p>
          <div v-else class="overflow-x-auto rounded-lg border border-gray-200 bg-white dark:border-dark-600 dark:bg-dark-800">
            <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-600">
              <thead class="bg-gray-50 dark:bg-dark-700">
                <tr class="text-left text-xs font-medium tracking-wide text-gray-500 dark:text-gray-400">
                  <th class="px-3 py-2">{{ t('admin.otohaCatalog.columns.model') }}</th>
                  <th class="px-3 py-2">{{ t('admin.otohaCatalog.images.discovery.columns.accounts') }}</th>
                  <th class="px-3 py-2">{{ t('admin.otohaCatalog.images.discovery.columns.how') }}</th>
                  <th class="px-3 py-2">{{ t('admin.otohaCatalog.images.discovery.columns.result') }}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
                <tr v-for="candidate in discovery.candidates" :key="candidate.model_id" :data-testid="`otoha-catalog-image-candidate-${candidate.model_id}`">
                  <td class="whitespace-nowrap px-3 py-2">
                    <div class="font-medium text-gray-900 dark:text-white">{{ candidate.name || candidate.model_id }}</div>
                    <div class="font-mono text-xs text-gray-500 dark:text-gray-400">{{ candidate.model_id }}</div>
                  </td>
                  <td class="px-3 py-2 text-gray-700 dark:text-gray-300">{{ candidate.accounts.join(t('admin.otohaCatalog.preview.separator')) }}</td>
                  <td class="min-w-[12rem] px-3 py-2 text-gray-700 dark:text-gray-300">{{ evidenceText(candidate.evidence) }}</td>
                  <td class="min-w-[12rem] px-3 py-2">
                    <span :class="['badge whitespace-nowrap', candidate.result === 'not_added' ? 'badge-warning' : 'badge-gray']">{{ resultText(candidate.result) }}</span>
                    <p v-if="candidate.result === 'not_added' && reasonText(candidate.reason)" class="mt-1 max-w-xs text-xs text-gray-500 dark:text-gray-400">{{ reasonText(candidate.reason) }}</p>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <p v-if="discovery.missing.length" class="text-sm text-gray-700 dark:text-gray-300">
            {{ t('admin.otohaCatalog.images.discovery.missing', { models: discovery.missing.join(t('admin.otohaCatalog.preview.separator')) }) }}
          </p>
        </template>
      </template>
    </div>

    <form class="flex flex-col gap-2 sm:flex-row" @submit.prevent="openCreate">
      <input
        v-model="newModelId"
        type="text"
        autocomplete="off"
        class="input flex-1"
        :placeholder="t('admin.otohaCatalog.images.addPlaceholder')"
        data-testid="otoha-catalog-new-image-model"
      />
      <button type="submit" class="btn btn-primary shrink-0" :disabled="!newModelId.trim()">
        <Icon name="plus" size="sm" class="mr-1" />
        {{ t('admin.otohaCatalog.images.add') }}
      </button>
    </form>

    <div
      v-if="entries.length === 0"
      class="rounded-lg border border-dashed border-gray-300 px-4 py-6 text-center dark:border-dark-500"
    >
      <p class="font-medium text-gray-700 dark:text-gray-200">{{ t('admin.otohaCatalog.images.empty') }}</p>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.otohaCatalog.images.emptyHint') }}</p>
    </div>
    <div v-else class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-600">
      <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-600">
        <thead class="bg-gray-50 dark:bg-dark-700">
          <tr class="text-left text-xs font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400">
            <th class="px-3 py-2">{{ t('admin.otohaCatalog.columns.model') }}</th>
            <th class="px-3 py-2">{{ t('admin.otohaCatalog.images.columns.sizes') }}</th>
            <th class="px-3 py-2">
              {{ t('admin.otohaCatalog.images.columns.price') }}
              <span class="block font-normal normal-case">{{ t('admin.otohaCatalog.images.perPicture') }}</span>
            </th>
            <th class="px-3 py-2">{{ t('admin.otohaCatalog.columns.shown') }}</th>
            <th class="px-3 py-2">{{ t('admin.otohaCatalog.columns.status') }}</th>
            <th class="px-3 py-2 text-right">{{ t('admin.otohaCatalog.columns.actions') }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
          <tr v-for="entry in entries" :key="entry.id" :data-testid="`otoha-catalog-image-row-${entry.model_id}`">
            <td class="whitespace-nowrap px-3 py-2">
              <div class="font-medium text-gray-900 dark:text-white">{{ entry.name || entry.model_id }}</div>
              <div class="font-mono text-xs text-gray-500 dark:text-gray-400">{{ entry.model_id }}</div>
              <div v-if="entry.edits" class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.otohaCatalog.images.canEdit') }}</div>
              <div v-if="entry.source === 'auto' || imageUpstreamMissing(entry)" class="mt-1 flex flex-wrap gap-1">
                <span v-if="entry.source === 'auto'" class="badge badge-primary whitespace-nowrap" :title="t('admin.otohaCatalog.images.autoHint')">{{ t('admin.otohaCatalog.images.auto') }}</span>
                <span v-if="imageUpstreamMissing(entry)" class="badge badge-warning whitespace-nowrap" :title="t('admin.otohaCatalog.images.statusHint.upstream_missing')">{{ t('admin.otohaCatalog.status.upstream_missing') }}</span>
              </div>
            </td>
            <td class="px-3 py-2">
              <div class="flex max-w-[16rem] flex-wrap gap-1">
                <span
                  v-for="size in entry.sizes"
                  :key="size"
                  class="whitespace-nowrap rounded bg-gray-100 px-1.5 py-0.5 text-xs text-gray-700 dark:bg-dark-600 dark:text-gray-300"
                >{{ size }}</span>
              </div>
              <div v-if="entry.qualities.length" class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.otohaCatalog.images.qualitiesLine', { qualities: entry.qualities.join(t('admin.otohaCatalog.preview.separator')) }) }}
              </div>
            </td>
            <td class="px-3 py-2">
              <ul v-if="usedPrices(entry).length" class="space-y-0.5">
                <li v-for="price in usedPrices(entry)" :key="price.tier" class="leading-snug">
                  <span class="text-gray-500 dark:text-gray-400">{{ tierName(price.tier) }}</span>
                  <span class="ml-2 whitespace-nowrap font-medium text-gray-900 dark:text-white">{{ formatMoney(price.sale) }}</span>
                  <span v-if="price.set" class="ml-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.otohaCatalog.images.setHere') }}</span>
                </li>
              </ul>
              <span v-else class="text-gray-400">{{ t('admin.otohaCatalog.noPrice') }}</span>
            </td>
            <td class="px-3 py-2">
              <Toggle
                :model-value="entry.enabled"
                :aria-label="t('admin.otohaCatalog.editor.enabled')"
                :disabled="busyIds.has(entry.id)"
                @update:model-value="toggleEnabled(entry, $event)"
              />
            </td>
            <td class="min-w-[13rem] px-3 py-2">
              <span :class="['badge whitespace-nowrap', statusBadge(entry)]">{{ statusLabel(entry) }}</span>
              <p v-if="statusHint(entry)" class="mt-1 max-w-xs text-xs text-gray-500 dark:text-gray-400">{{ statusHint(entry) }}</p>
            </td>
            <td class="whitespace-nowrap px-3 py-2 text-right">
              <button
                type="button"
                class="rounded-lg p-1.5 text-gray-600 hover:bg-gray-100 hover:text-primary-600 dark:text-gray-300 dark:hover:bg-dark-600"
                :title="t('common.edit')"
                :aria-label="t('common.edit')"
                @click="openEdit(entry)"
              >
                <Icon name="edit" size="sm" />
              </button>
              <button
                type="button"
                class="rounded-lg p-1.5 text-gray-600 hover:bg-red-50 hover:text-red-600 dark:text-gray-300 dark:hover:bg-red-900/20"
                :title="t('common.delete')"
                :aria-label="t('common.delete')"
                @click="pendingDelete = entry"
              >
                <Icon name="trash" size="sm" />
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>

  <BaseDialog
    :show="editorOpen"
    :title="editingId ? t('admin.otohaCatalog.images.editTitle', { model: form.model_id }) : t('admin.otohaCatalog.images.createTitle')"
    width="wide"
    :z-index="60"
    @close="closeEditor"
  >
    <form id="otoha-catalog-image-editor" class="space-y-6" data-testid="otoha-catalog-image-editor" @submit.prevent="save">
      <p v-if="editingAuto" class="rounded-lg bg-blue-50 p-3 text-sm text-blue-800 dark:bg-blue-900/20 dark:text-blue-200" data-testid="otoha-catalog-image-auto-note">
        {{ t('admin.otohaCatalog.images.autoEditNote') }}
      </p>

      <section class="space-y-3">
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
        <h4 class="text-sm font-semibold text-gray-800 dark:text-gray-100">{{ t('admin.otohaCatalog.images.editor.abilities') }}</h4>
        <label class="block">
          <span class="input-label">{{ t('admin.otohaCatalog.images.editor.sizes') }}</span>
          <input
            v-model="sizesText"
            type="text"
            class="input w-full font-mono"
            placeholder="1024x1024, 1536x1024, 1024x1536"
            autocomplete="off"
            data-testid="otoha-catalog-image-sizes"
          />
          <span class="input-hint">{{ t('admin.otohaCatalog.images.editor.sizesHint') }}</span>
        </label>
        <label class="block">
          <span class="input-label">{{ t('admin.otohaCatalog.images.editor.qualities') }}</span>
          <input v-model="qualitiesText" type="text" class="input w-full" placeholder="low, medium, high" autocomplete="off" />
          <span class="input-hint">{{ t('admin.otohaCatalog.images.editor.qualitiesHint') }}</span>
        </label>
        <div class="flex items-center gap-3">
          <Toggle v-model="form.edits" />
          <span class="text-sm text-gray-700 dark:text-gray-300">{{ t('admin.otohaCatalog.images.editor.edits') }}</span>
        </div>
      </section>

      <section class="space-y-3" data-testid="otoha-catalog-image-prices">
        <h4 class="text-sm font-semibold text-gray-800 dark:text-gray-100">{{ t('admin.otohaCatalog.images.editor.price') }}</h4>
        <p class="input-hint">{{ t('admin.otohaCatalog.images.editor.priceHint') }}</p>
        <div v-for="row in priceRows" :key="row.tier" class="grid items-end gap-2 sm:grid-cols-3" data-testid="otoha-catalog-image-price-row">
          <div class="text-sm text-gray-700 dark:text-gray-300">
            <div class="font-medium">{{ tierName(row.tier) }}</div>
            <div v-if="sizesInTier(row.tier).length" class="font-mono text-xs text-gray-500 dark:text-gray-400">{{ sizesInTier(row.tier).join(', ') }}</div>
          </div>
          <label class="block">
            <span class="input-label">{{ t('admin.otohaCatalog.images.editor.priceBefore') }}</span>
            <input
              v-model="row.text"
              type="number"
              min="0"
              step="any"
              class="input w-full"
              :placeholder="t('admin.otohaCatalog.images.editor.followBilling')"
            />
          </label>
          <div class="pb-2 text-sm text-gray-600 dark:text-gray-400">
            <template v-if="shownPrice(row.tier)">
              {{ t('admin.otohaCatalog.images.editor.shownNow', { price: formatMoney(shownPrice(row.tier)!.sale), billing: formatMoney(shownPrice(row.tier)!.upstream) }) }}
            </template>
            <template v-else>{{ t('admin.otohaCatalog.images.editor.afterSave') }}</template>
          </div>
        </div>
      </section>

      <p v-if="formError" class="input-error-text" role="alert">{{ formError }}</p>
    </form>

    <template #footer>
      <div class="flex justify-end gap-2">
        <button type="button" class="btn btn-secondary" @click="closeEditor">{{ t('common.cancel') }}</button>
        <button type="submit" form="otoha-catalog-image-editor" class="btn btn-primary" :disabled="saving">
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </div>
    </template>
  </BaseDialog>

  <ConfirmDialog
    :show="pendingDelete !== null"
    :title="t('admin.otohaCatalog.deleteTitle')"
    :message="deleteMessage"
    :confirm-text="t('common.delete')"
    danger
    @confirm="confirmDelete"
    @cancel="pendingDelete = null"
  />
</template>

<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type {
  OtohaImageAdminEntry,
  OtohaImageDiscovery,
  OtohaImageEntryInput,
  OtohaImageTierPrice
} from '@/api/admin/otohaCatalog'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'
import { formatDateTime } from '@/utils/format'
import { extractApiErrorCode, extractApiErrorMessage } from '@/utils/apiError'
import { platformLabel } from '@/utils/platformColors'
import {
  IMAGE_TIERS,
  checkImageSizes,
  discoveryAccountErrorKey,
  discoveryEvidenceKey,
  discoveryReasonKey,
  discoveryResultKey,
  discoverySkippedKey,
  imageSwitchNotice,
  imageUpstreamMissing,
  imagePriceFromText,
  imageSizeTier,
  parseImageList,
  type ImageTier
} from '@/views/admin/otohaCatalogPage'

const MODEL_ID_MAX = 200
const NAME_MAX = 200
const DESCRIPTION_MAX = 2000
const QUALITY = /^[a-z][a-z0-9_-]{0,31}$/

const props = defineProps<{
  groupId: number
  entries: OtohaImageAdminEntry[]
  discovery: OtohaImageDiscovery | null
  groupAllowsImages: boolean
}>()

const emit = defineEmits<{
  changed: []
}>()

const { t } = useI18n()
const appStore = useAppStore()

const DISCOVERY = 'admin.otohaCatalog.images.discovery.'

const newModelId = ref('')
const checking = ref(false)
const enablingImages = ref(false)
const busyIds = reactive(new Set<number>())
const pendingDelete = ref<OtohaImageAdminEntry | null>(null)

const editorOpen = ref(false)
const editingId = ref<number | null>(null)
const saving = ref(false)
const formError = ref('')
const form = reactive<OtohaImageEntryInput>(emptyInput(''))
const sizesText = ref('')
const qualitiesText = ref('')
// The tiers' prices as typed (empty follows billing) and what billing charges now, for the edited model.
const priceRows = reactive<{ tier: ImageTier; text: string | number }[]>(IMAGE_TIERS.map((tier) => ({ tier, text: '' })))
const editorPrices = ref<OtohaImageTierPrice[]>([])

const showSwitchNotice = computed(() => imageSwitchNotice(props.groupAllowsImages, props.entries, props.discovery))
const editingAuto = computed(() => props.entries.find((e) => e.id === editingId.value)?.source === 'auto')
const deleteMessage = computed(() => {
  const entry = pendingDelete.value
  const model = entry?.name || entry?.model_id || ''
  return entry?.source === 'auto'
    ? t('admin.otohaCatalog.images.deleteAutoMessage', { model })
    : t('admin.otohaCatalog.deleteMessage', { model })
})
const checkedLine = computed(() => {
  const ranAt = props.discovery?.ran_at
  if (!ranAt) return t(DISCOVERY + 'never')
  return t(DISCOVERY + (props.discovery?.trigger === 'schedule' ? 'checkedAuto' : 'checked'), { time: formatDateTime(ranAt) })
})
const skippedText = computed(() => {
  const key = discoverySkippedKey(props.discovery?.skipped_reason ?? '')
  return key ? t(DISCOVERY + key) : ''
})

function evidenceText(evidence: string): string {
  return t(DISCOVERY + discoveryEvidenceKey(evidence))
}
function resultText(result: string): string {
  return t(DISCOVERY + discoveryResultKey(result))
}
function reasonText(reason: string): string {
  const key = discoveryReasonKey(reason)
  return key ? t(DISCOVERY + key) : ''
}
function accountErrorText(error: string): string {
  const key = discoveryAccountErrorKey(error)
  return key ? t(DISCOVERY + key) : ''
}

async function checkUpstream() {
  checking.value = true
  try {
    await adminAPI.otohaCatalog.discoverImages(props.groupId)
    emit('changed')
  } catch (error) {
    console.error('Failed to check the upstream accounts for image models:', error)
    appStore.showError(t(DISCOVERY + 'failed'))
  } finally {
    checking.value = false
  }
}

async function turnOnDrawing() {
  enablingImages.value = true
  try {
    await adminAPI.groups.update(props.groupId, { allow_image_generation: true })
    appStore.showSuccess(t('admin.otohaCatalog.images.switchNotice.done'))
    emit('changed')
  } catch (error) {
    console.error('Failed to turn on image generation for the group:', error)
    appStore.showError(t('admin.otohaCatalog.images.switchNotice.failed'))
  } finally {
    enablingImages.value = false
  }
}

function emptyInput(modelId: string): OtohaImageEntryInput {
  return {
    model_id: modelId,
    name: '',
    description: '',
    enabled: true,
    sizes: [],
    qualities: [],
    edits: false,
    price_1k: null,
    price_2k: null,
    price_4k: null
  }
}

function priceOfTier(input: OtohaImageEntryInput, tier: ImageTier): number | null {
  return tier === '1K' ? input.price_1k : tier === '2K' ? input.price_2k : input.price_4k
}

function inputFromEntry(entry: OtohaImageEntryInput): OtohaImageEntryInput {
  return {
    model_id: entry.model_id,
    name: entry.name ?? '',
    description: entry.description ?? '',
    enabled: entry.enabled,
    sizes: [...(entry.sizes ?? [])],
    qualities: [...(entry.qualities ?? [])],
    edits: entry.edits,
    price_1k: entry.price_1k ?? null,
    price_2k: entry.price_2k ?? null,
    price_4k: entry.price_4k ?? null
  }
}

function fillForm(input: OtohaImageEntryInput, prices: OtohaImageTierPrice[]) {
  Object.assign(form, input)
  sizesText.value = input.sizes.join(', ')
  qualitiesText.value = input.qualities.join(', ')
  for (const row of priceRows) {
    row.text = priceOfTier(input, row.tier) ?? ''
  }
  editorPrices.value = prices
}

const TIER_KEYS: Record<ImageTier, string> = { '1K': 'small', '2K': 'medium', '4K': 'large' }

function tierName(tier: ImageTier): string {
  return t('admin.otohaCatalog.images.tierNames.' + TIER_KEYS[tier])
}

function formatMoney(value: number): string {
  return '$' + String(Math.round(value * 1e6) / 1e6)
}

// The tiers the model's sizes fall in, with their prices: the ones the app can be charged.
function usedPrices(entry: OtohaImageAdminEntry): OtohaImageTierPrice[] {
  const used = new Set(entry.sizes.map((size) => imageSizeTier(size)))
  return (entry.prices ?? []).filter((price) => used.has(price.tier))
}

function sizesInTier(tier: ImageTier): string[] {
  return parseImageList(sizesText.value).filter((size) => imageSizeTier(size) === tier)
}

function shownPrice(tier: ImageTier): OtohaImageTierPrice | undefined {
  return editorPrices.value.find((price) => price.tier === tier)
}

function statusLabel(entry: OtohaImageAdminEntry): string {
  return entry.in_catalog ? t('admin.otohaCatalog.status.shown') : t('admin.otohaCatalog.status.' + entry.problem)
}

function statusBadge(entry: OtohaImageAdminEntry): string {
  if (entry.in_catalog) return 'badge-success'
  if (entry.problem === 'disabled') return 'badge-gray'
  return 'badge-warning'
}

function statusHint(entry: OtohaImageAdminEntry): string {
  switch (entry.problem) {
    case 'images_not_allowed':
    case 'not_allowed':
    case 'no_route':
    case 'channel_restricted':
      return t('admin.otohaCatalog.images.statusHint.' + entry.problem)
    case 'no_account':
      return entry.route_platform
        ? t('admin.otohaCatalog.statusHint.no_account_at', { provider: platformLabel(entry.route_platform) })
        : t('admin.otohaCatalog.statusHint.no_account')
    case 'no_price':
    case 'upstream_missing':
      return t('admin.otohaCatalog.images.statusHint.' + entry.problem)
    default:
      return ''
  }
}

function openCreate() {
  const modelId = newModelId.value.trim()
  if (!modelId) return
  const existing = props.entries.find((e) => e.model_id === modelId)
  if (existing) {
    newModelId.value = ''
    openEdit(existing)
    return
  }
  formError.value = ''
  fillForm(emptyInput(modelId), [])
  editingId.value = null
  editorOpen.value = true
}

function openEdit(entry: OtohaImageAdminEntry) {
  formError.value = ''
  fillForm(inputFromEntry(entry), entry.prices ?? [])
  editingId.value = entry.id
  editorOpen.value = true
}

function closeEditor() {
  editorOpen.value = false
  editingId.value = null
}

function buildInput(): OtohaImageEntryInput | null {
  const fail = (message: string) => {
    formError.value = message
    return null
  }
  const modelId = form.model_id.trim()
  if (!modelId) return fail(t('admin.otohaCatalog.editor.required'))
  if (/\s/.test(modelId) || modelId.length > MODEL_ID_MAX) return fail(t('admin.otohaCatalog.editor.modelIdInvalid'))
  const name = form.name.trim()
  const description = form.description.trim()
  if (name.length > NAME_MAX || description.length > DESCRIPTION_MAX) return fail(t('admin.otohaCatalog.editor.tooLong'))
  const sizes = parseImageList(sizesText.value)
  const sizeProblem = checkImageSizes(sizes)
  if (sizeProblem) return fail(t('admin.otohaCatalog.images.errors.sizes.' + sizeProblem))
  const qualities = parseImageList(qualitiesText.value)
  if (qualities.some((quality) => !QUALITY.test(quality))) return fail(t('admin.otohaCatalog.images.errors.qualities'))
  const prices: Record<ImageTier, number | null> = { '1K': null, '2K': null, '4K': null }
  for (const row of priceRows) {
    const parsed = imagePriceFromText(row.text)
    if ('error' in parsed) return fail(t('admin.otohaCatalog.images.errors.price'))
    prices[row.tier] = parsed.price
  }
  return {
    model_id: modelId,
    name,
    description,
    enabled: form.enabled,
    sizes,
    qualities,
    edits: form.edits,
    price_1k: prices['1K'],
    price_2k: prices['2K'],
    price_4k: prices['4K']
  }
}

// Plain words for the admin; the server's own message goes to the console.
function saveErrorMessage(error: unknown): string {
  console.error('Failed to save an Otoha catalog image model:', extractApiErrorMessage(error, 'unknown error'))
  switch (extractApiErrorCode(error)) {
    case 'OTOHA_CATALOG_ENTRY_EXISTS':
      return t('admin.otohaCatalog.images.exists')
    case 'OTOHA_CATALOG_ENTRY_INVALID':
      return t('admin.otohaCatalog.invalid')
    case 'OTOHA_CATALOG_ENTRY_NOT_FOUND':
      return t('admin.otohaCatalog.images.gone')
    default:
      return t('admin.otohaCatalog.saveFailed')
  }
}

async function save() {
  formError.value = ''
  const input = buildInput()
  if (!input) return
  saving.value = true
  try {
    if (editingId.value) {
      await adminAPI.otohaCatalog.updateImageEntry(props.groupId, editingId.value, input)
    } else {
      await adminAPI.otohaCatalog.createImageEntry(props.groupId, input)
      newModelId.value = ''
    }
    appStore.showSuccess(t('admin.otohaCatalog.saved'))
    closeEditor()
    emit('changed')
  } catch (error) {
    formError.value = saveErrorMessage(error)
  } finally {
    saving.value = false
  }
}

async function toggleEnabled(entry: OtohaImageAdminEntry, enabled: boolean) {
  busyIds.add(entry.id)
  try {
    await adminAPI.otohaCatalog.updateImageEntry(props.groupId, entry.id, { ...inputFromEntry(entry), enabled })
    emit('changed')
  } catch (error) {
    appStore.showError(saveErrorMessage(error))
  } finally {
    busyIds.delete(entry.id)
  }
}

async function confirmDelete() {
  const entry = pendingDelete.value
  pendingDelete.value = null
  if (!entry) return
  try {
    await adminAPI.otohaCatalog.deleteImageEntry(props.groupId, entry.id)
    appStore.showSuccess(t('admin.otohaCatalog.deleted'))
    emit('changed')
  } catch (error) {
    console.error('Failed to remove an Otoha catalog image model:', error)
    appStore.showError(t('admin.otohaCatalog.deleteFailed'))
  }
}
</script>
