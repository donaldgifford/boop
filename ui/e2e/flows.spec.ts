import { expect, test } from "@playwright/test";

import { expectHeading, expectNoAxeViolations, PAGES, signIn, stack } from "./stack";

// The UI's flows against the deployed stack (IMPL-0005 12.8, 14.3).

test.describe("signed out", () => {
  test("a page sends the browser through the issuer and back", async ({ context, page }) => {
    await context.addCookies([{ name: "mock_user", value: "operator", url: stack.issuer }]);
    await page.goto("/pulls");
    await expect(page).toHaveURL(/\/pulls$/);
    await expectHeading(page, "Pull requests");
  });

  test("the status page is public and passes axe", async ({ page }) => {
    await page.goto("/status");
    await expect(page.getByText("boop status")).toBeVisible();
    await expect(page.getByRole("navigation", { name: "Primary" })).toHaveCount(0);
    await expectNoAxeViolations(page);
  });
});

test.describe("operator", () => {
  test.beforeEach(async ({ context, page }) => signIn(context, page, "operator"));

  for (const p of PAGES) {
    test(`${p.path} renders and passes axe`, async ({ page }) => {
      await page.goto(p.path);
      await expectHeading(page, p.heading);
      await expect(page.getByText("Loading…")).toHaveCount(0);
      await expectNoAxeViolations(page);
    });
  }

  test("recheck is accepted and lands in the timeline", async ({ page }) => {
    await page.goto(`/repositories/${stack.repoId}`);
    const accepted = page.waitForResponse((r) => r.url().endsWith(`/api/v1/repositories/${stack.repoId}/recheck`) && r.request().method() === "POST");
    await page.getByRole("button", { name: "Recheck" }).click();
    expect((await accepted).status()).toBe(202);
    await expect(page.getByRole("status").filter({ hasText: /Recheck requested|Already requested/ })).toBeVisible();
    await expect(page.getByText("action.recheck").first()).toBeVisible({ timeout: 30_000 });
  });
});

test.describe("viewer", () => {
  test.beforeEach(async ({ context, page }) => signIn(context, page, "viewer"));

  test("a repository has no recheck button", async ({ page }) => {
    await page.goto(`/repositories/${stack.repoId}`);
    await expectHeading(page, /\//);
    await expect(page.getByRole("button", { name: "Recheck" })).toHaveCount(0);
  });

  test("a repository under an invisible owner is 404", async ({ page }) => {
    const res = page.waitForResponse((r) => r.url().endsWith(`/api/v1/repositories/${stack.hiddenRepoId}`));
    await page.goto(`/repositories/${stack.hiddenRepoId}`);
    expect((await res).status()).toBe(404);
  });

  test("deliveries explain the restriction instead of asking", async ({ page }) => {
    await page.goto("/deliveries");
    await expect(page.getByText("Deliveries need every owner")).toBeVisible();
    await expectNoAxeViolations(page);
  });
});
