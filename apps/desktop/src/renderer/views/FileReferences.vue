<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref } from "vue";
import { fileLanguage, previewReference } from "../../shared/file-preview";
import { actions } from "../store";
import { t } from "../i18n";

const props = defineProps<{ html: string; basePath?: string }>();
const menu = ref<HTMLElement>();
const path = ref("");
const position = ref({ top: "0px", left: "0px" });
let trigger: HTMLElement | null = null;

function close(): void { menu.value?.hidePopover(); }
async function activate(event: MouseEvent | KeyboardEvent): Promise<void> {
  const target = event.target as HTMLElement;
  const card = target.closest<HTMLElement>(".file-card");
  // Keep compatibility with plain .file-ref links in older rendered content.
  const reference = target.closest<HTMLElement>(".file-ref");
  if (!card && !reference) return;
  event.preventDefault();
  const raw = (card ?? reference)!.getAttribute("data-file-path") ?? reference?.textContent ?? "";
  const resolved = props.basePath ? previewReference(props.basePath, raw) : raw;
  const opener = target.closest<HTMLElement>(".file-open-trigger");
  if (!opener) { close(); void actions.openFile(resolved); return; }
  if (trigger === opener && menu.value?.matches(":popover-open")) { close(); return; }
  close();
  trigger = opener;
  path.value = resolved;
  const rect = opener.getBoundingClientRect();
  position.value = {
    top: `${Math.max(8, Math.min(rect.bottom + 6, window.innerHeight - 100))}px`,
    left: `${Math.max(8, Math.min(rect.right - 204, window.innerWidth - 212))}px`,
  };
  await nextTick();
  menu.value?.showPopover();
  menu.value?.querySelector<HTMLButtonElement>("button")?.focus();
}
function onKey(event: KeyboardEvent): void {
  if (event.key === " " || event.key === "Enter" || (event.key === "ArrowDown" && (event.target as HTMLElement).closest(".file-open-trigger"))) void activate(event);
}
function menuKey(event: KeyboardEvent): void {
  if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); close(); trigger?.focus(); }
  if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
  event.preventDefault();
  const buttons = [...menu.value!.querySelectorAll<HTMLButtonElement>("button")];
  const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
  buttons[(index + (event.key === "ArrowDown" ? 1 : buttons.length - 1)) % buttons.length]?.focus();
}
function open(system: boolean): void {
  close(); trigger?.focus();
  void (system ? actions.openSystemFile(path.value) : actions.openFile(path.value));
}
onMounted(() => { window.addEventListener("resize", close); document.addEventListener("scroll", close, true); });
onUnmounted(() => { window.removeEventListener("resize", close); document.removeEventListener("scroll", close, true); });
</script>

<template>
  <div class="file-references">
    <div class="file-reference-content" @click="activate" @keydown="onKey" v-html="html" />
    <div ref="menu" popover="auto" class="file-open-menu" role="menu" :aria-label="t('打开方式')" :style="position" @keydown="menuKey">
      <button v-if="fileLanguage(path)" role="menuitem" @click="open(false)">{{ t('在 AIClaw 中预览') }}</button>
      <button role="menuitem" @click="open(true)">{{ t('用系统应用打开') }}</button>
    </div>
  </div>
</template>

<style scoped>
.file-open-menu { position: fixed; margin: 0; width: 204px; max-width: calc(100vw - 16px); padding: 5px; border: 1px solid var(--rule); border-radius: 10px; background: var(--surface); color: var(--ink); box-shadow: 0 8px 28px #0002; }
.file-open-menu button { display: block; width: 100%; padding: 8px 10px; border: 0; border-radius: 6px; background: transparent; color: inherit; text-align: left; font: inherit; font-size: 13px; cursor: pointer; }
.file-open-menu button:hover, .file-open-menu button:focus-visible { background: var(--hover); outline: none; }
</style>
