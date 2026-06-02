<template>
  <div class="aic-page">
    <div class="aic-page-head">
      <div>
        <h1 class="aic-title">{{ i18n.t('scheduler.title') }}</h1>
        <p class="aic-sub" style="margin-top:4px;font-size:13px;color:var(--el-text-color-secondary)">
          {{ i18n.t('scheduler.subtitle') }}
        </p>
      </div>
    </div>

    <div class="aic-page-body">
      <el-empty v-if="!loading && jobs.length === 0" :description="i18n.t('scheduler.empty')" />

      <el-table v-else :data="jobs" v-loading="loading" row-key="id" style="width:100%" table-layout="auto">
        <el-table-column type="expand">
          <template #default="{ row }">
            <div class="sj-expand">
              <div v-if="row.type === 'prompt'" class="sj-detail">
                <span class="sj-label">Prompt</span>
                <pre class="sj-pre">{{ row.prompt }}</pre>
              </div>
              <div v-if="row.type === 'command'" class="sj-detail">
                <span class="sj-label">Command</span>
                <pre class="sj-pre">{{ row.command }}</pre>
              </div>
              <div v-if="row.description" class="sj-detail">
                <span class="sj-label">{{ i18n.t('common.description') }}</span>
                <span>{{ row.description }}</span>
              </div>
              <div v-if="row.agent_uuid" class="sj-detail">
                <span class="sj-label">Agent UUID</span>
                <code class="sj-code">{{ row.agent_uuid }}</code>
              </div>

              <!-- 执行日志 -->
              <div class="sj-logs-section">
                <div class="sj-logs-head">
                  <span class="sj-label">{{ i18n.t('scheduler.logs') }}</span>
                  <el-button size="small" text type="primary" @click="loadLogs(row.id)" :loading="logsLoading === row.id">
                    {{ i18n.t('common.refresh') }}
                  </el-button>
                </div>
                <el-table v-if="logsMap[row.id]?.length" :data="logsMap[row.id]" size="small" style="width:100%">
                  <el-table-column :label="i18n.t('scheduler.runAt')" width="180">
                    <template #default="{ row: log }">
                      {{ formatTime(log.run_at) }}
                    </template>
                  </el-table-column>
                  <el-table-column :label="i18n.t('common.status')" width="90" align="center">
                    <template #default="{ row: log }">
                      <el-tag :type="log.status === 'success' ? 'success' : 'danger'" size="small">
                        {{ log.status === 'success' ? i18n.t('scheduler.success') : i18n.t('scheduler.failed') }}
                      </el-tag>
                    </template>
                  </el-table-column>
                  <el-table-column :label="i18n.t('scheduler.duration')" prop="duration" width="120" />
                  <el-table-column :label="i18n.t('scheduler.outputError')" min-width="200">
                    <template #default="{ row: log }">
                      <pre v-if="log.error" class="sj-pre sj-pre--error">{{ log.error }}</pre>
                      <pre v-else-if="log.output" class="sj-pre">{{ log.output }}</pre>
                      <span v-else class="sj-muted">-</span>
                    </template>
                  </el-table-column>
                </el-table>
                <div v-else class="sj-muted" style="margin-top:4px">{{ i18n.t('scheduler.noLogs') }}</div>
              </div>
            </div>
          </template>
        </el-table-column>

        <el-table-column :label="i18n.t('common.name')" prop="name" min-width="140">
          <template #default="{ row }">
            <span class="sj-name">{{ row.name }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="i18n.t('common.type')" width="90" align="center">
          <template #default="{ row }">
            <el-tag size="small" :type="row.type === 'prompt' ? 'primary' : 'info'">
              {{ row.type === 'prompt' ? 'Prompt' : 'Command' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column :label="i18n.t('scheduler.expression')" prop="expression" width="160" show-overflow-tooltip />
        <el-table-column :label="i18n.t('scheduler.runCount')" width="100" align="center">
          <template #default="{ row }">
            <span>{{ row.run_count }}</span>
            <span v-if="row.max_runs" class="sj-muted"> / {{ row.max_runs }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="i18n.t('scheduler.nextRun')" width="180">
          <template #default="{ row }">
            <span v-if="row.enabled && row.next_run_at">{{ formatTime(row.next_run_at) }}</span>
            <span v-else class="sj-muted">-</span>
          </template>
        </el-table-column>
        <el-table-column :label="i18n.t('scheduler.lastRun')" width="180">
          <template #default="{ row }">
            <span v-if="row.last_run_at">{{ formatTime(row.last_run_at) }}</span>
            <span v-else class="sj-muted">-</span>
          </template>
        </el-table-column>
        <el-table-column :label="i18n.t('common.status')" width="80" align="center">
          <template #default="{ row }">
            <el-switch
              :model-value="row.enabled"
              :loading="togglingId === row.id"
              size="small"
              @change="(val: any) => handleToggle(row, val as boolean)"
            />
          </template>
        </el-table-column>
        <el-table-column :label="i18n.t('common.actions')" width="80" fixed="right" align="center">
          <template #default="{ row }">
            <el-popconfirm :title="i18n.t('scheduler.deleteConfirm')" @confirm="handleDelete(row.id)">
              <template #reference>
                <el-button size="small" type="danger" text>{{ i18n.t('common.delete') }}</el-button>
              </template>
            </el-popconfirm>
          </template>
        </el-table-column>
      </el-table>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { schedulerApi, type SchedulerJob, type RunRecord } from '@/api/scheduler'
import { useI18nStore } from '@/stores/i18n'

const i18n = useI18nStore()
const jobs = ref<SchedulerJob[]>([])
const loading = ref(false)
const togglingId = ref<string | null>(null)
const logsLoading = ref<string | null>(null)
const logsMap = reactive<Record<string, RunRecord[]>>({})

function formatTime(t: string): string {
  if (!t) return '-'
  const d = new Date(t)
  if (isNaN(d.getTime())) return t
  return d.toLocaleString(i18n.language, {
    month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit',
  })
}

async function loadJobs() {
  loading.value = true
  try {
    const res: any = await schedulerApi.listJobs()
    jobs.value = res.data ?? []
  } catch {
    ElMessage.error(i18n.t('scheduler.loadFailed'))
  } finally {
    loading.value = false
  }
}

async function loadLogs(jobId: string) {
  logsLoading.value = jobId
  try {
    const res: any = await schedulerApi.getJobLogs(jobId, 20)
    logsMap[jobId] = (res.data ?? []).reverse()
  } catch {
    ElMessage.error(i18n.t('scheduler.logsFailed'))
  } finally {
    logsLoading.value = null
  }
}

async function handleToggle(row: SchedulerJob, enabled: boolean) {
  togglingId.value = row.id
  try {
    await schedulerApi.toggleJob(row.id, enabled)
    row.enabled = enabled
    ElMessage.success(enabled ? i18n.t('scheduler.enabled') : i18n.t('scheduler.disabled'))
  } catch {
    ElMessage.error(i18n.t('common.operationFailed'))
  } finally {
    togglingId.value = null
  }
}

async function handleDelete(id: string) {
  try {
    await schedulerApi.deleteJob(id)
    ElMessage.success(i18n.t('common.deleteSuccess'))
    await loadJobs()
  } catch {
    ElMessage.error(i18n.t('common.deleteFailed'))
  }
}

onMounted(() => loadJobs())
</script>

<style scoped>
.sj-name { font-weight: 500; }
.sj-muted { color: var(--el-text-color-placeholder); font-size: 12px; }

.sj-expand {
  padding: 12px 16px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.sj-detail {
  display: flex;
  gap: 8px;
  align-items: flex-start;
  font-size: 13px;
}

.sj-label {
  font-size: 12px;
  font-weight: 600;
  color: var(--el-text-color-secondary);
  min-width: 80px;
  flex-shrink: 0;
}

.sj-pre {
  margin: 0;
  font-family: ui-monospace, 'SF Mono', monospace;
  font-size: 12px;
  line-height: 1.5;
  background: var(--el-fill-color-light);
  padding: 6px 10px;
  border-radius: 6px;
  white-space: pre-wrap;
  word-break: break-all;
  max-height: 120px;
  overflow-y: auto;
  flex: 1;
  min-width: 0;
}

.sj-pre--error {
  color: var(--el-color-danger);
  background: var(--el-color-danger-light-9);
}

.sj-code {
  font-family: ui-monospace, 'SF Mono', monospace;
  font-size: 12px;
  background: var(--el-fill-color-light);
  padding: 2px 6px;
  border-radius: 4px;
}

.sj-logs-section {
  margin-top: 4px;
  padding-top: 10px;
  border-top: 1px solid var(--el-border-color-lighter);
}

.sj-logs-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
}
</style>
