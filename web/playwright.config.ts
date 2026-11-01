import { defineConfig } from "@playwright/test";
import { randomUUID } from "node:crypto";
import { tmpdir } from "node:os";
import { join } from "node:path";
const baseURL = "http://127.0.0.1:18080";
export default defineConfig({
  testDir: "./tests/e2e",
  fullyParallel: false,
  workers: 1,
  timeout: 60_000,
  use: {
    baseURL,
    viewport: { width: 1440, height: 1000 },
    trace: "retain-on-failure",
    launchOptions: { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH },
  },
  webServer: [
    {
      command: "pnpm start --port 13000",
      url: "http://127.0.0.1:13000/admin",
      reuseExistingServer: !process.env.CI,
      timeout: 60_000,
    },
    {
      command: "go test ../tests/integration -run '^TestBrowserFixture$' -v -timeout 5m",
      url: `${baseURL}/readyz`,
      reuseExistingServer: !process.env.CI,
      timeout: 120_000,
      env: { BROWSER_FIXTURE_STOP: join(tmpdir(), `gateway-browser-${randomUUID()}`) },
      gracefulShutdown: { signal: "SIGTERM", timeout: 10_000 },
    },
  ],
});
