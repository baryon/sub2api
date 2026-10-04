<template>
  <AppLayout>
    <div class="space-y-4" data-testid="otoha-catalog-page">
      <div class="card p-4 sm:p-6">
        <div class="flex flex-wrap items-end gap-4">
          <label class="block w-full sm:w-96">
            <span class="input-label">{{ t('admin.otohaCatalog.page.group') }}</span>
            <select
              v-model="selectedId"
              class="input w-full"
              :disabled="loading || options.length === 0"
              data-testid="otoha-catalog-group"
            >
              <option :value="null" disabled>{{ t('admin.otohaCatalog.page.chooseGroup') }}</option>
              <option v-for="option in options" :key="option.id" :value="option.id">
                {{ optionLabel(option) }}
              </option>
            </select>
          </label>
          <p class="min-w-0 flex-1 text-sm text-gray-600 dark:text-gray-400">{{ t('admin.otohaCatalog.page.intro') }}</p>
        </div>

        <div v-if="loading" class="mt-4 text-sm text-gray-500">
          <Icon name="refresh" size="sm" class="mr-1 inline animate-spin" />{{ t('admin.otohaCatalog.page.loading') }}
        </div>
        <div
          v-else-if="loadFailed"
          class="mt-4 flex flex-wrap items-center gap-3 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-800 dark:bg-red-900/20 dark:text-red-300"
        >
          <span>{{ t('admin.otohaCatalog.page.loadFailed') }}</span>
          <button type="button" class="btn btn-secondary btn-sm" @click="load">{{ t('admin.otohaCatalog.page.retry') }}</button>
        </div>
        <p
          v-else-if="otohaGroupId === 0"
          class="mt-4 rounded-md bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-300"
        >
          {{ t('admin.otohaCatalog.page.noOtohaGroup') }}
        </p>
        <p
          v-else-if="!otohaGroupListed"
          class="mt-4 rounded-md bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-300"
        >
          {{ t('admin.otohaCatalog.page.otohaGroupMissing') }}
        </p>
        <p
          v-else-if="selected && !selected.isOtoha"
          class="mt-4 rounded-md bg-blue-50 px-3 py-2 text-sm text-blue-700 dark:bg-blue-900/20 dark:text-blue-300"
        >
          {{ t('admin.otohaCatalog.page.notOtohaGroup') }}
        </p>
      </div>

      <div v-if="selected" class="card p-4 sm:p-6">
        <OtohaCatalogPanel :group="selected" />
      </div>
      <div
        v-else-if="!loading && !loadFailed"
        class="card px-4 py-10 text-center text-sm text-gray-500 dark:text-gray-400"
      >
        {{ options.length === 0 ? t('admin.otohaCatalog.page.noGroups') : t('admin.otohaCatalog.page.pickGroup') }}
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { adminAPI } from '@/api/admin'
import type { AdminGroup } from '@/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import OtohaCatalogPanel from '@/components/admin/group/OtohaCatalogPanel.vue'
import { platformLabel } from '@/utils/platformColors'
import { catalogGroupOptions, initialCatalogGroupId, type CatalogGroupOption } from './otohaCatalogPage'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

const loading = ref(true)
const loadFailed = ref(false)
const groups = ref<AdminGroup[]>([])
const otohaGroupId = ref(0)
const selectedId = ref<number | null>(null)

const options = computed(() => catalogGroupOptions(groups.value, otohaGroupId.value))
const otohaGroupListed = computed(() => options.value.some((o) => o.isOtoha))
const selected = computed(() => {
  const option = options.value.find((o) => o.id === selectedId.value)
  if (!option) return null
  const group = groups.value.find((g) => g.id === option.id)
  return { ...option, rate_multiplier: group?.rate_multiplier }
})

function optionLabel(option: CatalogGroupOption): string {
  const label = `${option.name} · ${platformLabel(option.platform)}`
  return option.isOtoha ? t('admin.otohaCatalog.page.otohaGroupOption', { group: label }) : label
}

async function load() {
  loading.value = true
  loadFailed.value = false
  try {
    const [settings, all] = await Promise.all([adminAPI.otohaCatalog.getSettings(), adminAPI.groups.getAll()])
    otohaGroupId.value = settings.otoha_group_id
    groups.value = all
    const requested = typeof route.query.group === 'string' ? route.query.group : undefined
    selectedId.value = initialCatalogGroupId(all, settings.otoha_group_id, requested)
  } catch (error) {
    console.error('Failed to load the Otoha catalog page:', error)
    loadFailed.value = true
  } finally {
    loading.value = false
  }
}

// The address keeps the chosen group, so a reload or a shared link opens the same one.
watch(selectedId, (id) => {
  const current = typeof route.query.group === 'string' ? route.query.group : undefined
  const next = id ? String(id) : undefined
  if (current !== next) {
    router.replace({ query: { ...route.query, group: next } })
  }
})

onMounted(load)
</script>
