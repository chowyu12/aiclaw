<script setup lang="ts">
import { actions } from "../store";
import { t } from "../i18n";
import { filename, fileType } from "../file-card";

defineProps<{ paths: string[] }>();
</script>

<template>
  <section class="generated-files" :aria-label="t('生成文件')">
    <div class="artifact-cards">
      <button v-for="path in paths" :key="path" class="file-card" :title="path"
        :aria-label="t('打开文件 {name}', { name: filename(path) })" @click="actions.openFile(path)">
        <span class="file-type" :data-type="fileType(path)" aria-hidden="true">{{ fileType(path) }}</span>
        <span class="file-name">{{ filename(path) }}</span>
      </button>
    </div>
  </section>
</template>

<style scoped>
.generated-files { max-width: var(--content-width); margin: 16px auto; padding: 0 28px; }
.artifact-cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(210px, 100%), 1fr)); gap: 8px; }
</style>
