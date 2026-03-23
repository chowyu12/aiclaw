<template>
  <div class="aic-page">
    <div class="aic-page-head">
      <h1 class="aic-title">Skills</h1>
      <p class="aic-sub">
        Workspace <code>skills/</code> 下的技能目录，运行时自动注入
        Agent；将技能放入该文件夹后点击刷新。
      </p>
    </div>
    <div class="aic-page-body">
      <el-card class="aic-card" shadow="never" v-loading="skillLoading">
        <div class="toolbar">
          <el-button @click="loadWorkspaceSkills">刷新列表</el-button>
        </div>
        <el-table
          :data="workspaceSkills"
          stripe
          style="width: 100%; margin-top: 16px"
        >
          <el-table-column prop="dir_name" label="目录" width="140" />
          <el-table-column prop="name" label="名称" min-width="120" />
          <el-table-column
            prop="description"
            label="描述"
            min-width="200"
            show-overflow-tooltip
          />
          <el-table-column prop="version" label="版本" width="90" />
        </el-table>
      </el-card>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from "vue";
import {
  workspaceSkillApi,
  type WorkspaceSkillItem,
} from "@/api/workspace_skill";

const workspaceSkills = ref<WorkspaceSkillItem[]>([]);
const skillLoading = ref(false);

async function loadWorkspaceSkills() {
  skillLoading.value = true;
  try {
    const res: any = await workspaceSkillApi.list();
    workspaceSkills.value = res.data?.list || [];
  } catch {
    workspaceSkills.value = [];
  } finally {
    skillLoading.value = false;
  }
}

onMounted(() => loadWorkspaceSkills());
</script>

<style scoped>
.toolbar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}
</style>
