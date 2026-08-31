<script setup lang="ts">
import { FileText, Image } from 'lucide-vue-next'
import type { ProjectAssistantAttachmentReceipt } from './types'

defineProps<{
  attachments: ProjectAssistantAttachmentReceipt[]
}>()
</script>

<template>
  <div v-if="attachments.length" class="flex max-w-full flex-wrap justify-end gap-1.5" aria-label="Attachments">
    <div
      v-for="attachment in attachments"
      :key="attachment.id"
      class="inline-flex min-w-0 max-w-full items-center gap-1.5 rounded-sm border border-border-subtle bg-surface-raised px-2 py-1 text-[11px] font-mono text-text-secondary"
      :title="`${attachment.contentType} · ${attachment.sizeBytes} bytes`"
    >
      <Image v-if="attachment.contentType.startsWith('image/')" class="h-3.5 w-3.5 shrink-0 text-accent" :stroke-width="1.75" />
      <FileText v-else class="h-3.5 w-3.5 shrink-0 text-accent" :stroke-width="1.75" />
      <span class="max-w-56 truncate">{{ attachment.filename }}</span>
    </div>
  </div>
</template>

