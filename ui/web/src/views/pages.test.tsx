// The DOM must exist before the overlays and user-event load: keep this first.
import { withDOM } from "../../testing/dom";

import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import type { QueryClient } from "@tanstack/react-query";
import { cleanup, render } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { keys } from "../api/queries";
import { AppHarness, appRouter, seededClient, testMe } from "../../testing/app";
import * as d from "../../testing/data";

withDOM(cleanup);

// A request that leaves a test is a seeding bug: fail it loudly.
const realFetch = globalThis.fetch;
afterAll(() => {
  globalThis.fetch = realFetch;
});
beforeAll(() => {
  globalThis.fetch = ((input: RequestInfo | URL) => Promise.reject(new Error(`unseeded request: ${String(input instanceof Request ? input.url : input)}`))) as typeof fetch;
});

// Each page over a seeded cache: the queries a page makes are seeded
// under the keys it computes, so no request leaves a test.

function mount(path: string, seed: (c: QueryClient) => void, me = testMe) {
  const client = seededClient({ me });
  seed(client);
  const router = appRouter(path);
  return { client, router, ...render(<AppHarness client={client} router={router} />) };
}

const user = () => userEvent.setup({ pointerEventsCheck: 0, document });

describe("pages", () => {
  test("estate shows the KPIs, the trend, top packages and installations", async () => {
    const { findByText, getAllByText } = mount("/", (c) => {
      c.setQueryData(keys.estate(undefined, "30d"), d.estate);
      c.setQueryData(keys.series("open_prs", undefined, "30d"), d.series("open_prs"));
      c.setQueryData(keys.series("runs", undefined, "30d"), d.series("runs"));
      c.setQueryData(keys.installations(), d.page([d.installation]));
    });
    expect(await findByText("pkg:npm/react")).toBeTruthy();
    expect(getAllByText("4,880").length).toBeGreaterThan(0);
  });

  test("pull requests list oldest first and open a drawer with updates, timeline and the GitHub link", async () => {
    const { findByText, findByRole, router } = mount("/pulls", (c) => {
      c.setQueryData(keys.pullRequests({ state: "open" }), d.page([d.pull]));
      c.setQueryData(keys.pullRequest(7, 42), d.pullDetail);
    });
    await user().click(await findByText("acme-web/site"));
    expect(router.state.location.search).toMatchObject({ open: "7-42" });
    const link = await findByRole("button", { name: /Open on GitHub/ });
    expect(link.getAttribute("href")).toBe(d.pull.url);
    expect(await findByText("run.finished")).toBeTruthy();
  });

  test("a pull request filter in the URL is a removable chip", async () => {
    const { findByRole, router } = mount("/pulls?update_type=major", (c) => {
      c.setQueryData(keys.pullRequests({ state: "open", update_type: "major" }), d.page([d.pull]));
      c.setQueryData(keys.pullRequests({ state: "open" }), d.page([d.pull]));
    });
    await user().click(await findByRole("button", { name: "Remove filter update_type = major" }));
    expect(router.state.location.search).not.toHaveProperty("update_type");
  });

  test("waiting groups items by section with a dashboard link", async () => {
    const { findByText, getByRole } = mount("/waiting", (c) => c.setQueryData(keys.dashboardItems({}), d.page([d.item])));
    expect(await findByText("Pending approval")).toBeTruthy();
    expect(getByRole("link", { name: "Dashboard" }).getAttribute("href")).toContain("https://github.com/acme-web/site/issues?q=");
  });

  test("a repository shows its phase and timeline; recheck is for operators only", async () => {
    const seed = (c: QueryClient) => {
      c.setQueryData(keys.repository(7), d.repository);
      c.setQueryData(keys.repositoryEvents(7), d.page([d.event]));
    };
    const viewer = mount("/repositories/7", seed);
    expect(await viewer.findByText("Run finished: Succeeded")).toBeTruthy();
    expect(viewer.queryByRole("button", { name: "Recheck" })).toBeNull();
    cleanup();

    const operator = mount("/repositories/7", seed, { ...testMe, operator: true });
    expect(await operator.findByRole("button", { name: "Recheck" })).toBeTruthy();
  });

  test("a repository's tab is in the URL", async () => {
    const { findByText } = mount("/repositories/7?tab=runs", (c) => {
      c.setQueryData(keys.repository(7), d.repository);
      c.setQueryData(keys.repositoryRuns(7), d.page([d.run]));
    });
    expect(await findByText("4m 07s")).toBeTruthy();
  });

  test("repositories, packages, a package, runs and events render their lists", async () => {
    const cases: [string, (c: QueryClient) => void, string][] = [
      ["/repositories", (c) => c.setQueryData(keys.repositories({}), d.page([d.repository])), "acme-web/site"],
      ["/packages", (c) => c.setQueryData(keys.packages({}), d.page([d.pkg])), "pkg:npm/react"],
      [
        "/packages/pkg%3Anpm%2Freact",
        (c) => {
          c.setQueryData(keys.updates("pkg:npm/react"), d.page([d.update]));
          c.setQueryData(keys.dependencies("pkg:npm/react"), d.page([d.dependency]));
        },
        "Used by",
      ],
      ["/runs", (c) => c.setQueryData(keys.runs({}), d.page([d.run])), "4m 07s"],
      ["/events", (c) => c.setQueryData(keys.events({}), d.page([d.event])), "Run finished: Succeeded"],
    ];
    for (const [path, seed, text] of cases) {
      const { findAllByText } = mount(path, seed);
      expect((await findAllByText(text)).length).toBeGreaterThan(0);
      cleanup();
    }
  });

  test("deliveries need every owner", async () => {
    const viewer = mount("/deliveries", () => {});
    expect(await viewer.findByText("Deliveries need every owner")).toBeTruthy();
    cleanup();

    const admin = mount("/deliveries", (c) => c.setQueryData(keys.deliveries({}), d.page([d.delivery])), { ...testMe, all_owners: true });
    expect(await admin.findByText("pull_request.closed")).toBeTruthy();
  });

  test("status is public and aggregate-only", async () => {
    const { findByText, queryByRole } = mount("/status", (c) => c.setQueryData(keys.status(), d.status));
    expect(await findByText("v0.9.0")).toBeTruthy();
    expect(queryByRole("navigation", { name: "Primary" })).toBeNull();
  });
});
