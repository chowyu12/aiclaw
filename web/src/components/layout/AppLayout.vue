<template>
  <el-container class="app-layout">
    <el-aside :width="isCollapse ? '64px' : '220px'" class="app-aside">
      <div class="aside-inner">
        <div class="logo">
          <div
            class="logo-brand"
            :class="{ 'logo-brand--collapsed': isCollapse }"
          >
            <AiclawLogo :compact="isCollapse" size="md" />
          </div>
          <el-icon
            class="collapse-btn"
            :size="20"
            @click="isCollapse = !isCollapse"
          >
            <Fold v-if="!isCollapse" />
            <Expand v-else />
          </el-icon>
        </div>
        <el-menu
          :default-active="activeMenu"
          :collapse="isCollapse"
          router
          class="app-menu"
          background-color="var(--aic-menu-bg)"
          text-color="var(--aic-menu-text)"
          active-text-color="var(--aic-menu-active)"
        >
          <el-menu-item index="/chat">
            <el-icon><ChatDotRound /></el-icon>
            <template #title>对话</template>
          </el-menu-item>
          <el-menu-item index="/skill">
            <el-icon><Reading /></el-icon>
            <template #title>技能</template>
          </el-menu-item>
          <el-menu-item index="/tools">
            <el-icon><SetUp /></el-icon>
            <template #title>工具</template>
          </el-menu-item>
          <el-menu-item index="/providers">
            <el-icon><Connection /></el-icon>
            <template #title>模型</template>
          </el-menu-item>
          <el-menu-item index="/mcp">
            <el-icon><Link /></el-icon>
            <template #title>MCP</template>
          </el-menu-item>
          <el-menu-item index="/settings">
            <el-icon><Setting /></el-icon>
            <template #title>设置</template>
          </el-menu-item>
          <el-menu-item index="/logs">
            <el-icon><Document /></el-icon>
            <template #title>日志</template>
          </el-menu-item>
        </el-menu>
        <div class="sidebar-footer">
          <div v-if="!isCollapse" class="sidebar-theme-row">
            <span class="theme-label">主题</span>
            <div class="theme-switch">
              <button
                type="button"
                class="theme-icon-btn"
                :class="{ active: themeStore.mode === 'light' }"
                title="浅色"
                @click="themeStore.setMode('light')"
              >
                <el-icon :size="16"><Sunny /></el-icon>
              </button>
              <button
                type="button"
                class="theme-icon-btn"
                :class="{ active: themeStore.mode === 'dark' }"
                title="深色"
                @click="themeStore.setMode('dark')"
              >
                <el-icon :size="16"><Moon /></el-icon>
              </button>
            </div>
          </div>
          <div v-else class="sidebar-theme-collapsed">
            <el-tooltip
              :content="themeStore.mode === 'dark' ? '切换浅色' : '切换深色'"
              placement="right"
            >
              <button
                type="button"
                class="theme-icon-btn single"
                @click="themeStore.toggleMode()"
              >
                <el-icon v-if="themeStore.mode === 'dark'" :size="18"
                  ><Sunny
                /></el-icon>
                <el-icon v-else :size="18"><Moon /></el-icon>
              </button>
            </el-tooltip>
          </div>
          <template v-if="!isCollapse">
            <div class="sidebar-user-line">
              <span class="username">Web 已登录</span>
              <el-button text type="danger" size="small" @click="handleLogout"
                >退出</el-button
              >
            </div>
          </template>
          <template v-else>
            <el-tooltip content="退出登录" placement="right">
              <el-button
                class="sidebar-logout-icon"
                text
                type="danger"
                @click="handleLogout"
              >
                <el-icon :size="18"><SwitchButton /></el-icon>
              </el-button>
            </el-tooltip>
          </template>
        </div>
      </div>
    </el-aside>
    <el-container class="app-body">
      <el-main class="app-main">
        <router-view />
      </el-main>
    </el-container>
  </el-container>
</template>

<script setup lang="ts">
import { ref, computed } from "vue";
import { useRoute, useRouter } from "vue-router";
import { useAuthStore } from "@/stores/auth";
import { useThemeStore } from "@/stores/theme";
import AiclawLogo from "@/components/brand/AiclawLogo.vue";

const route = useRoute();
const router = useRouter();
const authStore = useAuthStore();
const themeStore = useThemeStore();
const isCollapse = ref(false);

const activeMenu = computed(() => {
  const p = route.path;
  if (p === "/" || p === "") return "/chat";
  return p;
});

function handleLogout() {
  authStore.logout();
  router.push("/login");
}
</script>

<style>
* {
  margin: 0;
  padding: 0;
  box-sizing: border-box;
}
html,
body,
#app {
  height: 100%;
}
</style>

<style scoped>
.app-layout {
  height: 100vh;
}
.app-aside {
  background-color: var(--aic-sidebar-bg);
  transition: width 0.3s;
  overflow: hidden;
  border-right: 1px solid var(--aic-sidebar-border);
  display: flex;
  flex-direction: column;
}
.aside-inner {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
  height: 100%;
}
.logo {
  flex-shrink: 0;
  height: 60px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 0 10px 0 12px;
  color: var(--aic-sidebar-logo-text);
  font-size: 18px;
  font-weight: 600;
  border-bottom: 1px solid var(--aic-sidebar-border);
}
.logo-brand {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  justify-content: flex-start;
  gap: 8px;
}
.logo-brand--collapsed {
  justify-content: center;
}
.collapse-btn {
  flex-shrink: 0;
  cursor: pointer;
  color: var(--aic-sidebar-icon);
}
.collapse-btn:hover {
  color: var(--aic-sidebar-icon-hover);
}
.app-menu {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  border-right: none;
}
.sidebar-footer {
  flex-shrink: 0;
  padding: 12px 10px 14px;
  border-top: 1px solid var(--aic-sidebar-border);
}
.sidebar-theme-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 10px;
}
.theme-label {
  font-size: 11px;
  color: var(--aic-sidebar-muted);
  font-weight: 600;
  letter-spacing: 0.06em;
}
.theme-switch {
  display: flex;
  gap: 4px;
  padding: 2px;
  border-radius: 10px;
  border: 1px solid var(--aic-theme-btn-border);
  background: var(--aic-theme-btn-bg);
}
.theme-icon-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 28px;
  border: none;
  border-radius: 8px;
  cursor: pointer;
  background: transparent;
  color: var(--aic-theme-btn-color);
  transition:
    color 0.15s,
    background 0.15s;
}
.theme-icon-btn:hover {
  color: var(--aic-sidebar-icon-hover);
}
.theme-icon-btn.active {
  color: var(--aic-theme-btn-active-color);
  background: var(--aic-theme-btn-active-bg);
}
.sidebar-theme-collapsed {
  display: flex;
  justify-content: center;
  margin-bottom: 8px;
}
.theme-icon-btn.single {
  width: 100%;
  height: 36px;
}
.sidebar-user-line {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.username {
  font-size: 12px;
  color: var(--aic-sidebar-muted);
  line-height: 1.3;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.sidebar-logout-icon {
  width: 100%;
  padding: 8px 0;
}
.app-body {
  flex: 1;
  min-width: 0;
  min-height: 0;
}
.app-main {
  background: var(--aic-app-main-bg);
  padding: 0;
  overflow: hidden;
  height: 100%;
  display: flex;
  flex-direction: column;
}
.app-main > :deep(*) {
  flex: 1;
  min-height: 0;
}
/* 管理页：与对话页一致 — 顶栏条 + 下方可滚动内容区铺满，无外层嵌套留白 */
.app-main > :deep(.aic-page) {
  padding: 0;
  overflow: hidden;
  box-sizing: border-box;
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  width: 100%;
  max-width: none;
  margin: 0;
  background: var(--aic-app-main-bg);
}
.app-main > :deep(.aic-page) > .aic-page-head {
  flex-shrink: 0;
  padding: 16px 24px 12px;
  margin-bottom: 0;
  border-bottom: 1px solid var(--aic-page-head-border);
  background: var(--aic-page-head-bg);
}
.app-main > :deep(.aic-page) > .aic-page-head .aic-title {
  font-size: 20px;
}
.app-main > :deep(.aic-page) > .aic-page-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 16px 24px 28px;
  box-sizing: border-box;
}
</style>
