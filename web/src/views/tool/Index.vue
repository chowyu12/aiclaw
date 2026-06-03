<template>
  <div class="aic-page">
    <div class="aic-page-head">
      <h1 class="aic-title">{{ i18n.t('tools.title') }}</h1>
      <p class="aic-sub">{{ i18n.t('tools.subtitle') }}</p>
    </div>
    <div class="aic-page-body">
    <el-card class="aic-card" shadow="never">
      <template #header>
        <div class="aic-card-header">
          <span class="aic-card-title">{{ i18n.t('tools.list') }}</span>
          <div>
            <el-input v-model="keyword" :placeholder="i18n.t('common.search')" clearable style="width: 200px; margin-right: 12px;" @clear="loadData" @keyup.enter="loadData">
              <template #prefix><el-icon><Search /></el-icon></template>
            </el-input>
            <el-button type="primary" @click="router.push({ name: 'ToolCreate' })">
              <el-icon><Plus /></el-icon> {{ i18n.t('common.add') }}
            </el-button>
          </div>
        </div>
      </template>

      <el-table :data="list" v-loading="loading" stripe>
        <el-table-column prop="id" label="ID" width="60" />
        <el-table-column prop="name" :label="i18n.t('common.name')" width="160" />
        <el-table-column :label="i18n.t('common.description')" min-width="280">
          <template #default="{ row }">
            <span class="desc-cell">{{ row.description }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="handler_type" :label="i18n.t('common.type')" width="90" align="center">
          <template #default="{ row }">
            <el-tag :type="handlerTagType(row.handler_type)" size="small">
              {{ handlerLabel(row.handler_type) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="enabled" :label="i18n.t('common.status')" width="70" align="center">
          <template #default="{ row }">
            <el-tag :type="row.enabled ? 'success' : 'danger'" size="small">{{ row.enabled ? i18n.t('common.enabled') : i18n.t('common.disabled') }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="timeout" :label="i18n.t('tools.timeout')" width="90" align="center">
          <template #default="{ row }">{{ row.timeout || 30 }}</template>
        </el-table-column>
        <el-table-column prop="created_at" :label="i18n.t('common.createdAt')" width="170" show-overflow-tooltip />
        <el-table-column :label="i18n.t('common.actions')" width="120" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="router.push({ name: 'ToolEdit', params: { id: row.id } })">{{ i18n.t('common.edit') }}</el-button>
            <el-popconfirm title="Delete this item?" @confirm="handleDelete(row.id)">
              <template #reference>
                <el-button link type="danger">{{ i18n.t('common.delete') }}</el-button>
              </template>
            </el-popconfirm>
          </template>
        </el-table-column>
      </el-table>

      <el-pagination
        v-model:current-page="page" v-model:page-size="pageSize"
        :total="total" :page-sizes="[10, 20, 50]"
        layout="total, sizes, prev, pager, next" style="margin-top: 16px; justify-content: flex-end;"
        @size-change="loadData" @current-change="loadData"
      />
    </el-card>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { toolApi, type Tool } from '../../api/tool'
import { useI18nStore } from '../../stores/i18n'

const router = useRouter()
const i18n = useI18nStore()
const list = ref<Tool[]>([])
const loading = ref(false)
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const keyword = ref('')

function handlerTagType(type: string) {
  const m: Record<string, string> = { builtin: '', http: 'warning', command: '', script: 'info' }
  return m[type] || 'info'
}
function handlerLabel(type: string) {
  const m: Record<string, string> = { builtin: i18n.t('tools.builtin'), http: 'HTTP', command: i18n.t('tools.command'), script: i18n.t('tools.script') }
  return m[type] || type
}

async function loadData() {
  loading.value = true
  try {
    const res: any = await toolApi.list({ page: page.value, page_size: pageSize.value, keyword: keyword.value })
    list.value = res.data?.list || []
    total.value = res.data?.total || 0
  } finally {
    loading.value = false
  }
}

async function handleDelete(id: number) {
  try {
    await toolApi.delete(id)
    ElMessage.success(i18n.t('common.deleteSuccess'))
    loadData()
  } catch {
    ElMessage.error(i18n.t('common.deleteFailed'))
  }
}

onMounted(loadData)
</script>
