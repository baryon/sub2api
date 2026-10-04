<template>
  <OtohaLayout wide>
    <div class="flex flex-wrap items-end justify-between gap-4">
      <div>
        <h1 class="text-2xl font-bold text-gray-900 dark:text-white">{{ t('otoha.titles.usage') }}</h1>
        <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ t('otoha.usage.hint') }}</p>
      </div>
      <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-dark-200">
        <span>{{ t('otoha.usage.rangeLabel') }}</span>
        <select v-model.number="days" class="input w-auto py-1.5 text-sm" @change="reload">
          <option v-for="option in rangeOptions" :key="option.days" :value="option.days">{{ option.label }}</option>
        </select>
      </label>
    </div>

    <!-- Totals for the period -->
    <div class="mt-6 grid grid-cols-3 gap-3">
      <section v-for="card in totals" :key="card.label" class="card p-4">
        <h2 class="text-xs font-medium text-gray-500 dark:text-dark-400">{{ card.label }}</h2>
        <p class="mt-1 truncate text-lg font-semibold text-gray-900 dark:text-white">{{ statsLoaded ? card.value : '–' }}</p>
      </section>
    </div>

    <section class="card mt-6 overflow-hidden">
      <div v-if="loading" class="flex justify-center py-16">
        <div class="h-8 w-8 animate-spin rounded-full border-4 border-primary-500 border-t-transparent"></div>
      </div>

      <div v-else-if="loadError" class="p-6 text-center">
        <p class="text-sm text-red-600 dark:text-red-400">{{ loadError }}</p>
        <button type="button" class="btn btn-secondary mt-4" @click="reload">{{ t('otoha.usage.retry') }}</button>
      </div>

      <p v-else-if="!rows.length" class="p-8 text-center text-sm text-gray-500 dark:text-dark-400">
        {{ t('otoha.usage.empty') }}
      </p>

      <template v-else>
        <!-- Wider screens: a table -->
        <table class="hidden w-full text-left text-sm sm:table">
          <thead class="border-b border-gray-100 text-xs text-gray-500 dark:border-dark-800 dark:text-dark-400">
            <tr>
              <th class="px-4 py-3 font-medium">{{ t('otoha.usage.timeColumn') }}</th>
              <th class="px-4 py-3 font-medium">{{ t('otoha.usage.modelColumn') }}</th>
              <th class="px-4 py-3 font-medium">{{ t('otoha.usage.tokensColumn') }}</th>
              <th class="px-4 py-3 text-right font-medium">{{ t('otoha.usage.chargeColumn') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-800">
            <tr v-for="row in rows" :key="row.id" class="align-top">
              <td class="whitespace-nowrap px-4 py-3 text-gray-700 dark:text-dark-200">{{ row.time }}</td>
              <td class="px-4 py-3 font-medium text-gray-900 dark:text-white">{{ row.model }}</td>
              <td class="px-4 py-3 text-gray-700 dark:text-dark-200">
                <p>{{ number(row.tokens.total) }}</p>
                <p class="text-xs text-gray-500 dark:text-dark-400">{{ row.detail }}</p>
              </td>
              <td class="whitespace-nowrap px-4 py-3 text-right font-medium text-gray-900 dark:text-white">{{ row.charge }}</td>
            </tr>
          </tbody>
        </table>

        <!-- Phones: one block per request -->
        <ul class="divide-y divide-gray-100 dark:divide-dark-800 sm:hidden">
          <li v-for="row in rows" :key="row.id" class="flex items-start justify-between gap-3 px-4 py-3 text-sm">
            <div class="min-w-0">
              <p class="truncate font-medium text-gray-900 dark:text-white">{{ row.model }}</p>
              <p class="text-xs text-gray-500 dark:text-dark-400">{{ row.time }}</p>
              <p class="mt-1 text-xs text-gray-600 dark:text-dark-300">
                {{ number(row.tokens.total) }} {{ t('otoha.usage.tokensColumn') }} · {{ row.detail }}
              </p>
            </div>
            <p class="shrink-0 font-medium text-gray-900 dark:text-white">{{ row.charge }}</p>
          </li>
        </ul>

        <div v-if="pages > 1" class="flex items-center justify-between gap-3 border-t border-gray-100 px-4 py-3 text-sm dark:border-dark-800">
          <button type="button" class="btn btn-secondary" :disabled="page <= 1" @click="goTo(page - 1)">{{ t('otoha.usage.prev') }}</button>
          <span class="text-gray-500 dark:text-dark-400">{{ t('otoha.usage.page', { page, pages }) }}</span>
          <button type="button" class="btn btn-secondary" :disabled="page >= pages" @click="goTo(page + 1)">{{ t('otoha.usage.next') }}</button>
        </div>
      </template>
    </section>
  </OtohaLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import OtohaLayout from '@/components/otoha/OtohaLayout.vue'
import { usageAPI } from '@/api/usage'
import type { UsageLog, UsageStatsResponse } from '@/types'
import { formatCharge, usageRange, usageTokens } from './otohaUsage'

const PAGE_SIZE = 20

const { t, locale } = useI18n()

const days = ref(7)
const page = ref(1)
const total = ref(0)
const logs = ref<UsageLog[]>([])
const stats = ref<UsageStatsResponse | null>(null)
const statsLoaded = ref(false)
const loading = ref(false)
const loadError = ref('')
let requestSeq = 0
let statsSeq = 0

const rangeOptions = computed(() => [
  { days: 7, label: t('otoha.usage.last7') },
  { days: 30, label: t('otoha.usage.last30') },
  { days: 90, label: t('otoha.usage.last90') },
])

const pages = computed(() => Math.max(1, Math.ceil(total.value / PAGE_SIZE)))

const totals = computed(() => [
  { label: t('otoha.usage.requests'), value: number(stats.value?.total_requests ?? 0) },
  { label: t('otoha.usage.tokens'), value: number(stats.value?.total_tokens ?? 0) },
  { label: t('otoha.usage.charged'), value: formatCharge(stats.value?.total_actual_cost) },
])

const rows = computed(() =>
  logs.value.map((log) => {
    const tokens = usageTokens(log)
    const detail = [
      t('otoha.usage.tokenDetail', { input: number(tokens.input), output: number(tokens.output) }),
      tokens.cached > 0 ? t('otoha.usage.cachedDetail', { cached: number(tokens.cached) }) : '',
    ].filter(Boolean).join(' · ')
    return {
      id: log.id,
      time: formatTime(log.created_at),
      model: log.model,
      tokens,
      detail,
      charge: formatCharge(log.actual_cost),
    }
  }),
)

function number(value: number): string {
  return new Intl.NumberFormat(locale.value).format(value)
}

function formatTime(iso: string): string {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleString(locale.value, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

async function load() {
  const seq = ++requestSeq
  const range = usageRange(days.value)
  loading.value = true
  loadError.value = ''
  try {
    const list = await usageAPI.query({
      ...range,
      page: page.value,
      page_size: PAGE_SIZE,
      sort_by: 'created_at',
      sort_order: 'desc',
    })
    if (seq !== requestSeq) return
    logs.value = list.items
    total.value = list.total
  } catch (err: unknown) {
    if (seq !== requestSeq) return
    console.error('Failed to load usage:', err)
    loadError.value = t('otoha.usage.loadFailed')
  } finally {
    if (seq === requestSeq) loading.value = false
  }
}

async function loadStats() {
  const seq = ++statsSeq
  statsLoaded.value = false
  try {
    const data = await usageAPI.getStats(usageRange(days.value))
    if (seq !== statsSeq) return
    stats.value = data
    statsLoaded.value = true
  } catch (err: unknown) {
    // The totals stay as dashes; the list above says whether loading failed.
    console.error('Failed to load usage totals:', err)
  }
}

function reload() {
  page.value = 1
  void load()
  void loadStats()
}

function goTo(target: number) {
  page.value = Math.min(Math.max(1, target), pages.value)
  void load()
}

onMounted(reload)
</script>
