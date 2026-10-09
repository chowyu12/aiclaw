<script setup lang="ts">
import { onMounted, onUnmounted, reactive, ref, watch } from "vue";
import { actions } from "../store";
import { describeError } from "../errors";
import { t } from "../i18n";

const props = defineProps<{ paths: string[] }>();
defineEmits<{ loaded: [] }>();
const root = ref<HTMLElement | null>(null);
const visible = ref(false);
const viewer = ref<HTMLDialogElement | null>(null);
const selected = ref("");
const media = reactive<Record<string, { dataUrl?: string; error?: string }>>({});
let observer: IntersectionObserver | undefined;

function preview(path: string): void {
  selected.value = path;
  viewer.value?.showModal();
}

function closeBackdrop(event: MouseEvent): void {
  if (event.target === viewer.value) viewer.value?.close();
}

// Load only turns that enter the viewport, including restored history.
onMounted(() => {
  observer = new IntersectionObserver(entries => {
    if (entries.some(entry => entry.isIntersecting)) {
      visible.value = true;
      observer?.disconnect();
    }
  });
  if (root.value) observer.observe(root.value);
});
onUnmounted(() => observer?.disconnect());
watch(() => [visible.value, props.paths] as const, () => {
  if (!visible.value) return;
  for (const path of props.paths) {
    if (media[path]) continue;
    media[path] = {};
    void actions.readMedia(path).then(file => {
      media[path] = file.kind === "image" ? { dataUrl: file.dataUrl } : { error: t("图片无法显示") };
    }).catch(error => { media[path] = { error: describeError(error) }; });
  }
}, { deep: true });
</script>

<template>
  <div ref="root" class="generated-images" :class="{ multiple: paths.length > 1 }">
    <figure v-for="path in paths" :key="path">
      <button v-if="media[path]?.dataUrl && !media[path]?.error" class="image-preview" :aria-label="t('查看大图')" @click="preview(path)">
        <img :src="media[path]!.dataUrl" :alt="path" @load="$emit('loaded')" @error="media[path] = { error: t('图片无法显示') }" />
      </button>
      <p v-else-if="media[path]?.error" class="image-error" role="status">{{ media[path]!.error }}</p>
      <p v-else class="image-loading">{{ t("正在读取…") }}</p>
      <figcaption><button class="image-path" @click="actions.openFile(path)">{{ path }}</button></figcaption>
    </figure>
    <dialog ref="viewer" class="image-viewer" :aria-label="t('查看大图')" @click="closeBackdrop" @close="selected = ''">
      <button class="viewer-close" :aria-label="t('关闭图片预览')" @click="viewer?.close()">×</button>
      <img v-if="selected" :src="media[selected]?.dataUrl" :alt="selected" />
      <p>{{ selected }}</p>
    </dialog>
  </div>
</template>

<style scoped>
.generated-images { display: flex; flex-wrap: wrap; gap: 16px; max-width: var(--content-width); margin: 12px auto 20px; padding: 0 28px; }
figure { margin: 0; max-width: 100%; }
.image-preview { display: block; max-width: 100%; padding: 0; border: 0; background: transparent; cursor: zoom-in; }
.image-preview img { display: block; max-width: min(480px, 100%); max-height: 480px; width: auto; height: auto; border-radius: 12px; }
.multiple figure { width: min(180px, 100%); }
.multiple .image-preview { width: 100%; }
.multiple .image-preview img { width: 100%; height: 180px; object-fit: contain; border-radius: 8px; }
.image-path { margin-top: 7px; padding: 0; border: 0; background: transparent; color: var(--muted); font-size: 12px; overflow-wrap: anywhere; text-align: left; cursor: pointer; }
.image-path:hover { text-decoration: underline; }
.image-loading, .image-error { margin: 8px 0; font-size: 13px; color: var(--muted); }
.image-error { color: var(--danger); }
.image-viewer { max-width: 92vw; max-height: 92vh; padding: 40px 16px 12px; border: 0; border-radius: 14px; background: var(--surface); color: var(--ink); }
.image-viewer::backdrop { background: rgb(0 0 0 / 65%); }
.image-viewer img { display: block; max-width: 86vw; max-height: 76vh; margin: auto; object-fit: contain; }
.image-viewer p { margin: 8px 0 0; overflow-wrap: anywhere; font-size: 12px; color: var(--muted); }
.viewer-close { position: absolute; top: 8px; right: 10px; padding: 2px 8px; border: 0; background: transparent; color: inherit; font-size: 24px; cursor: pointer; }
</style>
