// The DOM must exist before the overlays and user-event load: keep this first.
import { withDOM } from "../../testing/dom";

import { describe, expect, test } from "bun:test";
import { cleanup, render, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { shellRouter, ShellHarness, type Seed, testConfig, testMe } from "../../testing/app";

withDOM(cleanup);

const user = () => userEvent.setup({ pointerEventsCheck: 0, document });

function mount(initial = "/", seed: Seed = { me: testMe }) {
  const router = shellRouter(initial);
  return { router, ...render(<ShellHarness seed={seed} router={router} />) };
}

describe("the shell", () => {
  test("the sidebar carries boop's identity, the three nav groups and the caller", async () => {
    const { findByRole, getByRole, getByText } = mount();
    const nav = await findByRole("navigation", { name: "Primary" });
    for (const label of ["Fleet", "Activity", "System"]) {
      expect(within(nav).getByText(label)).toBeTruthy();
    }
    for (const page of ["Estate", "Pull requests", "Waiting", "Packages", "Repositories", "Runs", "Events", "Deliveries", "Status"]) {
      expect(within(nav).getByRole("link", { name: page })).toBeTruthy();
    }
    expect(getByText("boop")).toBeTruthy();
    expect(getByText("renovate · fleet")).toBeTruthy();
    expect(getByRole("button", { name: /Account menu:.*Grace Hopper/ })).toBeTruthy();
    expect(getByText("page /")).toBeTruthy();
  });

  test("the external links group shows only when configured, opening new tabs", async () => {
    const plain = mount();
    await plain.findByText("page /");
    expect(plain.queryByText("Links")).toBeNull();
    cleanup();

    const { findByRole } = mount("/", { me: testMe, config: { ...testConfig, links: [{ label: "Grafana", url: "https://grafana.example.com/d/boop" }] } });
    const link = await findByRole("link", { name: /Grafana/ });
    expect(link.getAttribute("href")).toBe("https://grafana.example.com/d/boop");
    expect(link.getAttribute("target")).toBe("_blank");
    expect(link.getAttribute("rel")).toBe("noopener noreferrer");
  });

  test("breadcrumbs: the section links back from a detail page", async () => {
    const { findByText, getByRole } = mount("/repositories/42");
    await findByText("page /repositories/$id");
    const trail = getByRole("navigation", { name: "breadcrumb" });
    expect(within(trail).getByRole("link", { name: "Repositories" }).getAttribute("href")).toBe("/repositories");
    expect(within(trail).getByText("42").getAttribute("aria-current")).toBe("page");
  });

  test("sign out is a POST form to /auth/logout", async () => {
    const { findByText, container } = mount();
    await findByText("page /");
    const form = container.ownerDocument.querySelector<HTMLFormElement>('form[action="/auth/logout"]');
    expect(form?.method).toBe("post");
  });

  test("a caller who sees no owner gets the no-access view, not the pages", async () => {
    const { findByText, queryByText, getByRole } = mount("/", { me: { ...testMe, owners: [], groups: [] } });
    expect(await findByText("No owners visible")).toBeTruthy();
    expect(queryByText("page /")).toBeNull();
    expect(getByRole("button", { name: "Sign out" }).closest("form")?.getAttribute("action")).toBe("/auth/logout");
  });

  test("an every-owner caller is let in with no owner list", async () => {
    const { findByText } = mount("/", { me: { ...testMe, all_owners: true, owners: [] } });
    expect(await findByText("page /")).toBeTruthy();
  });

  test("an unknown path is the not-found view inside the shell", async () => {
    const { findByText, getByRole } = mount("/nope");
    expect(await findByText("Not found")).toBeTruthy();
    expect(getByRole("navigation", { name: "Primary" })).toBeTruthy();
  });

  test("⌘K opens the palette, and choosing a page goes there", async () => {
    const u = user();
    const { router, findByText, findByRole, queryByRole } = mount();
    await findByText("page /");
    expect(queryByRole("dialog")).toBeNull();
    await u.keyboard("{Meta>}k{/Meta}");
    const dialog = await findByRole("dialog");
    await u.type(within(dialog).getByRole("combobox"), "runs");
    await u.keyboard("{Enter}");
    expect(await findByText("page /runs")).toBeTruthy();
    expect(router.state.location.pathname).toBe("/runs");
  });

  test("the Search button opens the palette too", async () => {
    const u = user();
    const { findByText, getByRole, findByRole } = mount();
    await findByText("page /");
    await u.click(getByRole("button", { name: /Search/ }));
    expect(await findByRole("dialog")).toBeTruthy();
  });
});
