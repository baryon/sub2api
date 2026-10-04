<template>
  <div class="min-h-screen bg-gray-50 dark:bg-dark-950">
    <header class="border-b border-gray-200 bg-white/80 backdrop-blur dark:border-dark-800 dark:bg-dark-900/80">
      <div class="mx-auto flex max-w-4xl items-center justify-between gap-4 px-4 py-3">
        <router-link to="/otoha/buy" class="text-lg font-bold tracking-tight text-gray-900 dark:text-white">
          {{ t('otoha.brand') }}
        </router-link>
        <nav class="flex items-center gap-3 text-sm">
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
    </header>
    <main class="mx-auto max-w-4xl px-4 py-8">
      <slot />
    </main>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'

const { t } = useI18n()
const router = useRouter()
const authStore = useAuthStore()

async function logout() {
  await authStore.logout()
  await router.push('/otoha/buy')
}
</script>
