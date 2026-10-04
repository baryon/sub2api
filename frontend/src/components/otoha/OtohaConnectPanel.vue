<template>
  <div class="space-y-4">
    <div class="flex flex-wrap gap-3">
      <button type="button" class="btn btn-primary px-6 py-2.5 text-base" :disabled="busy" @click="openApp">
        {{ opening ? t('otoha.connect.opening') : t('otoha.connect.open') }}
      </button>
      <button type="button" class="btn btn-secondary" :disabled="busy" @click="copyCode">
        <Icon name="copy" size="sm" class="mr-1.5" />
        {{ t('otoha.connect.copyCode') }}
      </button>
      <button type="button" class="btn btn-secondary" :disabled="busy" @click="downloadConfig">
        <Icon name="download" size="sm" class="mr-1.5" />
        {{ t('otoha.connect.download') }}
      </button>
    </div>

    <div v-if="claim" class="rounded-xl bg-gray-50 p-4 dark:bg-dark-800">
      <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('otoha.connect.codeLabel') }}</p>
      <p class="mt-1 select-all font-mono text-lg tracking-wider text-gray-900 dark:text-white">{{ claim.code }}</p>
      <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
        {{ t('otoha.connect.codeHint', { time: formatTime(claim.expires_at) }) }}
      </p>
    </div>

    <p v-if="error" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
    <p v-else-if="notice" class="text-sm text-green-700 dark:text-green-400">{{ notice }}</p>

    <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('otoha.connect.fallback') }}</p>
    <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('otoha.connect.notInstalled') }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { otohaAPI, type OtohaClaimCode } from '@/api/otoha'
import { useClipboard } from '@/composables/useClipboard'
import { otohaErrorMessage } from '@/views/otoha/otohaErrors'
import { useOtohaPortal } from '@/composables/useOtohaPortal'

const props = defineProps<{
  /** Create a code as soon as the panel shows (the success page). */
  autoCreate?: boolean
}>()

const { t, locale } = useI18n()
const { copyToClipboard } = useClipboard()
const { portalActive } = useOtohaPortal()

const claim = ref<OtohaClaimCode | null>(null)
const creating = ref(false)
const opening = ref(false)
const downloading = ref(false)
const error = ref('')
const notice = ref('')
const busy = computed(() => creating.value || downloading.value)

// A code is replaced a minute before it runs out, so the app never receives one that just expired.
const REFRESH_MARGIN_MS = 60_000

async function freshClaim(): Promise<OtohaClaimCode | null> {
  if (claim.value && Date.parse(claim.value.expires_at) - Date.now() > REFRESH_MARGIN_MS) {
    return claim.value
  }
  creating.value = true
  error.value = ''
  try {
    const { data } = await otohaAPI.createClaim()
    claim.value = data
    return data
  } catch (err: unknown) {
    error.value = otohaErrorMessage(err, t, 'otoha.errors.claimFailed', { portal: portalActive.value })
    return null
  } finally {
    creating.value = false
  }
}

async function openApp() {
  notice.value = ''
  const current = await freshClaim()
  if (!current) return
  opening.value = true
  window.location.href = current.open_url
  window.setTimeout(() => { opening.value = false }, 3000)
}

async function copyCode() {
  notice.value = ''
  const current = await freshClaim()
  if (!current) return
  await copyToClipboard(current.code, t('otoha.connect.codeCopied'))
}

async function downloadConfig() {
  notice.value = ''
  error.value = ''
  downloading.value = true
  try {
    const { data } = await otohaAPI.downloadConfig()
    const url = URL.createObjectURL(data)
    const link = document.createElement('a')
    link.href = url
    link.download = 'config.json'
    document.body.appendChild(link)
    link.click()
    link.remove()
    URL.revokeObjectURL(url)
    notice.value = t('otoha.connect.downloaded')
  } catch (err: unknown) {
    error.value = otohaErrorMessage(err, t, 'otoha.errors.downloadFailed', { portal: portalActive.value })
  } finally {
    downloading.value = false
  }
}

function formatTime(iso: string): string {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleTimeString(locale.value, { hour: '2-digit', minute: '2-digit' })
}

onMounted(() => {
  if (props.autoCreate) void freshClaim()
})
</script>
