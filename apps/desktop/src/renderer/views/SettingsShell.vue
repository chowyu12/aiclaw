<script setup lang="ts">
import { defineAsyncComponent } from "vue";
import { store } from "../store";
const ThemeView = defineAsyncComponent(() => import("./ThemeView.vue"));
const SettingsView = defineAsyncComponent(() => import("./SettingsView.vue"));
const ProvidersView = defineAsyncComponent(() => import("./ProvidersView.vue"));
const PluginsView = defineAsyncComponent(() => import("./PluginsView.vue"));
const SearchEnginesView = defineAsyncComponent(() => import("./SearchEnginesView.vue"));
const McpView = defineAsyncComponent(() => import("./McpView.vue"));
const MemoryView = defineAsyncComponent(() => import("./MemoryView.vue"));
const SkillsView = defineAsyncComponent(() => import("./SkillsView.vue"));
const UsageView = defineAsyncComponent(() => import("./UsageView.vue"));
const SchedulesView = defineAsyncComponent(() => import("./SchedulesView.vue"));
const ArchivedView = defineAsyncComponent(() => import("./ArchivedView.vue"));

/**
 * 设置几页的路由。
 *
 * 导航在左栏，不在这里——它与会话列表占同一个位置、用同一套样式，
 * 因为对用户来说那就是「这个窗口现在在看什么」的唯一一处开关。
 * 早先在顶部做过一排分栏，结果左右各有一套导航，反而要想一下该点哪边。
 */
</script>

<template>
  <SettingsView v-if="store.view === 'settings'" />
  <ThemeView v-else-if="store.view === 'theme'" />
  <ProvidersView v-else-if="store.view === 'providers'" />
  <SearchEnginesView v-else-if="store.view === 'search'" />
  <PluginsView v-else-if="store.view === 'plugins'" />
  <McpView v-else-if="store.view === 'mcp'" />
  <SkillsView v-else-if="store.view === 'skills'" />
  <MemoryView v-else-if="store.view === 'memory'" />
  <UsageView v-else-if="store.view === 'usage'" />
  <SchedulesView v-else-if="store.view === 'schedules'" />
  <ArchivedView v-else-if="store.view === 'archived'" />
</template>
