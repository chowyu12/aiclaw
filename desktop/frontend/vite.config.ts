import {defineConfig} from 'vite'
import vue from '@vitejs/plugin-vue'
import {mkdirSync, writeFileSync} from 'node:fs'

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [
    vue(),
    {
      name: 'preserve-embedded-dist',
      closeBundle() {
        mkdirSync('dist', {recursive: true})
        writeFileSync('dist/.gitkeep', '\n')
      }
    }
  ]
})
