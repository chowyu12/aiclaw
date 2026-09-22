<script setup lang="ts">
import { onMounted, ref } from "vue";
import type { ApprovalPayload } from "../../shared/types";

defineProps<{ request: ApprovalPayload }>();
const emit = defineEmits<{ respond: [approved: boolean, scope?: "once" | "session"] }>();

const denyButton = ref<HTMLButtonElement | null>(null);

// 默认焦点在「拒绝」。没有沙箱之后审批是最后一道闸，回车不该等于同意。
onMounted(() => denyButton.value?.focus());

const KIND_LABEL: Record<ApprovalPayload["kind"], string> = {
  exec: "执行命令",
  write: "写入文件",
  tool: "调用外部工具",
};

/**
 * 警告按类型说实话。
 *
 * 原来不分类型都写「这会直接在你的电脑上执行，没有沙箱隔离」——对一次打到
 * MCP 的只读查询来说那句话是错的。弹窗上写错话比不写更糟：用户核对几次发现
 * 对不上，之后就不看了。
 */
const KIND_WARNING: Record<ApprovalPayload["kind"], string> = {
  exec: "这会直接在你的电脑上执行，不受沙箱限制。",
  write: "这会写入你电脑上的文件。",
  tool: "这个工具可能会改变外部系统的状态。只读的查询不会问你。",
};
</script>

<template>
  <div class="backdrop">
    <div class="card" role="dialog" aria-modal="true">
      <div class="head">
        <span class="badge">{{ KIND_LABEL[request.kind] }}</span>
        <h2>{{ request.title }}</h2>
      </div>

      <p v-if="request.reason" class="reason">{{ request.reason }}</p>

      <pre class="detail">{{ request.detail }}</pre>

      <dl v-if="request.cwd">
        <dt>工作目录</dt>
        <dd>{{ request.cwd }}</dd>
      </dl>

      <p class="warn">{{ KIND_WARNING[request.kind] }}</p>

      <!-- 有目录范围时多给一档：没设工作区的会话里，每写一个文件都问一次，
           用户很快会被训练成闭眼点「允许」——那比少问一次危险。按目录批准是
           折中：范围说得清楚，一次点击覆盖接下来的一串写。 -->
      <p v-if="request.scopePath" class="scope">
        本次会话内可以整个目录放行：<code>{{ request.scopePath }}</code>
      </p>

      <div class="buttons">
        <button ref="denyButton" class="deny" @click="emit('respond', false)">拒绝</button>
        <button
          v-if="request.scopePath"
          class="ghost"
          @click="emit('respond', true, 'session')"
        >
          本次会话都允许这个目录
        </button>
        <button class="allow" @click="emit('respond', true, 'once')">允许本次</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.scope {
  margin: 0;
  color: var(--muted);
  font-size: 11.5px;
  line-height: 1.6;
}

.scope code {
  font-family: var(--mono);
  word-break: break-all;
}

.backdrop {
  position: fixed;
  inset: 0;
  background: rgba(10, 14, 16, 0.55);
  display: grid;
  place-items: center;
  padding: 24px;
  z-index: 50;
}

.card {
  background: var(--surface);
  border-radius: 10px;
  border: 1px solid var(--rule);
  box-shadow: 0 20px 60px -20px rgba(0, 0, 0, 0.5);
  width: min(600px, 100%);
  padding: 22px 24px;
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.head {
  display: flex;
  align-items: center;
  gap: 10px;
}

.badge {
  font-family: var(--mono);
  font-size: 10.5px;
  letter-spacing: 0.08em;
  color: var(--warn);
  background: var(--warn-soft);
  border-radius: 3px;
  padding: 2px 7px;
}

h2 {
  font-size: 15px;
  margin: 0;
}

.reason {
  margin: 0;
  font-size: 13px;
  color: var(--muted);
  line-height: 1.7;
}

.detail {
  margin: 0;
  font-family: var(--mono);
  font-size: 12.5px;
  line-height: 1.7;
  background: var(--ground);
  border: 1px solid var(--rule);
  border-radius: 6px;
  padding: 12px 14px;
  max-height: 260px;
  overflow: auto;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

dl {
  margin: 0;
  display: grid;
  grid-template-columns: 72px 1fr;
  gap: 4px 12px;
  font-size: 12.5px;
}

dt {
  color: var(--muted);
}

dd {
  margin: 0;
  font-family: var(--mono);
  overflow-wrap: anywhere;
}

.warn {
  margin: 0;
  font-size: 12px;
  color: var(--danger);
}

.buttons {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  margin-top: 4px;
}

.deny,
.allow {
  border-radius: 6px;
  padding: 8px 18px;
  font-size: 13px;
  cursor: pointer;
  border: 1px solid var(--rule);
}

.deny {
  background: var(--surface-2);
  color: var(--ink);
}

.deny:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}

.allow {
  background: transparent;
  color: var(--muted);
}

.allow:hover {
  color: var(--ink);
}
</style>
