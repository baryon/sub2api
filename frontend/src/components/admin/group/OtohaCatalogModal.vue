<template>
  <BaseDialog
    :show="show"
    :title="group ? t('admin.otohaCatalog.titleWithGroup', { name: group.name }) : t('admin.otohaCatalog.title')"
    width="full"
    @close="emit('close')"
  >
    <OtohaCatalogPanel :group="group" :active="show" />

    <template #footer>
      <div class="flex justify-end">
        <button type="button" class="btn btn-secondary" @click="emit('close')">{{ t('common.close') }}</button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { AdminGroup } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import OtohaCatalogPanel from './OtohaCatalogPanel.vue'

defineProps<{
  show: boolean
  group: AdminGroup | null
}>()

const emit = defineEmits<{
  close: []
}>()

const { t } = useI18n()
</script>
