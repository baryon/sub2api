<template>
  <OtohaLayout>
    <h1 class="text-2xl font-bold text-gray-900 dark:text-white">{{ t('otoha.titles.account') }}</h1>

    <div v-if="loading" class="flex justify-center py-16">
      <div class="h-8 w-8 animate-spin rounded-full border-4 border-primary-500 border-t-transparent"></div>
    </div>

    <div v-else-if="loadError" class="card mt-6 p-6 text-center">
      <p class="text-sm text-red-600 dark:text-red-400">{{ loadError }}</p>
      <button type="button" class="btn btn-secondary mt-4" @click="load">{{ t('otoha.account.retry') }}</button>
    </div>

    <div v-else-if="account" class="mt-6 space-y-6">
      <p class="text-sm text-gray-500 dark:text-dark-400">{{ account.email }}</p>

      <div class="grid gap-4 md:grid-cols-2">
        <!-- Plan and this period's use -->
        <section class="card p-5">
          <h2 class="text-sm font-medium text-gray-500 dark:text-dark-400">{{ t('otoha.account.planTitle') }}</h2>
          <template v-if="account.plan && usage">
            <p class="mt-1 text-xl font-semibold text-gray-900 dark:text-white">{{ account.plan.name }}</p>
            <p class="text-sm text-gray-600 dark:text-dark-300">{{ t('otoha.account.expires', { date: formatDate(account.plan.expires_at) }) }}</p>
            <div class="mt-4">
              <p class="text-sm text-gray-700 dark:text-dark-200">
                {{ usage.limit != null
                  ? t('otoha.account.used', { used: usd(usage.used), limit: usd(usage.limit) })
                  : t('otoha.account.usedUnlimited', { used: usd(usage.used) }) }}
              </p>
              <div v-if="usage.percent != null" class="mt-2 h-2 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-700">
                <div
                  class="h-full rounded-full"
                  :class="usage.percent >= 90 ? 'bg-red-500' : usage.percent >= 80 ? 'bg-amber-500' : 'bg-primary-500'"
                  :style="{ width: `${usage.percent}%` }"
                ></div>
              </div>
              <p class="mt-2 text-xs text-gray-500 dark:text-dark-400">
                <span v-if="usage.remaining != null">{{ t('otoha.account.remaining', { amount: usd(usage.remaining) }) }}</span>
                <span v-if="usage.remaining != null && account.plan.period_resets_at"> · </span>
                <span v-if="account.plan.period_resets_at">{{ t('otoha.account.resets', { date: formatDate(account.plan.period_resets_at) }) }}</span>
              </p>
            </div>
            <router-link to="/otoha/buy" class="btn btn-secondary mt-4">{{ t('otoha.account.renew') }}</router-link>
          </template>
          <template v-else>
            <p class="mt-1 text-gray-700 dark:text-dark-200">{{ t('otoha.account.noPlan') }}</p>
            <router-link to="/otoha/buy" class="btn btn-primary mt-4">{{ t('otoha.account.buyPlan') }}</router-link>
          </template>
        </section>

        <!-- Balance -->
        <section class="card p-5">
          <h2 class="text-sm font-medium text-gray-500 dark:text-dark-400">{{ t('otoha.account.balanceTitle') }}</h2>
          <p class="mt-1 text-xl font-semibold text-gray-900 dark:text-white">{{ usd(account.balance) }}</p>
          <p class="text-sm text-gray-600 dark:text-dark-300">{{ t('otoha.account.balanceHint') }}</p>
          <router-link :to="{ path: '/otoha/buy', hash: '#topup' }" class="btn btn-secondary mt-4">{{ t('otoha.account.topUp') }}</router-link>
        </section>
      </div>

      <!-- Open the app, or set it up by hand -->
      <section class="card p-5">
        <h2 class="mb-4 text-lg font-semibold text-gray-900 dark:text-white">{{ t('otoha.connect.title') }}</h2>
        <OtohaConnectPanel />
      </section>

      <!-- Models and prices -->
      <section class="card p-5">
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('otoha.account.modelsTitle') }}</h2>
        <p v-if="!models.length" class="mt-2 text-sm text-gray-500 dark:text-dark-400">{{ t('otoha.account.modelsEmpty') }}</p>
        <template v-else>
          <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ t('otoha.account.modelsHint') }}</p>
          <div class="mt-4 overflow-x-auto">
            <table class="w-full text-left text-sm">
              <thead class="text-xs uppercase text-gray-500 dark:text-dark-400">
                <tr>
                  <th class="py-2 pr-4 font-medium">{{ t('otoha.account.modelColumn') }}</th>
                  <th class="py-2 font-medium">{{ t('otoha.account.priceColumn') }}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-800">
                <tr v-for="model in models" :key="model.id" class="align-top">
                  <td class="py-3 pr-4">
                    <p class="font-medium text-gray-900 dark:text-white">{{ model.name || model.id }}</p>
                    <p v-if="model.description" class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ model.description }}</p>
                    <div class="mt-1 flex flex-wrap gap-1">
                      <span v-if="costLabel(model.cost)" class="rounded bg-gray-100 px-1.5 py-0.5 text-xs text-gray-600 dark:bg-dark-800 dark:text-dark-300">{{ costLabel(model.cost) }}</span>
                      <span v-if="model.images" class="rounded bg-gray-100 px-1.5 py-0.5 text-xs text-gray-600 dark:bg-dark-800 dark:text-dark-300">{{ t('otoha.account.images') }}</span>
                      <span v-if="model.tools" class="rounded bg-gray-100 px-1.5 py-0.5 text-xs text-gray-600 dark:bg-dark-800 dark:text-dark-300">{{ t('otoha.account.tools') }}</span>
                    </div>
                  </td>
                  <td class="whitespace-nowrap py-3 text-gray-700 dark:text-dark-200">
                    <p>{{ t('otoha.account.inputPrice', { price: formatPerMillion(model.price?.input) }) }}</p>
                    <p>{{ t('otoha.account.outputPrice', { price: formatPerMillion(model.price?.output) }) }}</p>
                    <p v-if="model.price?.cachedInput != null" class="text-xs text-gray-500 dark:text-dark-400">
                      {{ t('otoha.account.cachedPrice', { price: formatPerMillion(model.price.cachedInput) }) }}
                    </p>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </template>
      </section>
    </div>
  </OtohaLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import OtohaLayout from '@/components/otoha/OtohaLayout.vue'
import OtohaConnectPanel from '@/components/otoha/OtohaConnectPanel.vue'
import { formatPaymentAmount } from '@/components/payment/currency'
import { otohaAPI, type OtohaAccount } from '@/api/otoha'
import { formatPerMillion, periodUsage } from './otohaRules'
import { otohaErrorMessage } from './otohaErrors'

const { t, te, locale } = useI18n()

const loading = ref(false)
const loadError = ref('')
const account = ref<OtohaAccount | null>(null)

const usage = computed(() => (account.value?.plan ? periodUsage(account.value.plan) : null))
// The catalog comes with the account summary; it is null until the group's catalog is published (TASK-54).
const models = computed(() => account.value?.catalog?.models ?? [])

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    const { data } = await otohaAPI.getAccount()
    account.value = data
  } catch (err: unknown) {
    loadError.value = otohaErrorMessage(err, t, 'otoha.account.loadFailed')
  } finally {
    loading.value = false
  }
}

function usd(amount: number): string {
  return formatPaymentAmount(amount, 'USD', locale.value)
}

function formatDate(iso: string): string {
  const date = new Date(iso)
  return Number.isNaN(date.getTime()) ? '' : date.toLocaleDateString(locale.value)
}

function costLabel(cost: string): string {
  const key = `otoha.account.cost.${cost}`
  return cost && te(key) ? t(key) : ''
}

onMounted(load)
</script>
