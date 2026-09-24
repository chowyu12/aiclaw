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
