<template>
  <div class="aic-page">
    <div class="aic-page-head">
      <h1 class="aic-title">工具</h1>
      <p class="aic-sub">管理 Agent 可调用的内置、HTTP、命令行等工具；与 Agent 设置中的「关联工具」联动。</p>
    </div>
    <div class="aic-page-body">
    <el-card class="aic-card" shadow="never">
      <template #header>
        <div class="aic-card-header">
          <span class="aic-card-title">工具列表</span>
          <div>
            <el-input v-model="keyword" placeholder="搜索" clearable style="width: 200px; margin-right: 12px;" @clear="loadData" @keyup.enter="loadData">
              <template #prefix><el-icon><Search /></el-icon></template>
            </el-input>
            <el-button type="primary" @click="router.push({ name: 'ToolCreate' })">
              <el-icon><Plus /></el-icon> 新增
            </el-button>
          </div>
        </div>
      </template>

      <el-table :data="list" v-loading="loading" stripe>
        <el-table-column prop="id" label="ID" width="60" />
        <el-table-column prop="name" label="名称" width="160" />
        <el-table-column label="描述" min-width="280">
          <template #default="{ row }">
            <span class="desc-cell">{{ row.description }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="handler_type" label="类型" width="90" align="center">
          <template #default="{ row }">
            <el-tag :type="handlerTagType(row.handler_type)" size="small">
              {{ handlerLabel(row.handler_type) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="enabled" label="状态" width="70" align="center">
          <template #default="{ row }">
            <el-tag :type="row.enabled ? 'success' : 'danger'" size="small">{{ row.enabled ? '启用' : '禁用' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="timeout" label="超时(秒)" width="80" align="center">
          <template #default="{ row }">{{ row.timeout || 30 }}</template>
        </el-table-column>
        <el-table-column prop="created_at" label="创建时间" width="170" show-overflow-tooltip />
        <el-table-column label="操作" width="120" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="router.push({ name: 'ToolEdit', params: { id: row.id } })">编辑</el-button>
            <el-popconfirm title="确定删除？" @confirm="handleDelete(row.id)">
              <template #reference>
                <el-button link type="danger">删除</el-button>
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

const router = useRouter()
const list = ref<Tool[]>([])
const loading = ref(false)
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const keyword = ref('')

function handlerTagType(type: string) {
  const m: Record<string, string> = { builtin: 'success', http: 'warning', command: '', script: 'info' }
  return m[type] || 'info'
}
function handlerLabel(type: string) {
  const m: Record<string, string> = { builtin: 'builtin', http: 'http', command: 'command', script: 'script' }
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
    ElMessage.success('删除成功')
    loadData()
  } catch {
    ElMessage.error('删除失败')
  }
}

onMounted(loadData)
</script>

<style scoped>
/* desc-cell 已移入 theme.css 全局样式 */
</style>
