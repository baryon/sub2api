<template>
  <OtohaLayout>
    <!-- Not signed in: what the service is, then register or sign in and come back here -->
    <section v-if="!authStore.isAuthenticated" class="mx-auto max-w-xl py-6 text-center">
      <h1 class="text-3xl font-bold text-gray-900 dark:text-white">{{ t('otoha.start.title') }}</h1>
      <p class="mt-3 text-gray-600 dark:text-dark-300">{{ t('otoha.start.subtitle') }}</p>
      <ul class="mx-auto mt-6 max-w-md space-y-2 text-left text-sm text-gray-700 dark:text-dark-200">
        <li v-for="key in ['point1', 'point2', 'point3']" :key="key" class="flex gap-2">
          <Icon name="checkCircle" size="sm" class="mt-0.5 shrink-0 text-primary-500" />
          <span>{{ t(`otoha.start.${key}`) }}</span>
        </li>
      </ul>
      <div class="mt-8 flex flex-col items-center gap-3 sm:flex-row sm:justify-center">
        <router-link :to="{ path: '/register', query: { redirect: route.fullPath } }" class="btn btn-primary px-6 py-2.5">
          {{ t('otoha.start.register') }}
        </router-link>
        <router-link :to="{ path: '/login', query: { redirect: route.fullPath } }" class="btn btn-secondary px-6 py-2.5">
          {{ t('otoha.start.login') }}
        </router-link>
      </div>
      <p class="mt-4 text-xs text-gray-500 dark:text-dark-400">{{ t('otoha.start.fromApp') }}</p>
    </section>

    <template v-else>
      <div v-if="loading" class="flex justify-center py-16">
        <div class="h-8 w-8 animate-spin rounded-full border-4 border-primary-500 border-t-transparent"></div>
      </div>

      <div v-else-if="loadError" class="card p-6 text-center">
        <p class="text-sm text-red-600 dark:text-red-400">{{ loadError }}</p>
        <button type="button" class="btn btn-secondary mt-4" @click="load">{{ t('otoha.account.retry') }}</button>
      </div>

      <!-- Paying -->
      <section v-else-if="phase !== 'choose' && payment" class="mx-auto max-w-lg space-y-4">
        <h1 class="text-2xl font-bold text-gray-900 dark:text-white">{{ t('otoha.buy.payTitle') }}</h1>
        <p class="text-sm text-gray-600 dark:text-dark-300">{{ t('otoha.buy.payFor', { item: payingLabel }) }}</p>
        <StripePaymentInline
          v-if="phase === 'stripe'"
          :order-id="payment.orderId"
          :amount="payment.amount"
          :client-secret="payment.clientSecret"
          :publishable-key="checkout?.stripe_publishable_key || ''"
          :pay-amount="payment.payAmount"
          :currency="payment.currency"
          :order-type="payment.orderType || undefined"
          return-path="/otoha/result"
          @success="goToResult"
          @done="goToResult"
          @redirect="goToResult"
          @back="backToChoose"
        />
        <PaymentStatusPanel
          v-else-if="phase === 'qr'"
          :order-id="payment.orderId"
          :amount="payment.amount"
          :pay-amount="payment.payAmount"
          :qr-code="payment.qrCode"
          :expires-at="payment.expiresAt"
          :payment-type="payment.paymentType"
          :pay-url="payment.payUrl"
          :order-type="payment.orderType"
          :currency="payment.currency"
          :out-trade-no="payment.outTradeNo"
          :mobile-alipay-deep-link="payment.alipayMobilePrecreateDeepLink"
          @success="goToResult"
          @done="backToChoose"
        />
        <div v-else class="card p-6 text-center text-sm text-gray-600 dark:text-dark-300">
          {{ t('otoha.buy.redirecting') }}
        </div>
      </section>

      <!-- Choose a plan or a top-up -->
      <div v-else class="space-y-8">
        <div>
          <h1 class="text-2xl font-bold text-gray-900 dark:text-white">{{ t('otoha.titles.buy') }}</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">
            {{ t('otoha.buy.signedInAs', { email: account?.email || authStore.user?.email || '' }) }}
          </p>
          <p v-if="account?.plan" class="mt-1 text-sm text-gray-700 dark:text-dark-200">
            {{ t('otoha.buy.currentPlan', { name: account.plan.name, date: formatDate(account.plan.expires_at) }) }}
          </p>
        </div>

        <section v-if="methodOptions.length > 1" class="space-y-3">
          <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('otoha.buy.methodTitle') }}</h2>
          <PaymentMethodSelector :methods="methodOptions" :selected="selectedMethod" @select="selectedMethod = $event" />
        </section>
        <p v-if="methodOptions.length === 0" class="rounded-xl bg-amber-50 p-4 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-200">
          {{ t('otoha.buy.noMethods') }}
        </p>
        <p v-if="payError" class="rounded-xl bg-red-50 p-4 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">
          {{ payError }}
        </p>

        <section class="space-y-3">
          <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('otoha.buy.plansTitle') }}</h2>
          <p v-if="plans.length === 0" class="text-sm text-gray-500 dark:text-dark-400">{{ t('otoha.buy.noPlans') }}</p>
          <div v-else class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <div v-for="plan in plans" :key="plan.id" class="card flex flex-col p-5">
              <h3 class="text-lg font-semibold text-gray-900 dark:text-white">{{ plan.name }}</h3>
              <p class="mt-2 text-2xl font-bold text-gray-900 dark:text-white">
                {{ t('otoha.buy.perPeriod', { price: formatPaymentAmount(plan.price, plan.currency || 'USD'), days: plan.validity_days }) }}
              </p>
              <p v-if="plan.monthly_limit_usd" class="mt-1 text-sm text-primary-700 dark:text-primary-300">
                {{ t('otoha.buy.monthlyQuota', { amount: formatPaymentAmount(plan.monthly_limit_usd, 'USD') }) }}
              </p>
              <p v-if="plan.description" class="mt-2 text-sm text-gray-600 dark:text-dark-300">{{ plan.description }}</p>
              <ul v-if="plan.features?.length" class="mt-3 space-y-1 text-sm text-gray-600 dark:text-dark-300">
                <li v-for="feature in plan.features" :key="feature" class="flex gap-2">
                  <Icon name="check" size="sm" class="mt-0.5 shrink-0 text-primary-500" />
                  <span>{{ feature }}</span>
                </li>
              </ul>
              <div class="mt-auto pt-5">
                <p v-if="planHint(plan)" class="mb-2 text-xs text-gray-500 dark:text-dark-400">{{ planHint(plan) }}</p>
                <button
                  type="button"
                  class="btn btn-primary w-full"
                  :disabled="submitting || methodOptions.length === 0 || actionOf(plan) === 'later'"
                  @click="buyPlan(plan)"
                >
                  {{ submitting && pendingPlanId === plan.id ? t('otoha.buy.paying') : planButtonLabel(plan) }}
                </button>
              </div>
            </div>
          </div>
        </section>

        <section id="topup" class="card space-y-3 p-5">
          <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('otoha.buy.topUpTitle') }}</h2>
          <template v-if="checkout?.balance_disabled">
            <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('otoha.buy.topUpDisabled') }}</p>
          </template>
          <template v-else>
            <p class="text-sm text-gray-600 dark:text-dark-300">{{ t('otoha.buy.topUpHint') }}</p>
            <div class="flex flex-wrap gap-2">
              <button
                v-for="preset in topUpPresets"
                :key="preset"
                type="button"
                class="rounded-lg border px-4 py-2 text-sm"
                :class="topUpAmount === preset
                  ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-200'
                  : 'border-gray-200 text-gray-700 hover:border-gray-300 dark:border-dark-700 dark:text-dark-200'"
                @click="topUpAmount = preset"
              >
                {{ formatPaymentAmount(preset, 'USD') }}
              </button>
            </div>
            <label class="block max-w-xs text-sm text-gray-700 dark:text-dark-200">
              {{ t('otoha.buy.amountLabel') }}
              <input v-model.number="topUpAmount" type="number" min="0" step="1" class="input mt-1 w-full" />
            </label>
            <p v-if="topUpError" class="text-sm text-red-600 dark:text-red-400">{{ topUpError }}</p>
            <button
              type="button"
              class="btn btn-primary"
              :disabled="submitting || !!topUpError || methodOptions.length === 0"
              @click="topUp"
            >
              {{ submitting && pendingPlanId === 0 ? t('otoha.buy.paying') : t('otoha.buy.topUp', { amount: formatPaymentAmount(topUpAmount || 0, 'USD') }) }}
            </button>
          </template>
        </section>
      </div>
    </template>
  </OtohaLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import OtohaLayout from '@/components/otoha/OtohaLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import PaymentMethodSelector, { type PaymentMethodOption } from '@/components/payment/PaymentMethodSelector.vue'
import PaymentStatusPanel from '@/components/payment/PaymentStatusPanel.vue'
import StripePaymentInline from '@/components/payment/StripePaymentInline.vue'
import { buildCreateOrderPayload, decidePaymentLaunch, getVisibleMethods, type PaymentRecoverySnapshot } from '@/components/payment/paymentFlow'
import { formatPaymentAmount } from '@/components/payment/currency'
import { paymentAPI } from '@/api/payment'
import { otohaAPI, type OtohaAccount } from '@/api/otoha'
import { useAuthStore } from '@/stores/auth'
import { isMobileDevice } from '@/utils/device'
import { extractApiErrorMessage, extractI18nErrorMessage } from '@/utils/apiError'
import type { CheckoutInfoResponse, OrderType, SubscriptionPlan } from '@/types/payment'
import { otohaPlans, planAction, topUpAmountError, type PlanAction } from './otohaRules'
import { otohaErrorMessage } from './otohaErrors'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()

const loading = ref(false)
const loadError = ref('')
const account = ref<OtohaAccount | null>(null)
const checkout = ref<CheckoutInfoResponse | null>(null)
const selectedMethod = ref('')
const topUpAmount = ref(50)
const submitting = ref(false)
const pendingPlanId = ref<number | null>(null)
const payError = ref('')
const phase = ref<'choose' | 'stripe' | 'qr' | 'redirect'>('choose')
const payment = ref<PaymentRecoverySnapshot | null>(null)
const payingLabel = ref('')

const plans = computed(() => otohaPlans(checkout.value?.plans, account.value?.group_id ?? 0))

const methodOptions = computed<PaymentMethodOption[]>(() => {
  const methods = getVisibleMethods(checkout.value?.methods ?? {})
  return Object.entries(methods)
    .filter(([, limit]) => limit.available)
    .map(([type, limit]) => ({ type, display_name: limit.display_name, fee_rate: limit.fee_rate, available: limit.available }))
})

const topUpPresets = computed(() => {
  const min = checkout.value?.global_min ?? 0
  const max = checkout.value?.global_max ?? 0
  return [20, 50, 100].filter((v) => !topUpAmountError(v, min, max))
})

const topUpError = computed(() => {
  const min = checkout.value?.global_min ?? 0
  const max = checkout.value?.global_max ?? 0
  switch (topUpAmountError(Number(topUpAmount.value), min, max)) {
    case 'invalid':
      return t('otoha.buy.amountInvalid')
    case 'tooSmall':
      return t('otoha.buy.amountTooSmall', { min: formatPaymentAmount(min, 'USD') })
    case 'tooLarge':
      return t('otoha.buy.amountTooLarge', { max: formatPaymentAmount(max, 'USD') })
    default:
      return ''
  }
})

async function load() {
  if (!authStore.isAuthenticated) return
  loading.value = true
  loadError.value = ''
  try {
    const [accountRes, checkoutRes] = await Promise.all([otohaAPI.getAccount(), paymentAPI.getCheckoutInfo()])
    account.value = accountRes.data
    checkout.value = checkoutRes.data
    const options = methodOptions.value
    const stripe = options.find((m) => m.type === 'stripe')
    selectedMethod.value = (stripe ?? options[0])?.type ?? ''
  } catch (err: unknown) {
    loadError.value = otohaErrorMessage(err, t, 'otoha.account.loadFailed')
  } finally {
    loading.value = false
  }
  if (route.hash === '#topup') {
    await nextTick()
    document.getElementById('topup')?.scrollIntoView({ behavior: 'smooth' })
  }
}

// What buying each plan does for the current plan: renew, upgrade now, or only after the current plan ends.
function actionOf(plan: SubscriptionPlan): PlanAction {
  return planAction(plan, account.value?.plan, plans.value)
}

function planButtonLabel(plan: SubscriptionPlan): string {
  switch (actionOf(plan)) {
    case 'renew':
      return t('otoha.buy.renewPlan')
    case 'upgrade':
      return t('otoha.buy.upgradePlan')
    case 'later':
      return t('otoha.buy.laterPlan')
    default:
      return t('otoha.buy.buyPlan')
  }
}

function planHint(plan: SubscriptionPlan): string {
  const current = account.value?.plan
  if (!current) return ''
  switch (actionOf(plan)) {
    case 'renew':
      return t('otoha.buy.renewHint', { days: plan.validity_days })
    case 'upgrade':
      return t('otoha.buy.upgradeHint', { days: plan.validity_days, name: current.name })
    case 'later':
      return t('otoha.buy.laterHint', { name: current.name, date: formatDate(current.expires_at) })
    default:
      return ''
  }
}

function buyPlan(plan: SubscriptionPlan) {
  if (actionOf(plan) === 'later') return
  payingLabel.value = plan.name
  void startPayment('subscription', plan.price, plan.id)
}

function topUp() {
  if (topUpError.value) return
  payingLabel.value = t('otoha.buy.topUpItem', { amount: formatPaymentAmount(topUpAmount.value, 'USD') })
  void startPayment('balance', Number(topUpAmount.value))
}

async function startPayment(orderType: OrderType, amount: number, planId?: number) {
  if (submitting.value || !selectedMethod.value) return
  submitting.value = true
  pendingPlanId.value = planId ?? 0
  payError.value = ''
  const visibleMethod = selectedMethod.value
  const origin = window.location.origin
  const isMobile = isMobileDevice()
  const forceQRCode = !!(checkout.value?.alipay_force_qrcode && visibleMethod === 'alipay')
  const mobilePrecreateDeepLink = checkout.value?.alipay_mobile_precreate_deep_link === true
  try {
    const payload = buildCreateOrderPayload({
      amount,
      paymentType: visibleMethod,
      orderType,
      planId,
      origin,
      isMobile,
      isWechatBrowser: false,
      forceQRCode,
      mobilePrecreateDeepLink,
    })
    // Providers that send the buyer back land on the Otoha result page (same site).
    payload.return_url = `${origin}/otoha/result`
    const { data: result } = await paymentAPI.createOrder(payload)
    const decision = decidePaymentLaunch(result, {
      visibleMethod,
      orderType,
      isMobile,
      forceQRCode,
      mobilePrecreateDeepLink,
      stripePopupUrl: 'inline',
      stripeRouteUrl: 'inline',
    })
    payment.value = decision.paymentState
    switch (decision.kind) {
      case 'stripe_route':
      case 'stripe_popup':
        phase.value = 'stripe'
        break
      case 'qr_waiting':
      case 'alipay_deep_link':
        phase.value = 'qr'
        break
      case 'redirect_waiting':
        phase.value = 'redirect'
        window.location.href = decision.paymentState.payUrl
        break
      default:
        payment.value = null
        payError.value = t('otoha.buy.unsupportedMethod')
    }
  } catch (err: unknown) {
    const reason = extractI18nErrorMessage(err, t, 'payment.errors', extractApiErrorMessage(err, t('payment.result.failed')))
    payError.value = t('otoha.buy.orderFailed', { reason })
  } finally {
    submitting.value = false
    pendingPlanId.value = null
  }
}

function goToResult() {
  if (!payment.value) return
  void router.push({ path: '/otoha/result', query: { order_id: String(payment.value.orderId) } })
}

function backToChoose() {
  phase.value = 'choose'
  payment.value = null
}

function formatDate(iso: string): string {
  const date = new Date(iso)
  return Number.isNaN(date.getTime()) ? '' : date.toLocaleDateString(locale.value)
}

onMounted(load)
</script>
