<template>
  <OtohaLayout>
    <div class="mx-auto max-w-xl">
      <!-- Waiting for the payment or the setup -->
      <div v-if="state === 'checking' || state === 'processing' || state === 'waiting'" class="card p-8 text-center">
        <div class="mx-auto h-10 w-10 animate-spin rounded-full border-4 border-primary-500 border-t-transparent"></div>
        <p class="mt-4 text-gray-700 dark:text-dark-200">
          {{ state === 'processing' ? t('otoha.result.processing') : t('otoha.result.checking') }}
        </p>
        <p v-if="orderId" class="mt-2 text-xs text-gray-500 dark:text-dark-400">{{ t('otoha.result.order', { id: orderId }) }}</p>
      </div>

      <!-- Ready -->
      <div v-else-if="state === 'done'" class="card space-y-6 p-8">
        <div class="text-center">
          <div class="mx-auto flex h-14 w-14 items-center justify-center rounded-full bg-green-100 dark:bg-green-900/30">
            <Icon name="check" size="lg" class="text-green-600" />
          </div>
          <h1 class="mt-4 text-2xl font-bold text-gray-900 dark:text-white">{{ t('otoha.result.doneTitle') }}</h1>
          <p class="mt-2 text-gray-600 dark:text-dark-300">{{ t('otoha.result.doneBody') }}</p>
        </div>
        <OtohaConnectPanel v-if="authStore.isAuthenticated" auto-create />
        <div v-else class="text-center">
          <p class="text-sm text-gray-600 dark:text-dark-300">{{ t('otoha.result.loginToClaim') }}</p>
          <router-link :to="{ path: '/login', query: { redirect: route.fullPath } }" class="btn btn-primary mt-3">
            {{ t('otoha.result.login') }}
          </router-link>
        </div>
        <div class="text-center">
          <router-link to="/otoha/account" class="text-sm text-primary-600 hover:underline dark:text-primary-400">
            {{ t('otoha.result.toAccount') }}
          </router-link>
        </div>
      </div>

      <!-- Everything else: what happened and what to do -->
      <div v-else class="card p-8 text-center">
        <h1 class="text-xl font-semibold text-gray-900 dark:text-white">{{ title }}</h1>
        <p v-if="body" class="mt-2 text-gray-600 dark:text-dark-300">{{ body }}</p>
        <div class="mt-6 flex flex-wrap justify-center gap-3">
          <button v-if="state === 'timeout' || state === 'failed'" type="button" class="btn btn-primary" @click="start">
            {{ t('otoha.result.refresh') }}
          </button>
          <router-link v-if="state === 'cancelled' || state === 'refunded'" to="/otoha/buy" class="btn btn-primary">
            {{ t('otoha.result.backToBuy') }}
          </router-link>
          <router-link v-if="authStore.isAuthenticated" to="/otoha/account" class="btn btn-secondary">
            {{ t('otoha.result.toAccount') }}
          </router-link>
          <router-link v-else :to="{ path: '/login', query: { redirect: route.fullPath } }" class="btn btn-secondary">
            {{ t('otoha.result.login') }}
          </router-link>
        </div>
      </div>
    </div>
  </OtohaLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import OtohaLayout from '@/components/otoha/OtohaLayout.vue'
import OtohaConnectPanel from '@/components/otoha/OtohaConnectPanel.vue'
import Icon from '@/components/icons/Icon.vue'
import { paymentAPI } from '@/api/payment'
import { useAuthStore } from '@/stores/auth'
import { orderOutcome, type OrderOutcome } from './otohaRules'

type ResultState = 'checking' | OrderOutcome | 'timeout' | 'missing'

const POLL_INTERVAL_MS = 2000
const MAX_POLLS = 30
const VERIFY_EVERY = 3

const { t } = useI18n()
const route = useRoute()
const authStore = useAuthStore()

const state = ref<ResultState>('checking')
const orderId = ref<number | null>(null)
let timer: number | undefined
let polls = 0
let outTradeNo = ''

const title = computed(() => {
  switch (state.value) {
    case 'failed':
      return t('otoha.result.failedTitle')
    case 'cancelled':
      return t('otoha.result.cancelledTitle')
    case 'refunded':
      return t('otoha.result.refundedTitle')
    case 'timeout':
      return t('otoha.result.timeoutTitle')
    default:
      return t('otoha.result.missingOrder')
  }
})

const body = computed(() => {
  switch (state.value) {
    case 'failed':
      return t('otoha.result.failedBody', { id: orderId.value ?? '' })
    case 'cancelled':
      return t('otoha.result.cancelledBody')
    case 'timeout':
      return t('otoha.result.timeoutBody')
    default:
      return ''
  }
})

function queryString(name: string): string {
  const value = route.query[name]
  return typeof value === 'string' ? value.trim() : ''
}

async function readStatus(): Promise<string> {
  if (authStore.isAuthenticated && orderId.value) {
    // Every few polls, ask the provider directly in case its notification is late.
    if (outTradeNo && polls > 0 && polls % VERIFY_EVERY === 0) {
      try {
        const { data } = await paymentAPI.verifyOrder(outTradeNo)
        return data.status
      } catch {
        // fall back to the stored order
      }
    }
    const { data } = await paymentAPI.getOrder(orderId.value)
    outTradeNo = data.out_trade_no || outTradeNo
    return data.status
  }
  const resumeToken = queryString('resume_token')
  if (resumeToken) {
    const { data } = await paymentAPI.resolveOrderPublicByResumeToken(resumeToken)
    return data.status
  }
  return ''
}

async function poll() {
  let status = ''
  try {
    status = await readStatus()
  } catch (err: unknown) {
    const code = (err as { status?: number } | null)?.status
    if (code === 403 || code === 404) {
      // Not this user's order, or no such order: polling will not change that.
      state.value = 'missing'
      return
    }
    status = '' // a passing network problem: keep trying
  }
  if (!status && polls === 0 && !orderId.value && !queryString('resume_token')) {
    state.value = 'missing'
    return
  }
  const outcome = status ? orderOutcome(status) : 'waiting'
  polls += 1
  if (outcome === 'waiting' || outcome === 'processing') {
    state.value = polls === 1 && outcome === 'waiting' ? 'checking' : outcome
    if (polls >= MAX_POLLS) {
      state.value = 'timeout'
      return
    }
    timer = window.setTimeout(poll, POLL_INTERVAL_MS)
    return
  }
  state.value = outcome
}

function start() {
  window.clearTimeout(timer)
  polls = 0
  state.value = 'checking'
  const id = Number(queryString('order_id'))
  orderId.value = Number.isInteger(id) && id > 0 ? id : null
  outTradeNo = queryString('out_trade_no')
  void poll()
}

onMounted(start)
onBeforeUnmount(() => window.clearTimeout(timer))
</script>
