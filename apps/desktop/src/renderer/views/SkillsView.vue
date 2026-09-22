<script setup lang="ts">
import { computed, onMounted } from "vue";
import { actions, store } from "../store";

/**
 * 技能管理：这台机器上所有能用的技能，按来源分组。
 *
 * 分组就是目录：自己的 ~/.aiclaw/skills、插件带的、项目里的 .claude/skills、
 * Claude Code、Codex、npm 全局包。同名技能只保留优先级最高那组里的一个，
 * 所以组的顺序也是「同名时谁说了算」的顺序。
 *
 * 自己目录里的能删能关；别处的只能关——那是人家的目录，在这里删掉会让那边也一起没了。
 */

type SkillRow = (typeof store.skills)[number];

interface SkillGroup {
  source: string;
  /** 这一组技能所在的目录，给人看「它们在哪儿」。 */
  dir: string;
  writable: boolean;
  skills: SkillRow[];
}

/** 组的顺序，与主进程 skill-roots.ts 里的优先级一致。插件来源不止一个，按前缀归到同一位。 */
const ORDER = ["AIClaw", "插件", "项目", "Claude Code", "Codex", "npm 全局"];

function rank(source: string): number {
  const index = ORDER.findIndex((label) => source === label || source.startsWith(`${label} `));
  return index < 0 ? ORDER.length : index;
}

/** 技能目录的上一级，就是这组的根。渲染层没有 node 的 path，按两种分隔符手切。 */
function parentDir(dir: string): string {
  const cut = Math.max(dir.lastIndexOf("/"), dir.lastIndexOf("\\"));
  return cut > 0 ? dir.slice(0, cut) : dir;
}

const groups = computed<SkillGroup[]>(() => {
  const byLabel = new Map<string, SkillGroup>();
  for (const skill of store.skills) {
    const group = byLabel.get(skill.source);
    if (group) {
      group.skills.push(skill);
      // 同一来源的技能不一定在同一个目录下（npm 全局有好几个 node_modules），
      // 目录只在全组一致时显示，否则留空。
      if (group.dir !== parentDir(skill.dir)) group.dir = "";
    } else {
      byLabel.set(skill.source, {
        source: skill.source,
        dir: parentDir(skill.dir),
        writable: skill.writable,
        skills: [skill],
      });
    }
  }
  return [...byLabel.values()].sort(
    (a, b) => rank(a.source) - rank(b.source) || a.source.localeCompare(b.source, "zh"),
  );
});

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
          npm 全局包里的技能，以及启用中的插件带来的——它们是同一种格式，
          没必要在这里再装一遍。别处的技能可以关掉但删不了，去它自己的位置删
          （插件带的随插件停用一起消失）。同名时按这个顺序取第一个：
          AIClaw &gt; 插件 &gt; 项目 &gt; Claude Code &gt; Codex &gt; npm。
        </p>
      </header>

      <div class="actions">
        <button class="ghost" @click="actions.openSkillsDir()">打开技能目录</button>
        <button class="ghost" @click="actions.refreshSkills()">刷新</button>
      </div>

      <p v-if="store.skills.length === 0" class="note">
        还没有技能。点「打开技能目录」，在里面建一个子目录放 SKILL.md。
      </p>

      <div v-for="group in groups" :key="group.source" class="group">
        <div class="group-head">
          <span class="badge" :class="{ external: !group.writable }">{{ group.source }}</span>
          <span class="count">{{ group.skills.length }} 个</span>
          <span v-if="group.dir" class="group-dir" :title="group.dir">{{ group.dir }}</span>
          <span v-if="!group.writable" class="group-note">只能关，不能删</span>
        </div>

        <article v-for="skill in group.skills" :key="skill.id" class="card" :class="{ off: !skill.enabled }">
        <div class="card-head">
          <div class="who">
            <span class="name">{{ skill.name }}</span>
            <span class="dir" :title="skill.dir">{{ skill.dirName }}</span>
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
      </div>
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

.group {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

/* 组头压得比卡片轻：它是分隔，不是内容。 */
.group-head {
  display: flex;
  align-items: baseline;
  gap: 8px;
  min-width: 0;
  padding: 6px 2px 0;
}

.count {
  color: var(--muted);
  font-size: 11.5px;
  white-space: nowrap;
}

.group-dir {
  flex: 1;
  min-width: 0;
  color: var(--muted);
  font-family: var(--mono);
  font-size: 11px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.group-note {
  color: var(--muted);
  font-size: 11px;
  white-space: nowrap;
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
