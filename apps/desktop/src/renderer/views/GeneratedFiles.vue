<script setup lang="ts">
import { t } from "../i18n";
import { fileCard } from "../file-card";
import FileReferences from "./FileReferences.vue";

defineProps<{ paths: string[] }>();
</script>

<template>
  <section class="generated-files" :aria-label="t('生成文件')">
    <FileReferences class="artifact-cards" :html="paths.map(fileCard).join('')" />
  </section>
</template>

<style scoped>
.generated-files { max-width: var(--content-width); margin: 16px auto; padding: 0 28px; }
.artifact-cards { border: 1px solid var(--rule); border-radius: 12px; overflow: hidden; }
.artifact-cards :deep(.file-card) { margin: 0; border: 0; border-radius: 0; }
.artifact-cards :deep(.file-card + .file-card) { border-top: 1px solid var(--rule); }
</style>
