<script setup lang="ts">
import { onMounted } from "vue";
import { actions, store } from "../store";

/**
 * 技能管理：这台机器上所有能用的技能，一个列表。
 *
 * 自己目录里的能删能关；Claude Code / Codex / npm 里的只能关——
 * 那是人家的目录，在这里删掉会让那边也一起没了。
 */

onMounted(() => void actions.refreshSkills());

async function remove(id: string, name: string): Promise<void> {
  if (!confirm(`删除技能「${name}」？目录会从本机移除，不可恢复。`)) return;
  await actions.deleteSkill(id);
}
</script>

<template>
  <div class="page">
    <section>
      <header>
        <h2>技能</h2>
        <p class="sub">
          一个技能就是一个目录，里面放 <code>SKILL.md</code>：开头写 name 与 description，
          下面写清楚什么时候用、怎么做。模型只在系统提示词里看到名字与用途，
          判断用得上时才把正文取出来——所以 description 要写清楚<strong>什么时候</strong>用。
        </p>
        <p class="sub">
          这里会一并列出 Claude Code（<code>~/.claude/skills</code>）、Codex
          （<code>~/.codex/skills</code>）、当前工作目录的 <code>.claude/skills</code>、
          npm 全局包里的技能——它们是同一种格式，没必要在这里再装一遍。
          别处的技能可以关掉但删不了，去它自己的位置删。
          同名时按这个顺序取第一个：AIClaw &gt; 项目 &gt; Claude Code &gt; Codex &gt; npm。
        </p>
      </header>

      <div class="actions">
        <button class="ghost" @click="actions.openSkillsDir()">打开技能目录</button>
        <button class="ghost" @click="actions.refreshSkills()">刷新</button>
      </div>

      <p v-if="store.skills.length === 0" class="note">
        还没有技能。点「打开技能目录」，在里面建一个子目录放 SKILL.md。
      </p>

      <article v-for="skill in store.skills" :key="skill.id" class="card" :class="{ off: !skill.enabled }">
        <div class="card-head">
          <div class="who">
            <span class="name">{{ skill.name }}</span>
            <span class="dir" :title="skill.dir">{{ skill.dirName }}</span>
            <span class="badge" :class="{ external: !skill.writable }">{{ skill.source }}</span>
          </div>
          <label class="toggle">
            <input
              type="checkbox"
              :checked="skill.enabled"
              @change="actions.toggleSkill(skill.id, ($event.target as HTMLInputElement).checked)"
            />
            启用
          </label>
          <!-- 别处目录里的技能不给删按钮：那是人家 Claude Code / Codex 的东西，
               在这里删掉会让那边也一起没了。 -->
          <button
            v-if="skill.writable"
            class="icon"
            title="删除"
            @click="remove(skill.id, skill.name)"
          >
            ×
          </button>
        </div>
        <p class="desc">{{ skill.description || "（没写 description——模型无从判断什么时候该用它）" }}</p>
      </article>
    </section>
  </div>
</template>

<style scoped>
.page {
  height: 100%;
  overflow-y: auto;
  padding: 28px 28px 64px;
}

section {
  display: flex;
  flex-direction: column;
  gap: 14px;
  max-width: 660px;
  margin: 0 auto 26px;
}

header {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

h2 {
  margin: 0;
  font-size: 15px;
  font-weight: 650;
}

.sub {
  margin: 0;
  color: var(--muted);
  font-size: 12px;
  line-height: 1.75;
}

.actions {
  display: flex;
  gap: 8px;
  align-items: center;
}

.card {
  display: flex;
  flex-direction: column;
  gap: 7px;
  padding: 14px 16px;
  border: 1px solid var(--rule);
  border-radius: var(--r-lg);
  background: var(--surface);
  box-shadow: var(--shadow-1);
}

/* 停用的技能压暗但不隐藏：让用户看得见自己关了什么。 */
.card.off {
  opacity: 0.55;
}

.card-head {
  display: flex;
  align-items: center;
  gap: 10px;
}

.who {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: baseline;
  gap: 8px;
}

.name {
  font-size: 13.5px;
  font-weight: 550;
}

.dir {
  color: var(--muted);
  font-family: var(--mono);
  font-size: 11px;
}

.badge {
  padding: 1px 7px;
  border-radius: var(--r-full);
  background: var(--accent-soft);
  color: var(--accent);
  font-size: 10.5px;
  white-space: nowrap;
}

/* 别处来的用中性色：强调色留给「这是我们自己的」。 */
.badge.external {
  background: var(--surface-2);
  color: var(--muted);
}

.toggle {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: 12px;
  color: var(--ink-2);
  cursor: pointer;
  /* 不加这条「启用」会被挤到复选框下面一行。 */
  white-space: nowrap;
}

.toggle input {
  width: auto;
}

.icon {
  flex: 0 0 auto;
  width: 24px;
  height: 24px;
  border: none;
  border-radius: var(--r-sm);
  background: none;
  color: var(--muted);
  font-size: 15px;
  line-height: 1;
  cursor: pointer;
}

.icon:hover {
  background: var(--danger-soft);
  color: var(--danger);
}

.desc {
  margin: 0;
  color: var(--ink-2);
  font-size: 12.5px;
  line-height: 1.7;
}

.note {
  margin: 0;
  color: var(--muted);
  font-size: 12px;
  line-height: 1.7;
}

.note.warn {
  color: var(--danger);
  background: var(--danger-soft);
  padding: 10px 13px;
  border-radius: var(--r-md);
}
</style>
