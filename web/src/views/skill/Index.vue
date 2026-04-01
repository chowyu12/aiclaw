<template>
  <div class="aic-page">
    <div class="aic-page-head">
      <h1 class="aic-title">Skills</h1>
      <p class="aic-sub">
        内置技能随程序发布；本地技能来自 Workspace <code>skills/</code> 目录，放入后点击刷新即可加载。
      </p>
    </div>
    <div class="aic-page-body" v-loading="loading">
      <div class="toolbar" style="margin-bottom: 12px">
        <el-button @click="loadSkills">刷新列表</el-button>
      </div>

      <!-- 内置技能 -->
      <div class="sk-group">
        <div class="sk-group-header" @click="builtinOpen = !builtinOpen">
          <el-icon class="sk-arrow" :class="{ 'is-open': builtinOpen }"><ArrowRight /></el-icon>
          <span class="sk-group-title">内置技能</span>
          <el-tag size="small" type="info" round>{{ builtinSkills.length }}</el-tag>
        </div>
        <el-collapse-transition>
          <div v-show="builtinOpen">
            <div v-if="builtinSkills.length === 0" class="sk-empty">暂无内置技能</div>
            <div v-else class="sk-list">
              <div v-for="s in builtinSkills" :key="s.dir_name" class="sk-item">
                <div class="sk-item-head">
                  <span class="sk-item-name">{{ s.name }}</span>
                  <el-tag size="small" round>v{{ s.version }}</el-tag>
                </div>
                <div class="sk-item-desc">{{ s.description }}</div>
                <div class="sk-item-meta">
                  <span>目录 <code>{{ s.dir_name }}</code></span>
                  <span v-if="s.author">作者 {{ s.author }}</span>
                </div>
              </div>
            </div>
          </div>
        </el-collapse-transition>
      </div>

      <!-- 本地技能 -->
      <div class="sk-group">
        <div class="sk-group-header" @click="localOpen = !localOpen">
          <el-icon class="sk-arrow" :class="{ 'is-open': localOpen }"><ArrowRight /></el-icon>
          <span class="sk-group-title">本地技能</span>
          <el-tag size="small" type="info" round>{{ localSkills.length }}</el-tag>
        </div>
        <el-collapse-transition>
          <div v-show="localOpen">
            <div v-if="localSkills.length === 0" class="sk-empty">
              <code>~/.aiclaw/skills/</code> 下暂无技能目录
            </div>
            <div v-else class="sk-list">
              <div v-for="s in localSkills" :key="s.dir_name" class="sk-item">
                <div class="sk-item-head">
                  <span class="sk-item-name">{{ s.name }}</span>
                  <el-tag v-if="s.version" size="small" round>v{{ s.version }}</el-tag>
                  <el-tag v-if="s.main_file" size="small" type="success" round>可执行</el-tag>
                </div>
                <div class="sk-item-desc">{{ s.description || '—' }}</div>
                <div class="sk-item-meta">
                  <span>目录 <code>{{ s.dir_name }}</code></span>
                  <span v-if="s.author">作者 {{ s.author }}</span>
                  <span v-if="s.main_file">入口 <code>{{ s.main_file }}</code></span>
                </div>
              </div>
            </div>
          </div>
        </el-collapse-transition>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { workspaceSkillApi, type SkillItem } from '@/api/workspace_skill'

const builtinSkills = ref<SkillItem[]>([])
const localSkills = ref<SkillItem[]>([])
const loading = ref(false)
const builtinOpen = ref(true)
const localOpen = ref(true)

async function loadSkills() {
  loading.value = true
  try {
    const res: any = await workspaceSkillApi.list()
    builtinSkills.value = res.data?.builtin || []
    localSkills.value = res.data?.local || []
  } catch {
    builtinSkills.value = []
    localSkills.value = []
  } finally {
    loading.value = false
  }
}

onMounted(() => loadSkills())
</script>

<style scoped>
/* ===== 分组容器 ===== */
.sk-group {
  margin-bottom: 16px;
  border-radius: 10px;
  border: 1px solid var(--el-border-color-lighter);
  background: var(--el-fill-color-blank);
  overflow: hidden;
}

/* ===== 分组头（可点击折叠） ===== */
.sk-group-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 16px;
  cursor: pointer;
  user-select: none;
  transition: background 0.15s;
}

.sk-group-header:hover {
  background: var(--el-fill-color-light);
}

.sk-group-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--el-text-color-primary);
}

.sk-arrow {
  transition: transform 0.2s;
  color: var(--el-text-color-secondary);
  font-size: 14px;
}

.sk-arrow.is-open {
  transform: rotate(90deg);
}

/* ===== 列表 ===== */
.sk-list {
  border-top: 1px solid var(--el-border-color-lighter);
}

.sk-item {
  padding: 12px 16px;
  border-bottom: 1px solid var(--el-border-color-extra-light);
}

.sk-item:last-child {
  border-bottom: none;
}

.sk-item-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 4px;
}

.sk-item-name {
  font-size: 14px;
  font-weight: 500;
  color: var(--el-text-color-primary);
}

.sk-item-desc {
  font-size: 13px;
  color: var(--el-text-color-regular);
  line-height: 1.5;
  margin-bottom: 6px;
}

.sk-item-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.sk-item-meta code {
  font-size: 11px;
  background: var(--aic-sub-code-bg, var(--el-fill-color-light));
  color: var(--aic-sub-code-color, var(--el-text-color-primary));
  padding: 1px 5px;
  border-radius: 4px;
}

/* ===== 空状态 ===== */
.sk-empty {
  padding: 20px 16px;
  text-align: center;
  font-size: 13px;
  color: var(--el-text-color-secondary);
  border-top: 1px solid var(--el-border-color-lighter);
}

.sk-empty code {
  font-size: 12px;
  background: var(--aic-sub-code-bg, var(--el-fill-color-light));
  color: var(--aic-sub-code-color, var(--el-text-color-primary));
  padding: 1px 5px;
  border-radius: 4px;
}
</style>
