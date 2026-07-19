<template>
  <el-container class="app-layout">
    <el-aside :width="isCollapse ? '64px' : '168px'" class="app-aside">
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
            <template #title>{{ i18n.t("app.chat") }}</template>
          </el-menu-item>
          <el-menu-item index="/agents">
            <el-icon><User /></el-icon>
            <template #title>{{ i18n.t("app.agents") }}</template>
          </el-menu-item>
          <el-menu-item index="/runtimes">
            <el-icon><Monitor /></el-icon>
            <template #title>{{ i18n.t("app.runtimes") }}</template>
          </el-menu-item>
          <el-menu-item index="/skill">
            <el-icon><Reading /></el-icon>
            <template #title>{{ i18n.t("app.skills") }}</template>
          </el-menu-item>
          <el-menu-item index="/memories">
            <el-icon><Collection /></el-icon>
            <template #title>{{ i18n.t("app.memories") }}</template>
          </el-menu-item>
          <el-menu-item index="/tools">
            <el-icon><SetUp /></el-icon>
            <template #title>{{ i18n.t("app.tools") }}</template>
          </el-menu-item>
          <el-menu-item index="/providers">
            <el-icon><Connection /></el-icon>
            <template #title>{{ i18n.t("app.providers") }}</template>
          </el-menu-item>
          <el-menu-item index="/mcp">
            <el-icon><Link /></el-icon>
            <template #title>MCP</template>
          </el-menu-item>
          <el-menu-item index="/channels">
            <el-icon><Share /></el-icon>
            <template #title>{{ i18n.t("app.channels") }}</template>
          </el-menu-item>
          <el-menu-item index="/scheduler">
            <el-icon><Timer /></el-icon>
            <template #title>{{ i18n.t("app.scheduler") }}</template>
          </el-menu-item>
          <el-menu-item index="/search-engine">
            <el-icon><Search /></el-icon>
            <template #title>{{ i18n.t("app.searchEngine") }}</template>
          </el-menu-item>

          <el-menu-item index="/logs">
            <el-icon><Document /></el-icon>
            <template #title>{{ i18n.t("app.logs") }}</template>
          </el-menu-item>
        </el-menu>
        <div class="sidebar-footer">
          <template v-if="!isCollapse">
            <div class="sidebar-quick-controls">
              <div class="footer-segment" :aria-label="i18n.t('app.theme')">
                <button
                  type="button"
                  class="footer-toggle-btn"
                  :class="{ active: themeStore.mode === 'light' }"
                  :title="i18n.t('app.light')"
                  @click="themeStore.setMode('light')"
                >
                  <el-icon :size="15"><Sunny /></el-icon>
                </button>
                <button
                  type="button"
                  class="footer-toggle-btn"
                  :class="{ active: themeStore.mode === 'dark' }"
                  :title="i18n.t('app.dark')"
                  @click="themeStore.setMode('dark')"
                >
                  <el-icon :size="15"><Moon /></el-icon>
                </button>
              </div>
              <div class="footer-segment" :aria-label="i18n.t('app.language')">
                <button
                  type="button"
                  class="footer-toggle-btn lang"
                  :class="{ active: i18n.language === 'zh-CN' }"
                  title="中文"
                  @click="i18n.setLanguage('zh-CN')"
                >中</button>
                <button
                  type="button"
                  class="footer-toggle-btn lang"
                  :class="{ active: i18n.language === 'en-US' }"
                  title="English"
                  @click="i18n.setLanguage('en-US')"
                >EN</button>
              </div>
            </div>
            <div class="sidebar-user-line">
              <span class="username">{{
                appVersion ? appVersion : "AiClaw"
              }}</span>
              <el-tooltip :content="i18n.t('app.logoutFull')" placement="top">
                <button
                  type="button"
                  class="footer-logout-btn"
                  @click="handleLogout"
                >
                  <el-icon :size="15"><SwitchButton /></el-icon>
                </button>
              </el-tooltip>
            </div>
          </template>
          <template v-else>
            <div class="sidebar-theme-collapsed">
              <el-tooltip
                :content="themeStore.mode === 'dark' ? i18n.t('app.switchLight') : i18n.t('app.switchDark')"
                placement="right"
              >
                <button
                  type="button"
                  class="footer-toggle-btn single"
                  @click="themeStore.toggleMode()"
                >
                  <el-icon v-if="themeStore.mode === 'dark'" :size="16"
                    ><Sunny
                  /></el-icon>
                  <el-icon v-else :size="16"><Moon /></el-icon>
                </button>
              </el-tooltip>
              <el-tooltip :content="i18n.t('app.language')" placement="right">
                <button
                  type="button"
                  class="footer-toggle-btn single lang"
                  @click="i18n.toggleLanguage()"
                >{{ i18n.language === 'zh-CN' ? '中' : 'EN' }}</button>
              </el-tooltip>
              <el-tooltip :content="i18n.t('app.logoutFull')" placement="right">
                <button
                  type="button"
                  class="footer-logout-btn single"
                  @click="handleLogout"
                >
                  <el-icon :size="16"><SwitchButton /></el-icon>
                </button>
              </el-tooltip>
            </div>
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
import { ref, computed, onMounted } from "vue";
import { useRoute, useRouter } from "vue-router";
import { useAuthStore } from "@/stores/auth";
import { useThemeStore } from "@/stores/theme";
import { useI18nStore } from "@/stores/i18n";
import AiclawLogo from "@/components/brand/AiclawLogo.vue";
import request from "@/api/request";

const route = useRoute();
const router = useRouter();
const authStore = useAuthStore();
const themeStore = useThemeStore();
const i18n = useI18nStore();
const isCollapse = ref(false);
const appVersion = ref("");

onMounted(async () => {
  try {
    const res: any = await request.get("/version");
    appVersion.value = res.data?.version || "";
  } catch {
    appVersion.value = "";
  }
});

const activeMenu = computed(() => {
  const p = route.path;
  if (p === "/" || p === "") return "/chat";
  if (p.startsWith("/agents")) return "/agents";
  if (p.startsWith("/runtimes")) return "/runtimes";
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
  transition: width 0.25s cubic-bezier(0.4, 0, 0.2, 1);
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
  height: 56px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 0 12px;
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
  padding: 6px 8px;
}
.app-menu :deep(.el-menu-item) {
  height: 40px;
  line-height: 40px;
  border-radius: 8px;
  margin-bottom: 2px;
  padding-left: 14px !important;
  font-size: 13px;
  font-weight: 500;
  transition:
    background-color 0.15s,
    color 0.15s;
}
.app-menu :deep(.el-menu-item .el-icon) {
  font-size: 17px;
  margin-right: 10px;
}
.app-menu :deep(.el-menu--collapse .el-menu-item) {
  padding-left: 0 !important;
  justify-content: center;
}
.sidebar-footer {
  flex-shrink: 0;
  padding: 10px 10px 12px;
  border-top: 1px solid color-mix(in srgb, var(--aic-sidebar-border) 70%, transparent);
  background: color-mix(in srgb, var(--aic-sidebar-bg) 92%, transparent);
}
.sidebar-quick-controls {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 8px;
}
.footer-segment {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 2px;
  padding: 2px;
  border-radius: 12px;
  border: 1px solid var(--aic-theme-btn-border);
  background: var(--aic-theme-btn-bg);
}
.footer-toggle-btn,
.footer-logout-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  min-width: 0;
  width: 100%;
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
.footer-toggle-btn:hover,
.footer-logout-btn:hover {
  color: var(--aic-sidebar-icon-hover);
  background: color-mix(in srgb, var(--aic-theme-btn-active-bg) 45%, transparent);
}
.footer-toggle-btn.active {
  color: var(--aic-theme-btn-active-color);
  background: var(--aic-theme-btn-active-bg);
}
.footer-toggle-btn.lang {
  font-size: 11px;
  font-weight: 700;
}
.sidebar-theme-collapsed {
  display: flex;
  flex-direction: column;
  gap: 8px;
  justify-content: center;
  margin-bottom: 0;
}
.footer-toggle-btn.single,
.footer-logout-btn.single {
  width: 100%;
  height: 34px;
  border: 1px solid var(--aic-theme-btn-border);
  background: var(--aic-theme-btn-bg);
}
.sidebar-user-line {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.username {
  font-size: 11px;
  color: var(--aic-sidebar-muted);
  line-height: 1.3;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  opacity: 0.78;
}
.footer-logout-btn {
  width: 30px;
  height: 28px;
  flex-shrink: 0;
  color: var(--el-color-danger);
  background: transparent;
}
.footer-logout-btn:hover {
  color: var(--el-color-danger);
  background: var(--el-color-danger-light-9);
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
