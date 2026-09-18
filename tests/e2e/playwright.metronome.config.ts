import { defineConfig, devices } from "@playwright/test";

// UI/API-contract tests use mocked HTTP responses; no provider keys, test .so,
// MCP services or deployed Bifrost instance are needed.
export default defineConfig({
  testDir: "./features/plugins",
  testMatch: "metronome.spec.ts",
  workers: 1,
  reporter: "list",
  use: {
    ...devices["Desktop Chrome"],
    baseURL: process.env.BASE_URL || "http://127.0.0.1:18732",
    launchOptions: { executablePath: process.env.E2E_CHROMIUM_EXECUTABLE },
  },
});