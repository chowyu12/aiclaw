import { defineStore } from 'pinia'
import { watch } from 'vue'
import { useStorage } from '@vueuse/core'

export type ThemeMode = 'light' | 'dark'

export function applyDomTheme(mode: ThemeMode) {
  document.documentElement.setAttribute('data-theme', mode)
  document.documentElement.classList.toggle('dark', mode === 'dark')
}

/** 在 Pinia 挂载前同步读取，避免首屏闪烁 */
export function readInitialThemeMode(): ThemeMode {
  try {
    const raw = localStorage.getItem('aiclaw-theme')
    if (raw == null) return 'light'
    const v = JSON.parse(raw) as string
    return v === 'light' ? 'light' : 'dark'
  } catch {
    return 'light'
  }
}

export const useThemeStore = defineStore('theme', () => {
  const mode = useStorage<ThemeMode>('aiclaw-theme', 'light')

  watch(mode, (m) => applyDomTheme(m), { immediate: true })

  function setMode(m: ThemeMode) {
    mode.value = m
  }

  function toggleMode() {
    mode.value = mode.value === 'dark' ? 'light' : 'dark'
  }

  return { mode, setMode, toggleMode }
})
