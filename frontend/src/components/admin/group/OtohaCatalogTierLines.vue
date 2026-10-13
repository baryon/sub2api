<template>
  <ul class="space-y-0.5" data-testid="otoha-catalog-tier-lines">
    <li v-for="(line, index) in lines" :key="index" class="leading-snug">
      <span class="text-gray-500 dark:text-gray-400">{{ rangeLabel(line) }}</span>
      <span class="ml-2 whitespace-nowrap font-medium text-gray-900 dark:text-white">
        {{ amount(line.input) }} / {{ amount(line.output) }}
      </span>
      <span v-if="line.cachedInput !== undefined" class="ml-1 whitespace-nowrap text-gray-500 dark:text-gray-400">
        · {{ t('admin.otohaCatalog.editor.tiers.cachedShort', { price: amount(line.cachedInput) }) }}
      </span>
    </li>
  </ul>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { PriceTierLine } from '@/views/admin/otohaCatalogPage'

defineProps<{
  lines: PriceTierLine[]
}>()

const { t } = useI18n()

function amount(value: number): string {
  return '$' + String(Math.round(value * 1e6) / 1e6)
}

function rangeLabel(line: PriceTierLine): string {
  return t('admin.otohaCatalog.editor.tiers.range.' + line.range.kind, { tokens: line.range.tokens })
}
</script>
