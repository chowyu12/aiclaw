import { defineStore } from 'pinia'
import { ref } from 'vue'
import { type Agent, agentApi } from '@/api/agent'

export const useAgentStore = defineStore('agent', () => {
  const agents = ref<Agent[]>([])
  const activeAgent = ref<Agent | null>(null)
  const loading = ref(false)

  // 从 localStorage 恢复上次选择的 agentUUID
  const savedUUID = localStorage.getItem('activeAgentUUID')

  async function loadAgents() {
    loading.value = true
    try {
      const res = await agentApi.list({ page: 1, page_size: 100 })
      agents.value = res.data?.list ?? []
      // 恢复之前选中的 Agent
      if (savedUUID) {
        const found = agents.value.find((a) => a.uuid === savedUUID)
        if (found) {
          activeAgent.value = found
          return
        }
      }
      // 默认选中 is_default=true 或第一个
      const def = agents.value.find((a) => a.is_default) ?? agents.value[0]
      if (def) setActiveAgent(def)
    } catch {
      // ignore
    } finally {
      loading.value = false
    }
  }

  function setActiveAgent(agent: Agent) {
    activeAgent.value = agent
    localStorage.setItem('activeAgentUUID', agent.uuid)
  }

  function clearActiveAgent() {
    activeAgent.value = null
    localStorage.removeItem('activeAgentUUID')
  }

  return {
    agents,
    activeAgent,
    loading,
    loadAgents,
    setActiveAgent,
    clearActiveAgent,
  }
})
