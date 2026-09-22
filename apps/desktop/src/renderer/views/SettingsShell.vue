<script setup lang="ts">
import { store } from "../store";
import SettingsView from "./SettingsView.vue";
import CapabilitiesView from "./CapabilitiesView.vue";
import McpView from "./McpView.vue";
import SkillsView from "./SkillsView.vue";

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
  <CapabilitiesView v-else-if="store.view === 'plugins'" />
  <McpView v-else-if="store.view === 'mcp'" />
  <SkillsView v-else-if="store.view === 'skills'" />
</template>
