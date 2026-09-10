import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import cssInjectedByJsPlugin from 'vite-plugin-css-injected-by-js'

export default defineConfig({
  plugins: [vue(), cssInjectedByJsPlugin()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    rollupOptions: {
      output: {
        // 单入口直出:无懒加载分块,入口即全部产物
        // (Makefile static 只拷 index.html / main.js / favicon.png)
        entryFileNames: 'main.js',
      },
    },
  },
})
