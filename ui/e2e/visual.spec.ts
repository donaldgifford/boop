import { expect, test } from "@playwright/test";

import { expectHeading, PAGES, signIn } from "./stack";

// Screenshots of every page over the seeded stack. They render in the
// pinned Playwright image only (`just ui-visual`), so the files carry no
// platform suffix; refresh them with --update-snapshots.
test.describe("visual", () => {
  test.beforeEach(async ({ context, page }) => signIn(context, page, "operator"));

  for (const p of PAGES) {
    test(`${p.path}`, async ({ page }) => {
      await page.goto(p.path);
      await expectHeading(page, p.heading);
      await expect(page.getByText("Loading…")).toHaveCount(0);
      const name = p.path === "/" ? "estate" : p.path.slice(1).replaceAll(/[^a-z0-9]+/gi, "-");
      await expect(page).toHaveScreenshot(`${name}.png`, { fullPage: true, mask: [page.locator("time")] });
    });
  }

  test("/status", async ({ page }) => {
    await page.goto("/status");
    await expect(page.getByText("boop status")).toBeVisible();
    await expect(page).toHaveScreenshot("status.png", { fullPage: true, mask: [page.locator("time")] });
  });
});
