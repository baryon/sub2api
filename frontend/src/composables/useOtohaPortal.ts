import { computed } from 'vue'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { isOtohaPortalEnabled, otohaPortalAppliesTo, otohaPortalNav } from '@/router/otohaPortal'

/**
 * Otoha portal state for views (TASK-61): whether the site runs as the portal, whether it shapes what the current
 * viewer sees (visitors and regular users, not admins), and the portal menu.
 */
export function useOtohaPortal() {
  const appStore = useAppStore()
  const authStore = useAuthStore()

  const portalEnabled = computed(() => isOtohaPortalEnabled(appStore.cachedPublicSettings))
  const portalActive = computed(() =>
    otohaPortalAppliesTo({
      portalEnabled: portalEnabled.value,
      isAuthenticated: authStore.isAuthenticated,
      isAdmin: authStore.isAdmin,
    }),
  )
  const portalNav = computed(() => otohaPortalNav(appStore.cachedPublicSettings?.custom_menu_items))

  return { portalEnabled, portalActive, portalNav }
}
