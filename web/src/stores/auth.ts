import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { resetTokenVerified } from '@/router'

export const useAuthStore = defineStore('auth', () => {
  const token = ref(localStorage.getItem('token') || '')

  const isLoggedIn = computed(() => !!token.value)

  function setToken(t: string) {
    token.value = t
    localStorage.setItem('token', t)
  }

  function logout() {
    token.value = ''
    localStorage.removeItem('token')
    resetTokenVerified()
  }

  return { token, isLoggedIn, setToken, logout }
})
