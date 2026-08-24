import { defineConfig, devices } from '@playwright/test'

/**
 * E2E 配置。
 *
 * 两点刻意的选择：
 *  - workers: 1。用例之间共享同一套后端数据（席位、房间状态是全局的），
 *    并行跑会互相干扰出假失败。
 *  - 不配 webServer。被测对象是 docker compose 起的真实前端容器，
 *    不是 vite dev server —— 测的必须是交付出去的那个产物。
 */
export default defineConfig({
  testDir: '.',
  testMatch: '*.spec.ts',
  timeout: 60_000,
  expect: { timeout: 10_000 },
  workers: 1,
  fullyParallel: false,
  forbidOnly: true,
  retries: 0,
  reporter: [['list'], ['html', { outputFolder: 'report', open: 'never' }]],
  outputDir: 'artifacts',
  use: {
    baseURL: process.env.WEB_BASE ?? 'http://frontend-user',
    locale: 'zh-CN',
    timezoneId: 'Asia/Shanghai',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
  },
  projects: [
    { name: 'desktop', use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } } },
    { name: 'mobile', use: { ...devices['Pixel 7'] } },
  ],
})
