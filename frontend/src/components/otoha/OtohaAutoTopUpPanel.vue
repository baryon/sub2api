<template>
  <section id="auto-topup" class="card p-5">
    <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('otoha.autoTopUp.title') }}</h2>
    <p class="mt-1 text-sm text-gray-600 dark:text-dark-300">{{ t('otoha.autoTopUp.intro') }}</p>

    <div v-if="loading" class="flex justify-center py-8">
      <div class="h-6 w-6 animate-spin rounded-full border-4 border-primary-500 border-t-transparent"></div>
    </div>

    <div v-else-if="loadError" class="mt-4">
      <p class="text-sm text-red-600 dark:text-red-400">{{ loadError }}</p>
      <button type="button" class="btn btn-secondary mt-3" @click="load">{{ t('otoha.account.retry') }}</button>
    </div>

    <div v-else-if="state" class="mt-4 space-y-5">
      <!-- The system turned it off -->
      <div
        v-if="stopReasonKey"
        class="rounded-lg border border-amber-300 bg-amber-50 p-4 dark:border-amber-700 dark:bg-amber-900/20"
        role="alert"
      >
        <p class="text-sm font-medium text-amber-900 dark:text-amber-200">{{ t('otoha.autoTopUp.stoppedTitle') }}</p>
        <p class="mt-1 text-sm text-amber-800 dark:text-amber-300">{{ t(stopReasonKey) }}</p>
        <p class="mt-1 text-sm text-amber-800 dark:text-amber-300">{{ t('otoha.autoTopUp.stoppedNext') }}</p>
      </div>

      <p v-if="state.card_payments_available === false && !state.card" class="text-sm text-gray-600 dark:text-dark-300">
        {{ t('otoha.autoTopUp.unavailable') }}
      </p>

      <!-- Adding or replacing the card -->
      <div v-if="addingCard" class="space-y-3">
        <p class="text-sm text-gray-700 dark:text-dark-200">{{ t('otoha.autoTopUp.cardFormHint') }}</p>
        <div v-if="cardFormLoading" class="flex justify-center py-6">
          <div class="h-6 w-6 animate-spin rounded-full border-4 border-primary-500 border-t-transparent"></div>
        </div>
        <div ref="cardMount" class="min-h-[120px]"></div>
        <p v-if="cardError" class="text-sm text-red-600 dark:text-red-400">{{ cardError }}</p>
        <div class="flex gap-2">
          <button type="button" class="btn btn-primary" :disabled="!cardReady || savingCard" @click="saveCard">
            {{ savingCard ? t('otoha.autoTopUp.savingCard') : t('otoha.autoTopUp.saveCard') }}
          </button>
          <button type="button" class="btn btn-secondary" :disabled="savingCard" @click="cancelAddCard">
            {{ t('otoha.autoTopUp.cancel') }}
          </button>
        </div>
      </div>

      <!-- The saved card -->
      <div v-else-if="state.card" class="flex flex-wrap items-center justify-between gap-3">
        <div>
          <p class="text-sm font-medium text-gray-900 dark:text-white">{{ cardText }}</p>
          <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('otoha.autoTopUp.cardExpires', { month: expMonth, year: state.card.exp_year }) }}</p>
        </div>
        <div class="flex gap-2">
          <button type="button" class="btn btn-secondary" :disabled="busy" @click="startAddCard">{{ t('otoha.autoTopUp.replaceCard') }}</button>
          <button type="button" class="btn btn-secondary" :disabled="busy" @click="removeCard">{{ t('otoha.autoTopUp.removeCard') }}</button>
        </div>
      </div>

      <div v-else-if="state.card_payments_available !== false">
        <button type="button" class="btn btn-primary" @click="startAddCard">{{ t('otoha.autoTopUp.addCard') }}</button>
        <p class="mt-2 text-xs text-gray-500 dark:text-dark-400">{{ t('otoha.autoTopUp.cardOnly') }}</p>
      </div>

      <p v-if="actionError" class="text-sm text-red-600 dark:text-red-400">{{ actionError }}</p>

      <!-- Settings -->
      <form v-if="state.card && !addingCard" class="space-y-4" @submit.prevent="save">
        <div class="flex items-center justify-between gap-3">
          <label class="text-sm font-medium text-gray-900 dark:text-white" for="auto-topup-enabled">
            {{ t('otoha.autoTopUp.enable') }}
          </label>
          <Toggle id="auto-topup-enabled" v-model="form.enabled" />
        </div>
        <div class="grid gap-4 sm:grid-cols-3">
          <label class="block text-sm text-gray-700 dark:text-dark-200">
            {{ t('otoha.autoTopUp.thresholdLabel') }}
            <input v-model.number="form.threshold" type="number" min="0" step="0.01" class="input mt-1 w-full" />
          </label>
          <label class="block text-sm text-gray-700 dark:text-dark-200">
            {{ t('otoha.autoTopUp.amountLabel') }}
            <input v-model.number="form.amount" type="number" min="0" step="0.01" class="input mt-1 w-full" />
          </label>
          <label class="block text-sm text-gray-700 dark:text-dark-200">
            {{ t('otoha.autoTopUp.monthlyCapLabel') }}
            <input v-model.number="form.monthlyCap" type="number" min="0" step="0.01" class="input mt-1 w-full" />
          </label>
        </div>
        <p class="text-sm text-gray-600 dark:text-dark-300">
          {{ t('otoha.autoTopUp.summary', { threshold: usd(form.threshold), amount: usd(form.amount), cap: usd(form.monthlyCap) }) }}
        </p>
        <p v-if="formError" class="text-sm text-red-600 dark:text-red-400">{{ formError }}</p>
        <p v-if="saved" class="text-sm text-green-700 dark:text-green-400">{{ t('otoha.autoTopUp.saved') }}</p>
        <button type="submit" class="btn btn-primary" :disabled="busy || !!formError">
          {{ saving ? t('otoha.autoTopUp.saving') : t('otoha.autoTopUp.save') }}
        </button>
      </form>

      <!-- This month and the latest top-up -->
      <div v-if="state.card" class="space-y-1 border-t border-gray-100 pt-4 text-sm text-gray-600 dark:border-dark-800 dark:text-dark-300">
        <p>{{ t('otoha.autoTopUp.usedThisMonth', { used: usd(state.used_this_month_usd), cap: usd(state.monthly_cap_usd) }) }}</p>
        <p v-if="lastTopUpText">{{ lastTopUpText }}</p>
        <p v-else>{{ t('otoha.autoTopUp.noTopUpYet') }}</p>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
import { formatPaymentAmount } from '@/components/payment/currency'
import { paymentAPI } from '@/api/payment'
import { otohaAPI, type OtohaAutoTopUp } from '@/api/otoha'
import type { Stripe, StripeElements } from '@stripe/stripe-js'
import {
  AUTO_TOPUP_DEFAULTS,
  autoTopUpErrorKey,
  autoTopUpFormError,
  autoTopUpStopReasonKey,
  cardBrandName,
} from '@/views/otoha/otohaAutoTopUp'

const { t, te, locale } = useI18n()

const emit = defineEmits<{ changed: [state: OtohaAutoTopUp] }>()

const loading = ref(false)
const loadError = ref('')
const state = ref<OtohaAutoTopUp | null>(null)
const limits = reactive({ minAmount: 0, maxAmount: 0 })
const form = reactive({ enabled: false, ...AUTO_TOPUP_DEFAULTS })
const saving = ref(false)
const saved = ref(false)
const actionError = ref('')

const addingCard = ref(false)
const cardFormLoading = ref(false)
const cardReady = ref(false)
const savingCard = ref(false)
const cardError = ref('')
const cardMount = ref<HTMLElement | null>(null)
let stripe: Stripe | null = null
let elements: StripeElements | null = null
let setupId = ''

const busy = computed(() => saving.value || savingCard.value)
const stopReasonKey = computed(() => (state.value && !state.value.enabled ? autoTopUpStopReasonKey(state.value.disabled_reason) : ''))
const expMonth = computed(() => String(state.value?.card?.exp_month ?? '').padStart(2, '0'))
const cardText = computed(() => {
  const card = state.value?.card
  return card ? t('otoha.autoTopUp.cardLabel', { brand: cardBrandName(card.brand) || t('otoha.autoTopUp.cardGeneric'), last4: card.last4 }) : ''
})
const formError = computed(() => {
  const problem = autoTopUpFormError(form, limits)
  if (!problem) return ''
  const params = Object.fromEntries(Object.entries(problem.params ?? {}).map(([k, v]) => [k, usd(v)]))
  return t(problem.key, params)
})
const lastTopUpText = computed(() => {
  const last = state.value?.last_top_up
  if (!last) return ''
  const key = `otoha.autoTopUp.last.${last.result}`
  const date = new Date(last.at)
  return t(te(key) ? key : 'otoha.autoTopUp.last.failed', {
    date: Number.isNaN(date.getTime()) ? '' : date.toLocaleDateString(locale.value),
    amount: usd(last.amount_usd),
  })
})

function usd(amount: number): string {
  return formatPaymentAmount(Number.isFinite(amount) ? amount : 0, 'USD', locale.value)
}

function apply(next: OtohaAutoTopUp) {
  state.value = next
  form.enabled = next.enabled
  form.threshold = next.threshold_usd
  form.amount = next.amount_usd
  form.monthlyCap = next.monthly_cap_usd
  emit('changed', next)
}

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    const [stateRes, checkoutRes] = await Promise.all([otohaAPI.getAutoTopUp(), paymentAPI.getCheckoutInfo().catch(() => null)])
    limits.minAmount = checkoutRes?.data?.global_min ?? 0
    limits.maxAmount = checkoutRes?.data?.global_max ?? 0
    apply(stateRes.data)
  } catch {
    loadError.value = t('otoha.autoTopUp.loadFailed')
  } finally {
    loading.value = false
  }
}

async function save() {
  if (busy.value || formError.value) return
  saving.value = true
  saved.value = false
  actionError.value = ''
  try {
    const { data } = await otohaAPI.updateAutoTopUp({
      enabled: form.enabled,
      threshold_usd: form.threshold,
      amount_usd: form.amount,
      monthly_cap_usd: form.monthlyCap,
    })
    apply(data)
    saved.value = true
  } catch (err: unknown) {
    actionError.value = t(autoTopUpErrorKey(err, 'otoha.autoTopUp.saveFailed'))
  } finally {
    saving.value = false
  }
}

async function startAddCard() {
  actionError.value = ''
  cardError.value = ''
  cardReady.value = false
  addingCard.value = true
  cardFormLoading.value = true
  try {
    const { data } = await otohaAPI.startAutoTopUpCardSetup()
    const { loadStripe } = await import('@stripe/stripe-js/pure')
    stripe = await loadStripe(data.publishable_key)
    if (!stripe) throw new Error('stripe unavailable')
    const isDark = document.documentElement.classList.contains('dark')
    elements = stripe.elements({
      clientSecret: data.client_secret,
      appearance: { theme: isDark ? 'night' : 'stripe', variables: { borderRadius: '8px' } },
    })
    await nextTick()
    if (!cardMount.value) return
    const element = elements.create('payment', { layout: 'tabs' })
    element.mount(cardMount.value)
    element.on('ready', () => { cardReady.value = true })
  } catch (err: unknown) {
    addingCard.value = false
    actionError.value = t(autoTopUpErrorKey(err, 'otoha.autoTopUp.cardFormFailed'))
  } finally {
    cardFormLoading.value = false
  }
}

async function saveCard() {
  if (!stripe || !elements || savingCard.value) return
  savingCard.value = true
  cardError.value = ''
  try {
    const result = await stripe.confirmSetup({
      elements,
      confirmParams: { return_url: window.location.origin + '/otoha/account' },
      redirect: 'if_required',
    })
    if (result.error) {
      cardError.value = result.error.message || t('otoha.autoTopUp.cardNotSaved')
      return
    }
    setupId = result.setupIntent?.id ?? ''
    const { data } = await otohaAPI.finishAutoTopUpCardSetup(setupId)
    apply(data)
    closeCardForm()
  } catch (err: unknown) {
    cardError.value = t(autoTopUpErrorKey(err, 'otoha.autoTopUp.cardNotSaved'))
  } finally {
    savingCard.value = false
  }
}

function closeCardForm() {
  addingCard.value = false
  elements = null
  stripe = null
  cardReady.value = false
}

function cancelAddCard() {
  cardError.value = ''
  closeCardForm()
}

async function removeCard() {
  if (busy.value || !window.confirm(t('otoha.autoTopUp.removeConfirm'))) return
  saving.value = true
  actionError.value = ''
  try {
    const { data } = await otohaAPI.removeAutoTopUpCard()
    apply(data)
  } catch (err: unknown) {
    actionError.value = t(autoTopUpErrorKey(err, 'otoha.autoTopUp.removeFailed'))
  } finally {
    saving.value = false
  }
}

onMounted(load)
onBeforeUnmount(closeCardForm)
</script>
