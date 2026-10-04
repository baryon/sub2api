<template>
  <div class="min-h-screen bg-gray-50 dark:bg-dark-950">
    <header class="border-b border-gray-200 bg-white/80 backdrop-blur dark:border-dark-800 dark:bg-dark-900/80">
      <div class="mx-auto flex items-center justify-between gap-4 px-4 py-3" :class="widthClass">
        <router-link :to="brandTarget" class="text-lg font-bold tracking-tight text-gray-900 dark:text-white">
          {{ t('otoha.brand') }}
        </router-link>
        <!-- Otoha portal: the menu sits in its own row below; here only account actions -->
        <nav v-if="portalEnabled" class="flex items-center gap-3 text-sm">
          <router-link
            v-if="authStore.isAdmin"
            to="/admin/dashboard"
            class="text-gray-600 hover:text-gray-900 dark:text-dark-300 dark:hover:text-white"
          >
            {{ t('otoha.nav.admin') }}
          </router-link>
          <router-link
            v-if="!authStore.isAuthenticated"
            :to="{ path: '/login', query: { redirect: route.fullPath } }"
            class="text-gray-600 hover:text-gray-900 dark:text-dark-300 dark:hover:text-white"
          >
            {{ t('home.login') }}
          </router-link>
          <button
            v-if="authStore.isAuthenticated"
            type="button"
            class="text-gray-500 hover:text-gray-900 dark:text-dark-400 dark:hover:text-white"
            @click="logout"
          >
            {{ t('otoha.nav.logout') }}
          </button>
          <LocaleSwitcher />
        </nav>
        <nav v-else class="flex items-center gap-3 text-sm">
          <router-link
            to="/otoha/buy"
            class="text-gray-600 hover:text-gray-900 dark:text-dark-300 dark:hover:text-white"
            active-class="font-semibold text-gray-900 dark:text-white"
          >
            {{ t('otoha.nav.buy') }}
          </router-link>
          <router-link
            v-if="authStore.isAuthenticated"
            to="/otoha/account"
            class="text-gray-600 hover:text-gray-900 dark:text-dark-300 dark:hover:text-white"
            active-class="font-semibold text-gray-900 dark:text-white"
          >
            {{ t('otoha.nav.account') }}
          </router-link>
          <button
            v-if="authStore.isAuthenticated"
            type="button"
            class="text-gray-500 hover:text-gray-900 dark:text-dark-400 dark:hover:text-white"
            @click="logout"
          >
            {{ t('otoha.nav.logout') }}
          </button>
          <LocaleSwitcher />
        </nav>
      </div>
      <nav
        v-if="portalEnabled && authStore.isAuthenticated"
        :aria-label="t('otoha.nav.menu')"
        class="mx-auto flex gap-1 overflow-x-auto px-2 text-sm"
        :class="widthClass"
      >
        <router-link
          v-for="item in portalNav"
          :key="item.path"
          :to="item.path"
          class="shrink-0 whitespace-nowrap border-b-2 px-3 pb-2 pt-1"
          :class="isActive(item.path)
            ? 'border-primary-500 font-semibold text-gray-900 dark:text-white'
            : 'border-transparent text-gray-600 hover:text-gray-900 dark:text-dark-300 dark:hover:text-white'"
        >
          {{ item.labelKey ? t(item.labelKey) : item.label }}
        </router-link>
      </nav>
    </header>
    <main class="mx-auto px-4 py-8" :class="widthClass">
      <slot />
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import { useOtohaPortal } from '@/composables/useOtohaPortal'
import { OTOHA_PORTAL_BUY, OTOHA_PORTAL_HOME } from '@/router/otohaPortal'

const props = defineProps<{
  /** Room for tables (orders, usage, account settings); the portal always uses it. */
  wide?: boolean
}>()

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const { portalEnabled, portalNav } = useOtohaPortal()

// The portal's pages share one width so the menu does not shift between them.
const widthClass = computed(() => (props.wide || portalEnabled.value ? 'max-w-5xl' : 'max-w-4xl'))
const brandTarget = computed(() =>
  portalEnabled.value && authStore.isAuthenticated ? OTOHA_PORTAL_HOME : OTOHA_PORTAL_BUY,
)

function isActive(path: string): boolean {
  return route.path === path || route.path.startsWith(`${path}/`)
}

async function logout() {
  await authStore.logout()
  await router.push('/otoha/buy')
}
</script>
