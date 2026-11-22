import AxeBuilder from "@axe-core/playwright";
import { type BrowserContext, expect, type Page } from "@playwright/test";

// The deployed stack the suite runs against (IMPL-0005 14.3): the UI, the
// mock issuer it signs in through, and the seeded store's fixtures.
export const stack = {
  issuer: process.env.BOOP_ISSUER_URL ?? "http://localhost:9090",
  // repoId is a repository the operator and the viewer both see.
  repoId: process.env.BOOP_E2E_REPO_ID ?? "7",
  // hiddenRepoId is a repository under an owner the viewer cannot see.
  hiddenRepoId: process.env.BOOP_E2E_HIDDEN_REPO_ID ?? "9",
  purl: process.env.BOOP_E2E_PURL ?? "pkg:npm/react",
};

// Who signs in: the mock issuer's users (server/testing/mock-issuer.ts),
// picked by its mock_user cookie. The operator's group sees every owner
// and may recheck; the viewer's sees one owner.
export type Who = "operator" | "viewer";

export async function signIn(context: BrowserContext, page: Page, who: Who): Promise<void> {
  await context.addCookies([{ name: "mock_user", value: who, url: stack.issuer }]);
  await page.goto("/");
  await expect(page.getByRole("navigation", { name: "Primary" })).toBeVisible();
}

// PAGES are every page, with the heading each shows once it has loaded.
export const PAGES: readonly { path: string; heading: string | RegExp }[] = [
  { path: "/", heading: "Estate" },
  { path: "/pulls", heading: "Pull requests" },
  { path: "/waiting", heading: "Waiting" },
  { path: "/packages", heading: "Packages" },
  { path: `/packages/${encodeURIComponent(stack.purl)}`, heading: stack.purl },
  { path: "/repositories", heading: "Repositories" },
  { path: `/repositories/${stack.repoId}`, heading: /\// },
  { path: "/runs", heading: "Runs" },
  { path: "/events", heading: "Events" },
  { path: "/deliveries", heading: "Deliveries" },
];

export async function expectHeading(page: Page, heading: string | RegExp): Promise<void> {
  await expect(page.getByRole("heading", { level: 1, name: heading })).toBeVisible();
}

// expectNoAxeViolations runs axe on the page as rendered.
export async function expectNoAxeViolations(page: Page): Promise<void> {
  const results = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
  expect(results.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`)).toEqual([]);
}
