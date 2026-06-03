<template>
  <div class="aic-page">
    <div class="aic-page-head">
      <h1 class="aic-title">Skills</h1>
      <p class="aic-sub">
        Built-in skills ship with the application. Local skills come from the Workspace
        <code>skills/</code> directory. Pending candidates are archived to
        <code>skills-pending/</code> after successful multi-tool Agent runs and become active
        only after promotion.
      </p>
    </div>
    <div class="aic-page-body" v-loading="loading">
      <el-tabs v-model="activeTab" class="sk-tabs">
        <template #prefix>
          <el-button size="small" plain style="margin-right: 12px" @click="loadAll">Refresh</el-button>
        </template>

        <!-- 待审候选 -->
        <el-tab-pane name="pending">
          <template #label>
            <span class="sk-tab-label">
              Pending
              <el-badge
                v-if="pendingSkills.length > 0"
                :value="pendingSkills.length"
                type="warning"
                class="sk-tab-badge"
              />
              <span v-else class="sk-tab-count">{{ pendingSkills.length }}</span>
            </span>
          </template>
          <div v-if="pendingSkills.length === 0" class="sk-empty">
            No pending candidates. Successful Agent runs that use 3 or more distinct tools
            are archived here automatically.
          </div>
          <div v-else class="sk-list">
            <div v-for="p in pendingSkills" :key="p.file_name" class="sk-item">
              <div class="sk-item-head">
                <span class="sk-item-name">{{ p.file_name }}</span>
                <el-tag size="small" type="warning" round>pending</el-tag>
                <span class="sk-time">{{ formatTime(p.updated_at) }}</span>
              </div>
              <div class="sk-item-desc">{{ p.preview || '—' }}</div>
              <div class="sk-item-actions">
                <el-button size="small" @click="openPreview(p.file_name)">Preview</el-button>
                <el-button size="small" type="primary" @click="openPromote(p)">Promote</el-button>
                <el-button size="small" type="danger" plain @click="discardPending(p.file_name)">
                  Discard
                </el-button>
              </div>
            </div>
          </div>
        </el-tab-pane>

        <!-- 内置技能 -->
        <el-tab-pane name="builtin">
          <template #label>
            <span class="sk-tab-label">
              Built-in
              <span class="sk-tab-count">{{ builtinSkills.length }}</span>
            </span>
          </template>
          <div v-if="builtinSkills.length === 0" class="sk-empty">No built-in skills</div>
          <div v-else class="sk-list">
            <div v-for="s in builtinSkills" :key="s.dir_name" class="sk-item">
              <div class="sk-item-head">
                <span class="sk-item-name">{{ s.name }}</span>
                <el-tag size="small" round>v{{ s.version }}</el-tag>
              </div>
              <div class="sk-item-desc">{{ s.description }}</div>
              <div class="sk-item-meta">
                <span>Directory <code>{{ s.dir_name }}</code></span>
                <span v-if="s.author">Author {{ s.author }}</span>
              </div>
            </div>
          </div>
        </el-tab-pane>

        <!-- 本地技能 -->
        <el-tab-pane name="local">
          <template #label>
            <span class="sk-tab-label">
              Local
              <span class="sk-tab-count">{{ localSkills.length }}</span>
            </span>
          </template>
          <div v-if="localSkills.length === 0" class="sk-empty">
            No skill directories under <code>~/.aiclaw/skills/</code>
          </div>
          <div v-else class="sk-list">
            <div v-for="s in localSkills" :key="s.dir_name" class="sk-item">
              <div class="sk-item-head">
                <span class="sk-item-name">{{ s.name }}</span>
                <el-tag v-if="s.version" size="small" round>v{{ s.version }}</el-tag>
                <el-tag v-if="s.main_file" size="small" type="success" round>Executable</el-tag>
              </div>
              <div class="sk-item-desc">{{ s.description || '—' }}</div>
              <div class="sk-item-meta">
                <span>Directory <code>{{ s.dir_name }}</code></span>
                <span v-if="s.author">Author {{ s.author }}</span>
                <span v-if="s.main_file">Entry <code>{{ s.main_file }}</code></span>
              </div>
            </div>
          </div>
        </el-tab-pane>
      </el-tabs>
    </div>

    <!-- 全文预览 -->
    <el-dialog
      v-model="previewVisible"
      :title="previewFile"
      width="720px"
      :destroy-on-close="true"
    >
      <pre class="sk-preview"><code>{{ previewContent }}</code></pre>
      <template #footer>
        <el-button @click="previewVisible = false">Close</el-button>
        <el-button
          v-if="previewFile"
          type="primary"
          @click="previewVisible = false; openPromote({ file_name: previewFile, updated_at: '', preview: '' })"
        >
          Promote
        </el-button>
      </template>
    </el-dialog>

    <!-- 转正 -->
    <el-dialog v-model="promoteVisible" title="Promote Skill Candidate" width="520px" :destroy-on-close="true">
      <el-form :model="promoteForm" label-width="90px">
        <el-form-item label="Candidate">
          <el-tag>{{ promoteForm.file }}</el-tag>
        </el-form-item>
        <el-form-item label="Name" required>
          <el-input
            v-model="promoteForm.name"
            placeholder="Example: fetch-and-summarize-url"
            maxlength="64"
            show-word-limit
          />
        </el-form-item>
        <el-form-item label="Description" required>
          <el-input
            v-model="promoteForm.description"
            type="textarea"
            :rows="3"
            placeholder="One sentence describing when to use this skill and what it does. This is written to SKILL.md frontmatter."
            maxlength="500"
            show-word-limit
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="promoteVisible = false">Cancel</el-button>
        <el-button type="primary" :loading="promoting" @click="submitPromote">Promote</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, reactive } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  workspaceSkillApi,
  type SkillItem,
  type PendingSkillItem,
} from '@/api/workspace_skill'

const builtinSkills = ref<SkillItem[]>([])
const localSkills = ref<SkillItem[]>([])
const pendingSkills = ref<PendingSkillItem[]>([])
const loading = ref(false)
const activeTab = ref('builtin')

const previewVisible = ref(false)
const previewFile = ref('')
const previewContent = ref('')

const promoteVisible = ref(false)
const promoting = ref(false)
const promoteForm = reactive({
  file: '',
  name: '',
  description: '',
})

async function loadSkills() {
  try {
    const res: any = await workspaceSkillApi.list()
    builtinSkills.value = res.data?.builtin || []
    localSkills.value = res.data?.local || []
  } catch {
    builtinSkills.value = []
    localSkills.value = []
  }
}

async function loadPending() {
  try {
    const res: any = await workspaceSkillApi.listPending()
    pendingSkills.value = res.data?.list || []
  } catch {
    pendingSkills.value = []
  }
}

async function loadAll() {
  loading.value = true
  try {
    await Promise.all([loadSkills(), loadPending()])
    if (pendingSkills.value.length > 0) {
      activeTab.value = 'pending'
    }
  } finally {
    loading.value = false
  }
}

async function openPreview(file: string) {
  try {
    const res: any = await workspaceSkillApi.readPending(file)
    previewFile.value = res.data?.file_name || file
    previewContent.value = res.data?.content || ''
    previewVisible.value = true
  } catch {
    /* error 已由拦截器提示 */
  }
}

function openPromote(p: PendingSkillItem) {
  promoteForm.file = p.file_name
  promoteForm.name = guessNameFromFile(p.file_name)
  promoteForm.description = ''
  promoteVisible.value = true
}

async function submitPromote() {
  const name = promoteForm.name.trim()
  const description = promoteForm.description.trim()
  if (!name) {
    ElMessage.warning('Enter a skill name')
    return
  }
  if (!description) {
    ElMessage.warning('Enter a description')
    return
  }
  promoting.value = true
  try {
    await workspaceSkillApi.promotePending(promoteForm.file, { name, description })
    ElMessage.success('Promoted to an active skill')
    promoteVisible.value = false
    await loadAll()
  } catch {
    /* 错误提示已由拦截器处理 */
  } finally {
    promoting.value = false
  }
}

async function discardPending(file: string) {
  try {
    await ElMessageBox.confirm(`Discard candidate ${file}? This cannot be undone.`, 'Discard Candidate', {
      type: 'warning',
      confirmButtonText: 'Discard',
      cancelButtonText: 'Cancel',
    })
  } catch {
    return
  }
  try {
    await workspaceSkillApi.discardPending(file)
    ElMessage.success('Discarded')
    await loadPending()
  } catch {
    /* 拦截器已提示 */
  }
}

// "20260423-153012-fetch-news.md" → "fetch-news"
function guessNameFromFile(file: string): string {
  const base = file.replace(/\.md$/i, '')
  const parts = base.split('-')
  if (parts.length <= 2) return base
  return parts.slice(2).join('-')
}

function formatTime(ts: string): string {
  if (!ts) return ''
  const d = new Date(ts)
  if (isNaN(d.getTime())) return ts
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

onMounted(() => loadAll())
</script>

<style scoped>
/* ===== Tabs 容器 ===== */
.sk-tabs {
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 10px;
  background: var(--el-fill-color-blank);
  overflow: hidden;
}

/* tab 面板内边距 */
.sk-tabs :deep(.el-tabs__content) {
  padding: 0;
}

/* tab 导航条底部对齐 */
.sk-tabs :deep(.el-tabs__header) {
  margin: 0;
  padding: 0 16px;
  background: var(--el-fill-color-light);
  border-bottom: 1px solid var(--el-border-color-lighter);
}

/* tab label 布局 */
.sk-tab-label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

/* 数量角标（无新增时用灰色文字） */
.sk-tab-count {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 18px;
  height: 18px;
  padding: 0 5px;
  font-size: 11px;
  line-height: 1;
  border-radius: 9px;
  background: var(--el-fill-color-dark);
  color: var(--el-text-color-secondary);
}

/* el-badge 对齐微调 */
.sk-tab-badge {
  position: static;
  transform: none;
}
.sk-tab-badge :deep(.el-badge__content) {
  position: static;
  transform: none;
}

/* ===== 列表 ===== */
.sk-list {
  border-top: none;
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
  word-break: break-all;
}

.sk-time {
  margin-left: auto;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.sk-item-desc {
  font-size: 13px;
  color: var(--el-text-color-regular);
  line-height: 1.5;
  margin-bottom: 6px;
  white-space: pre-wrap;
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

.sk-item-actions {
  display: flex;
  gap: 8px;
  margin-top: 6px;
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

/* ===== 预览 ===== */
.sk-preview {
  max-height: 60vh;
  overflow: auto;
  background: var(--el-fill-color-light);
  padding: 12px;
  border-radius: 6px;
  font-size: 12px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
}
</style>
