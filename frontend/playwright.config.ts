import { defineConfig } from '@playwright/test'
export default defineConfig({
  testDir: './tests',
  use: { baseURL: 'http://127.0.0.1:3195', viewport: { width: 1280, height: 900 }, screenshot: 'only-on-failure' },
  webServer: { command: 'npm run dev -- --port 3195', url: 'http://127.0.0.1:3195/app/', reuseExistingServer: !process.env.CI },
})
