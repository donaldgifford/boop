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
    // The k3d stack's issuer has an in-cluster name and a certificate
    // from the e2e CA: BOOP_E2E_HOST_RULES maps the name to its
    // port-forward, and BOOP_E2E_IGNORE_HTTPS accepts the certificate.
    ignoreHTTPSErrors: process.env.BOOP_E2E_IGNORE_HTTPS === "1",
    launchOptions: {
      args: process.env.BOOP_E2E_HOST_RULES ? [`--host-resolver-rules=${process.env.BOOP_E2E_HOST_RULES}`] : [],
    },
  },
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] }, testIgnore: /visual\.spec\.ts/ },
    { name: "visual", use: { ...devices["Desktop Chrome"] }, testMatch: /visual\.spec\.ts/ },
  ],
});
