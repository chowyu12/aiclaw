<script setup lang="ts">
import { nextTick, ref } from "vue";
import type { SearchSource } from "../../shared/types";
import { t } from "../i18n";

defineProps<{ sources: SearchSource[] }>();
const open = ref(false);
const trigger = ref<HTMLButtonElement>();
const closeButton = ref<HTMLButtonElement>();
function domain(url: string): string { return new URL(url).hostname.replace(/^www\./, ""); }
async function show(): Promise<void> {
  open.value = true;
  await nextTick();
  closeButton.value?.focus();
}
function close(): void { open.value = false; trigger.value?.focus(); }
function onKey(event: KeyboardEvent): void {
  if (event.key === "Escape") { event.preventDefault(); close(); }
  if (event.key !== "Tab") return;
  const elements = [...(event.currentTarget as HTMLElement).querySelectorAll<HTMLElement>("button, a[href]")];
  const first = elements[0], last = elements.at(-1);
  if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
  else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
}
</script>

<template>
  <div class="search-sources">
    <button ref="trigger" class="sources-trigger" :aria-expanded="open" @click="show">
      <span class="source-symbol" aria-hidden="true">◎</span>{{ t('参考来源 · {n}', { n: sources.length }) }}
    </button>
    <Teleport to="body">
      <div v-if="open" class="sources-overlay" @click.self="close">
        <section class="sources-drawer" role="dialog" aria-modal="true" :aria-label="t('参考来源')" @keydown="onKey">
          <header><strong>{{ t('参考来源 · {n}', { n: sources.length }) }}</strong>
            <button ref="closeButton" class="sources-close" :aria-label="t('关闭来源面板')" @click="close">×</button>
          </header>
          <p class="sources-note">{{ t('本轮联网搜索返回的网页，供核对回答。') }}</p>
          <div class="sources-list">
            <a v-for="source in sources" :key="source.url" class="source-card" :href="source.url" target="_blank" rel="noopener noreferrer">
              <span class="source-domain"><span class="site-icon" aria-hidden="true">{{ domain(source.url).slice(0, 1).toUpperCase() }}</span>{{ domain(source.url) }}</span>
              <strong class="source-title">{{ source.title || domain(source.url) }}</strong>
              <span v-if="source.snippet" class="source-snippet">{{ source.snippet }}</span>
            </a>
          </div>
        </section>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.search-sources { max-width: var(--content-width); margin: 12px auto 8px; padding: 0 28px; }
.sources-trigger { display: inline-flex; align-items: center; gap: 7px; border: 1px solid var(--rule); background: var(--surface, white); color: var(--muted); border-radius: 16px; padding: 6px 11px; font-size: 12px; cursor: pointer; }
.sources-trigger:hover { background: var(--accent-soft); color: var(--accent); }
.source-symbol { font-size: 16px; }
.sources-overlay { position: fixed; inset: 0; z-index: 100; background: #00000012; display: flex; justify-content: flex-end; }
.sources-drawer { width: min(360px, 100vw); height: 100%; display: flex; flex-direction: column; background: var(--ground); border-left: 1px solid var(--rule); box-shadow: -8px 0 24px #0000000c; }
.sources-drawer header { display: flex; align-items: center; justify-content: space-between; padding: 18px 20px 6px; font-size: 14px; }
.sources-close { width: 28px; height: 28px; border: 0; border-radius: 6px; background: transparent; color: var(--muted); font-size: 22px; cursor: pointer; }
.sources-close:hover { background: var(--accent-soft); }
.sources-note { margin: 4px 20px 12px; color: var(--muted); font-size: 12px; line-height: 1.6; }
.sources-list { overflow-y: auto; padding: 0 12px 20px; min-height: 0; }
.source-card { display: flex; flex-direction: column; gap: 8px; padding: 14px 8px; text-decoration: none; color: var(--ink); border-radius: 8px; }
.source-card:hover { background: var(--accent-soft); }
.source-domain { display: flex; align-items: center; gap: 7px; font-size: 11px; color: var(--muted); overflow-wrap: anywhere; }
.site-icon { display: inline-flex; align-items: center; justify-content: center; width: 18px; height: 18px; flex-shrink: 0; border-radius: 50%; background: var(--accent-soft); color: var(--accent); font-size: 10px; }
.source-title { font-size: 14px; line-height: 1.5; overflow-wrap: anywhere; }
.source-snippet { color: var(--muted); font-size: 12px; line-height: 1.7; display: -webkit-box; -webkit-line-clamp: 3; -webkit-box-orient: vertical; overflow: hidden; }
.sources-trigger:focus-visible, .sources-close:focus-visible, .source-card:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
</style>
