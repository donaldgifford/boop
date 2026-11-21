import { defineConfig, devices } from "@playwright/test";

// The Playwright flows (IMPL-0005 12.8, 14.3) run against a deployed
// stack: the k3d e2e cluster in CI, or anything BOOP_UI_URL names. The
// suite starts no servers of its own.
export default defineConfig({
  testDir: "e2e",
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  // Screenshots render in one environment, the pinned Playwright image,
  // so the path carries no platform suffix.
  snapshotPathTemplate: "e2e/__screenshots__/{arg}{ext}",
  expect: { toHaveScreenshot: { maxDiffPixelRatio: 0.002 } },
  use: {
    baseURL: process.env.BOOP_UI_URL ?? "http://localhost:3000",
    trace: "retain-on-failure",
  },
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] }, testIgnore: /visual\.spec\.ts/ },
    { name: "visual", use: { ...devices["Desktop Chrome"] }, testMatch: /visual\.spec\.ts/ },
  ],
});
