<script setup lang="ts">
import { computed, onMounted, onUnmounted } from "vue";
import { actions, store } from "./store";
import ChatView from "./views/ChatView.vue";
import SettingsShell from "./views/SettingsShell.vue";
import SessionSidebar from "./views/SessionSidebar.vue";
import ApprovalDialog from "./views/ApprovalDialog.vue";

/** 「配完了」= 选好了默认模型（它所属的模型服务在库里带着端点与 Key）。 */
const configured = computed(() => Boolean(store.config?.providerId && store.config.model));

onMounted(async () => {
  await actions.bootstrap();
  // 运行时总是先拉起来：模型服务的清单在内核那边的库里，配置页要靠它才能读写。
  // 开着应用第一件事总是点那个「启动」按钮，那这个按钮就不该存在——
  // 失败时会退回带重试按钮的提示页，不会卡在一个没有出口的界面上。
  await actions.startRuntime();
  if (store.runtime.state !== "ready") return;
  // 没有能用的模型就落到模型服务页，省得用户在对话页对着一个不能用的输入框。
  if (!(await actions.ensureDefaultModel())) {
    actions.setView(store.providers.length === 0 ? "providers" : "settings");
    return;
  }
  await actions.resumeLatest();
});

// 开应用之后查一次，之后每 6 小时一次。
//
// 不做得更勤：这是内部工具，一天发不了几个版本，而一个总在弹的提示条
// 只会被学会无视。启动那次延后几秒，别和拉起运行时抢网络与注意力。
onMounted(() => {
  const timer = setTimeout(() => void actions.checkUpdate(), 8000);
  const interval = setInterval(() => void actions.checkUpdate(), 6 * 60 * 60 * 1000);
  onUnmounted(() => {
    clearTimeout(timer);
    clearInterval(interval);
  });
});

/** 有新版、而且用户没对这个版本说过「以后再说」。 */
const updateReady = computed(
  () =>
    store.update?.hasUpdate === true &&
    store.update.latest !== store.updateDismissed,
);
</script>

<template>
  <!-- 没有顶栏：窗口自己有标题栏，再挂一条就是两层。导航挪进了侧边栏底部的
       设置菜单——MCP、技能和配置都是低频的全局设置，不值得常驻一整行。 -->
  <div class="shell">
    <SessionSidebar />

    <main class="pane">
      <div v-if="store.error" class="error-bar">
        <span>{{ store.error }}</span>
        <button @click="actions.clearError()">知道了</button>
      </div>

      <!-- 升级条。放在错误条下面：出错的时候那条更要紧。 -->
      <div v-if="updateReady" class="update-bar">
        <span>
          <template v-if="store.updateReady">
            新版本 <strong>{{ store.update?.latest }}</strong> 已下载好，重启即可更新
          </template>
          <template v-else-if="store.updateDownloading">
            正在后台下载新版本 <strong>{{ store.update?.latest }}</strong>
            <em v-if="store.updateProgress >= 0">（{{ store.updateProgress }}%）</em>
          </template>
          <template v-else>
            有新版本 <strong>{{ store.update?.latest }}</strong>
            <em>（当前 {{ store.update?.current }}）</em>
          </template>
        </span>
        <button
          v-if="store.update?.canInstall"
          class="go"
          :disabled="store.updating || store.updateDownloading"
          @click="actions.installUpdate()"
        >
          {{
            store.updating
              ? "正在更新…"
              : store.updateDownloading
                ? "下载中…"
                : store.updateReady
                  ? "立即重启更新"
                  : (store.update?.installLabel ?? "升级并重启")
          }}
        </button>
        <button v-else class="go" @click="actions.installUpdate()">打开发布页</button>
        <button @click="actions.dismissUpdate()">以后再说</button>
      </div>

      <div class="content">
        <ChatView v-if="store.view === 'chat'" :configured="configured" />
        <!-- 其余四项是同一个设置页的分栏，外壳带顶部分栏与「返回对话」。 -->
        <SettingsShell v-else />
      </div>
    </main>

    <ApprovalDialog
      v-if="store.approvals.length > 0"
      :request="store.approvals[0]!"
      @respond="
        (accept: boolean, scope?: 'once' | 'session') =>
          actions.respondApproval(String(store.approvals[0]!.id), accept, scope)
      "
    />
  </div>
</template>

<style scoped>
.shell {
  display: flex;
  height: 100vh;
}

.pane {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
  min-height: 0;
}

.content {
  flex: 1;
  min-height: 0;
}

.update-bar {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 16px;
  background: var(--accent-soft);
  color: var(--accent);
  font-size: 12.5px;
  border-bottom: 1px solid var(--rule);
}

.update-bar span {
  flex: 1;
}

.update-bar em {
  font-style: normal;
  color: var(--muted);
}

.update-bar button {
  flex: 0 0 auto;
  white-space: nowrap;
  padding: 4px 11px;
  border: 1px solid var(--rule-strong);
  border-radius: var(--r-sm);
  background: var(--surface);
  color: var(--ink-2);
  font-size: 12px;
  cursor: pointer;
}

.update-bar button.go {
  border-color: var(--accent);
  color: var(--accent);
  font-weight: 500;
}

.update-bar button:disabled {
  opacity: 0.6;
  cursor: default;
}

.error-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 9px 18px;
  background: var(--danger-soft);
  color: var(--danger);
  font-size: 13px;
}

.error-bar span {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  max-height: 84px;
  overflow-y: auto;
}

.error-bar button {
  flex-shrink: 0;
  border: 1px solid currentColor;
  background: transparent;
  color: inherit;
  border-radius: 5px;
  padding: 3px 10px;
  cursor: pointer;
  font-size: 12px;
}
</style>
