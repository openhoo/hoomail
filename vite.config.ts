import { fileURLToPath, URL } from 'node:url'
import preact from '@preact/preset-vite'
import tailwindcss from '@tailwindcss/vite'
import { defineConfig } from 'vite'

export default defineConfig({
  plugins: [preact({ reactAliasesEnabled: false }), tailwindcss()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('.', import.meta.url)) },
  },
  build: {
    outDir: 'web/dist',
    emptyOutDir: true,
  },
})
