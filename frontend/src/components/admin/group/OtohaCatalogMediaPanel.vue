<template>
  <section class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-600" data-testid="otoha-catalog-media">
    <div>
      <h4 class="text-sm font-semibold text-gray-800 dark:text-gray-100">{{ t('admin.otohaCatalog.media.title') }}</h4>
      <p class="mt-1 text-sm text-gray-600 dark:text-gray-400">{{ t('admin.otohaCatalog.media.intro') }}</p>
    </div>

    <div
      v-if="blockedModels.length && canAllow"
      class="flex flex-col gap-3 rounded-lg border border-amber-300 bg-amber-50 p-3 sm:flex-row sm:items-center sm:justify-between dark:border-amber-700 dark:bg-amber-900/20"
      role="status"
      data-testid="otoha-catalog-media-allow-notice"
    >
      <div class="min-w-0">
        <p class="font-medium text-amber-900 dark:text-amber-200">{{ t('admin.otohaCatalog.media.allowNotice.title', { count: blockedModels.length }) }}</p>
        <p class="mt-1 break-words font-mono text-xs text-amber-800 dark:text-amber-300">{{ blockedModels.join(t('admin.otohaCatalog.preview.separator')) }}</p>
        <p class="mt-1 text-sm text-amber-800 dark:text-amber-300">{{ t('admin.otohaCatalog.media.allowNotice.body') }}</p>
      </div>
      <button type="button" class="btn btn-primary shrink-0" :disabled="allowing" data-testid="otoha-catalog-media-allow" @click="allowModels">
        {{ allowing ? t('admin.otohaCatalog.media.allowNotice.allowing') : t('admin.otohaCatalog.media.allowNotice.allow') }}
      </button>
    </div>

    <div class="space-y-3 rounded-lg bg-gray-50 p-3 dark:bg-dark-700" data-testid="otoha-catalog-media-discovery">
      <div class="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
        <div class="min-w-0">
          <h5 class="text-sm font-semibold text-gray-800 dark:text-gray-100">{{ t(DISCOVERY + 'title') }}</h5>
          <p class="mt-1 text-sm text-gray-600 dark:text-gray-400">{{ t(DISCOVERY + 'intro') }}</p>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ checkedLine }}</p>
        </div>
        <button type="button" class="btn btn-secondary shrink-0" :disabled="checking" data-testid="otoha-catalog-media-discover" @click="checkUpstream">
          {{ checking ? t(DISCOVERY + 'checking') : t(DISCOVERY + 'check') }}
        </button>
      </div>

      <template v-if="discovery && discovery.ran_at">
        <p v-if="skippedText" class="text-sm text-gray-700 dark:text-gray-300">{{ skippedText }}</p>
        <template v-else>
          <ul v-if="discovery.accounts.length" class="space-y-1 text-sm text-gray-700 dark:text-gray-300">
            <li v-for="account in discovery.accounts" :key="account.id">
              <span class="font-medium">{{ account.name }}</span>
              <span class="text-gray-500 dark:text-gray-400">
                {{ account.error ? ' · ' + accountErrorText(account.error) : ' · ' + t(DISCOVERY + 'accountCounts', { count: account.media_models }) }}
              </span>
            </li>
          </ul>
          <p v-if="discovery.candidates.length === 0" class="text-sm text-gray-600 dark:text-gray-400">{{ t(DISCOVERY + 'nothingFound') }}</p>
          <div v-else class="overflow-x-auto rounded-lg border border-gray-200 bg-white dark:border-dark-600 dark:bg-dark-800">
            <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-600">
              <thead class="bg-gray-50 dark:bg-dark-700">
                <tr class="text-left text-xs font-medium tracking-wide text-gray-500 dark:text-gray-400">
                  <th class="px-3 py-2">{{ t('admin.otohaCatalog.columns.model') }}</th>
                  <th class="px-3 py-2">{{ t(DISCOVERY + 'columns.accounts') }}</th>
                  <th class="px-3 py-2">{{ t(DISCOVERY + 'columns.how') }}</th>
                  <th class="px-3 py-2">{{ t(DISCOVERY + 'columns.result') }}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
                <tr v-for="candidate in discovery.candidates" :key="candidate.kind + '/' + candidate.model_id" :data-testid="`otoha-catalog-media-candidate-${candidate.model_id}`">
                  <td class="whitespace-nowrap px-3 py-2">
                    <div class="font-medium text-gray-900 dark:text-white">{{ candidate.name || candidate.model_id }}</div>
                    <div class="text-xs text-gray-500 dark:text-gray-400">
                      {{ t('admin.otohaCatalog.media.kinds.' + candidate.kind) }} · <span class="font-mono">{{ candidate.model_id }}</span>
                    </div>
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
            {{ t(DISCOVERY + 'missing', { models: missingNames }) }}
          </p>
        </template>
      </template>
    </div>

    <div v-if="entries.length === 0" class="rounded-lg border border-dashed border-gray-300 px-4 py-6 text-center dark:border-dark-500">
      <p class="font-medium text-gray-700 dark:text-gray-200">{{ t('admin.otohaCatalog.media.empty') }}</p>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.otohaCatalog.media.emptyHint') }}</p>
    </div>
    <div v-else class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-600">
      <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-600">
        <thead class="bg-gray-50 dark:bg-dark-700">
          <tr class="text-left text-xs font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400">
            <th class="px-3 py-2">{{ t('admin.otohaCatalog.columns.model') }}</th>
            <th class="px-3 py-2">{{ t('admin.otohaCatalog.media.columns.price') }}</th>
            <th class="px-3 py-2">{{ t('admin.otohaCatalog.columns.shown') }}</th>
            <th class="px-3 py-2">{{ t('admin.otohaCatalog.columns.status') }}</th>
            <th class="px-3 py-2 text-right">{{ t('admin.otohaCatalog.columns.actions') }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
          <tr v-for="entry in entries" :key="entry.kind + '/' + entry.model_id" :data-testid="`otoha-catalog-media-row-${entry.model_id}`">
            <td class="px-3 py-2">
              <div class="whitespace-nowrap font-medium text-gray-900 dark:text-white">{{ entry.name || entry.model_id }}</div>
              <div class="whitespace-nowrap text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.otohaCatalog.media.kinds.' + entry.kind) }} · <span class="font-mono">{{ entry.model_id }}</span>
              </div>
              <div class="mt-0.5 whitespace-nowrap font-mono text-xs text-gray-500 dark:text-gray-400">{{ entry.endpoint }}</div>
              <p v-if="entry.description" class="mt-0.5 max-w-xs text-xs text-gray-500 dark:text-gray-400">{{ entry.description }}</p>
            </td>
            <td class="px-3 py-2">
              <ul v-if="entry.prices.length" class="space-y-0.5">
                <li v-for="price in entry.prices" :key="price.label" class="leading-snug">
                  <span class="text-gray-500 dark:text-gray-400">{{ priceLabel(entry, price.label) }}</span>
                  <span class="ml-2 whitespace-nowrap font-medium text-gray-900 dark:text-white">{{ formatMoney(price.sale) }}</span>
                  <span class="ml-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.otohaCatalog.media.billingPrice', { price: formatMoney(price.upstream) }) }}</span>
                </li>
              </ul>
              <span v-else class="text-gray-400">{{ t('admin.otohaCatalog.noPrice') }}</span>
              <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{{ perText(entry.per) }}</p>
            </td>
            <td class="px-3 py-2">
              <Toggle
                :model-value="entry.enabled"
                :aria-label="t('admin.otohaCatalog.editor.enabled')"
                :disabled="busyKeys.has(keyOf(entry))"
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
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>

  <BaseDialog
    :show="editorOpen"
    :title="t('admin.otohaCatalog.media.editTitle', { model: form.model_id })"
    width="wide"
    :z-index="60"
    @close="closeEditor"
  >
    <form id="otoha-catalog-media-editor" class="space-y-4" data-testid="otoha-catalog-media-editor" @submit.prevent="save">
      <p class="rounded-lg bg-blue-50 p-3 text-sm text-blue-800 dark:bg-blue-900/20 dark:text-blue-200">{{ t('admin.otohaCatalog.media.editNote') }}</p>
      <label class="block">
        <span class="input-label">{{ t('admin.otohaCatalog.editor.name') }}</span>
        <input v-model="form.name" type="text" class="input w-full" :placeholder="form.model_id" autocomplete="off" />
      </label>
      <label class="block">
        <span class="input-label">{{ t('admin.otohaCatalog.editor.description') }}</span>
        <textarea v-model="form.description" rows="2" class="input w-full"></textarea>
      </label>
      <div class="flex items-center gap-3">
        <Toggle v-model="form.enabled" />
        <span class="text-sm text-gray-700 dark:text-gray-300">{{ t('admin.otohaCatalog.editor.enabled') }}</span>
      </div>
      <p v-if="formError" class="input-error-text" role="alert">{{ formError }}</p>
    </form>

    <template #footer>
      <div class="flex justify-end gap-2">
        <button type="button" class="btn btn-secondary" @click="closeEditor">{{ t('common.cancel') }}</button>
        <button type="submit" form="otoha-catalog-media-editor" class="btn btn-primary" :disabled="saving">
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { OtohaMediaAdminEntry, OtohaMediaDiscovery, OtohaMediaEntryInput } from '@/api/admin/otohaCatalog'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'
import { formatDateTime } from '@/utils/format'
import { extractApiErrorCode, extractApiErrorMessage } from '@/utils/apiError'
import {
  discoveryAccountErrorKey,
  discoveryEvidenceKey,
  discoveryReasonKey,
  discoveryResultKey,
  discoverySkippedKey
} from '@/views/admin/otohaCatalogPage'
import { allowlistWithModels, mediaBlockedModels } from '@/views/admin/otohaCatalogMedia'

const props = defineProps<{
  groupId: number
  entries: OtohaMediaAdminEntry[]
  discovery: OtohaMediaDiscovery | null
  groupAllowlist: { enabled: boolean; models: string[] }
}>()

const emit = defineEmits<{
  changed: []
}>()

const { t } = useI18n()
const appStore = useAppStore()

const DISCOVERY = 'admin.otohaCatalog.media.discovery.'

const checking = ref(false)
const allowing = ref(false)
const busyKeys = reactive(new Set<string>())

const editorOpen = ref(false)
const saving = ref(false)
const formError = ref('')
const form = reactive<OtohaMediaEntryInput>({ kind: 'video', model_id: '', name: '', description: '', enabled: true })

const blockedModels = computed(() => mediaBlockedModels(props.entries, props.discovery))
// The group's model list is the admin's: the button only adds to a list that is already on.
const canAllow = computed(() => allowlistWithModels(props.groupAllowlist, blockedModels.value) !== null)
const checkedLine = computed(() => {
  const ranAt = props.discovery?.ran_at
  if (!ranAt) return t(DISCOVERY + 'never')
  return t(DISCOVERY + (props.discovery?.trigger === 'schedule' ? 'checkedAuto' : 'checked'), { time: formatDateTime(ranAt) })
})
const skippedText = computed(() => {
  const key = discoverySkippedKey(props.discovery?.skipped_reason ?? '')
  return key ? t(DISCOVERY + key) : ''
})
const missingNames = computed(() =>
  (props.discovery?.missing ?? []).map((key) => key.split('/').slice(1).join('/')).join(t('admin.otohaCatalog.preview.separator'))
)

function keyOf(entry: { kind: string; model_id: string }): string {
  return entry.kind + '/' + entry.model_id
}

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

// What a price is per, in words.
function perText(per: string): string {
  switch (per) {
    case 'second':
      return t('admin.otohaCatalog.media.per.second')
    case '1M characters':
      return t('admin.otohaCatalog.media.per.millionCharacters')
    case 'hour':
      return t('admin.otohaCatalog.media.per.hour')
    case '1M tokens':
      return t('admin.otohaCatalog.media.per.millionTokens')
    default:
      return per
  }
}

function formatMoney(value: number): string {
  return '$' + String(Math.round(value * 1e6) / 1e6)
}

// A video price is for a resolution; the others name what the price is for.
function priceLabel(entry: OtohaMediaAdminEntry, label: string): string {
  return entry.kind === 'video' ? label : t('admin.otohaCatalog.media.priceLabels.' + label)
}

function statusLabel(entry: OtohaMediaAdminEntry): string {
  return entry.in_catalog ? t('admin.otohaCatalog.status.shown') : t('admin.otohaCatalog.status.' + entry.problem)
}

function statusBadge(entry: OtohaMediaAdminEntry): string {
  if (entry.in_catalog) return 'badge-success'
  if (entry.problem === 'disabled') return 'badge-gray'
  return 'badge-warning'
}

function statusHint(entry: OtohaMediaAdminEntry): string {
  switch (entry.problem) {
    case 'not_allowed':
    case 'no_price':
    case 'upstream_missing':
    case 'channel_restricted':
      return t('admin.otohaCatalog.media.statusHint.' + entry.problem)
    case 'no_route':
      return t('admin.otohaCatalog.media.statusHint.' + (entry.kind === 'voice' ? 'no_route_voice' : 'no_route'))
    case 'no_account':
      return t('admin.otohaCatalog.media.statusHint.no_account')
    default:
      return ''
  }
}

async function checkUpstream() {
  checking.value = true
  try {
    await adminAPI.otohaCatalog.discoverMedia(props.groupId)
    emit('changed')
  } catch (error) {
    console.error('Failed to check the upstream accounts for video, voice and quick-judgement models:', error)
    appStore.showError(t(DISCOVERY + 'failed'))
  } finally {
    checking.value = false
  }
}

async function allowModels() {
  const list = allowlistWithModels(props.groupAllowlist, blockedModels.value)
  if (!list) return
  allowing.value = true
  try {
    await adminAPI.groups.update(props.groupId, { model_allowlist: list })
    appStore.showSuccess(t('admin.otohaCatalog.media.allowNotice.done'))
    emit('changed')
  } catch (error) {
    console.error('Failed to add models to the group model list:', error)
    appStore.showError(t('admin.otohaCatalog.media.allowNotice.failed'))
  } finally {
    allowing.value = false
  }
}

function inputFromEntry(entry: OtohaMediaAdminEntry): OtohaMediaEntryInput {
  return { kind: entry.kind, model_id: entry.model_id, name: entry.name, description: entry.description, enabled: entry.enabled }
}

function openEdit(entry: OtohaMediaAdminEntry) {
  formError.value = ''
  Object.assign(form, inputFromEntry(entry))
  editorOpen.value = true
}

function closeEditor() {
  editorOpen.value = false
}

// Plain words for the admin; the server's own message goes to the console.
function saveErrorMessage(error: unknown): string {
  console.error('Failed to save an Otoha catalog media model:', extractApiErrorMessage(error, 'unknown error'))
  switch (extractApiErrorCode(error)) {
    case 'OTOHA_CATALOG_ENTRY_INVALID':
      return t('admin.otohaCatalog.invalid')
    case 'OTOHA_CATALOG_ENTRY_NOT_FOUND':
      return t('admin.otohaCatalog.media.gone')
    default:
      return t('admin.otohaCatalog.saveFailed')
  }
}

async function save() {
  formError.value = ''
  if (form.name.trim().length > 200 || form.description.trim().length > 2000) {
    formError.value = t('admin.otohaCatalog.editor.tooLong')
    return
  }
  saving.value = true
  try {
    await adminAPI.otohaCatalog.updateMediaEntry(props.groupId, { ...form, name: form.name.trim(), description: form.description.trim() })
    appStore.showSuccess(t('admin.otohaCatalog.saved'))
    closeEditor()
    emit('changed')
  } catch (error) {
    formError.value = saveErrorMessage(error)
  } finally {
    saving.value = false
  }
}

async function toggleEnabled(entry: OtohaMediaAdminEntry, enabled: boolean) {
  const key = keyOf(entry)
  busyKeys.add(key)
  try {
    await adminAPI.otohaCatalog.updateMediaEntry(props.groupId, { ...inputFromEntry(entry), enabled })
    emit('changed')
  } catch (error) {
    appStore.showError(saveErrorMessage(error))
  } finally {
    busyKeys.delete(key)
  }
}
</script>
