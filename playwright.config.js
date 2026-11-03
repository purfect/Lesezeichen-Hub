import { defineConfig } from '@playwright/test';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';

const port = 18769;
export default defineConfig({
  testDir: './tests',
  testMatch: '**/*.browser.spec.js',
  workers: 1,
  timeout: 30000,
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    viewport: { width: 1440, height: 1000 },
    trace: 'retain-on-failure',
    launchOptions: process.env.PLAYWRIGHT_CHROME_PATH
      ? { executablePath: process.env.PLAYWRIGHT_CHROME_PATH }
      : {},
  },
  webServer: {
    command: process.env.PLAYWRIGHT_HUB_BIN || 'go run .',
    url: `http://127.0.0.1:${port}/api/notes`,
    reuseExistingServer: process.env.PLAYWRIGHT_REUSE_SERVER === '1',
    timeout: 120000,
    env: { ADDR: `127.0.0.1:${port}`, BOOKMARK_DB_PATH: join(tmpdir(), `lesezeichen-notes-test-${randomUUID()}.db`), ENABLE_EXTERNAL_PRICES: 'false' },
  },
});
