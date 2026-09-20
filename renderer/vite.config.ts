import {defineConfig} from 'vite'
import vue from '@vitejs/plugin-vue'
import {mkdirSync, writeFileSync} from 'node:fs'

// https://vitejs.dev/config/
export default defineConfig({
  // The shell serves the renderer from a custom scheme root, so asset URLs
  // must be relative rather than absolute.
  base: "./",
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
