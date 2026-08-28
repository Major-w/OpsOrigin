import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import { fileURLToPath } from 'node:url'

// Vitest 配置：前端单测骨架。
// - jsdom 提供浏览器环境（localStorage / document / window），便于测试 stores 与工具函数。
// - 路径别名 `@` 与 tsconfig.json 的 paths 保持一致。
// - setup 文件统一注入 jest-dom 断言与第三方库所需的浏览器 API polyfill。
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./vitest.setup.ts'],
    css: false,
    include: ['src/**/*.{test,spec}.{ts,tsx}'],
    exclude: ['node_modules', '.next', 'e2e/**', 'playwright/**'],
    coverage: {
      provider: 'v8',
      reporter: ['text', 'html', 'lcov'],
      include: ['src/**/*.{ts,tsx}'],
      exclude: [
        'src/**/*.{test,spec}.{ts,tsx}',
        'src/test/**',
        'src/**/*.d.ts',
        'src/app/**',
        '**/*.config.*',
      ],
    },
  },
})
