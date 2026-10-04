<template>
  <OtohaLayout v-if="portalActive" wide>
    <h1 v-if="portalTitle" class="mb-6 text-2xl font-bold text-gray-900 dark:text-white">{{ portalTitle }}</h1>
    <slot />
  </OtohaLayout>
  <AppLayout v-else>
    <slot />
  </AppLayout>
</template>

<script setup lang="ts">
// Pages shared with the Otoha portal (orders, account settings, custom pages) use the portal's layout for
// visitors and regular users when the portal is on, and the usual layout otherwise (TASK-61). In the portal the
// page heading is the menu entry's name, as the usual layout's header is not there.
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import OtohaLayout from './OtohaLayout.vue'
import { useOtohaPortal } from '@/composables/useOtohaPortal'

const { t } = useI18n()
const route = useRoute()
const { portalActive, portalNav } = useOtohaPortal()

const portalTitle = computed(() => {
  const item = portalNav.value.find((entry) => entry.path === route.path)
  if (!item) return ''
  return item.labelKey ? t(item.labelKey) : item.label ?? ''
})
</script>
