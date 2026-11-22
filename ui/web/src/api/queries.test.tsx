import "../../testing/dom";

import { describe, expect, test } from "bun:test";
import { type Query, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";

import { seededClient } from "../../testing/app";
import { withDOM } from "../../testing/dom";
import type { Schemas } from "./client";
import { eventPollMs, inProgress, keys, listPollMs, livePollMs, useEvents, useRepository, useRuns } from "./queries";

withDOM(cleanup);

const repo = (over: Partial<Schemas["RepositoryDetail"]>): Schemas["RepositoryDetail"] => ({
  id: 7,
  installation_id: 1,
  owner: "acme-web",
  slug: "acme-web/site",
  state: "idle",
  profile: "default",
  open_prs: 0,
  waiting: 0,
  live: true,
  phase: "idle",
  ...over,
});

// interval resolves a query's refetchInterval the way the observer does.
function interval(client: ReturnType<typeof seededClient>, key: readonly unknown[]): number | false | undefined {
  const query = client.getQueryCache().find({ queryKey: key }) as Query | undefined;
  const opt = query?.observers[0]?.options.refetchInterval;
  return typeof opt === "function" ? opt(query as never) : opt;
}

describe("polling (DESIGN-0004 OQ10)", () => {
  test("inProgress is a running state or phase", () => {
    expect(inProgress(repo({ state: "running" }))).toBe(true);
    expect(inProgress(repo({ phase: "running" }))).toBe(true);
    expect(inProgress(repo({}))).toBe(false);
    expect(inProgress(undefined)).toBe(false);
  });

  test.each<[string, Schemas["RepositoryDetail"], number]>([
    ["idle", repo({}), listPollMs],
    ["running", repo({ state: "running", phase: "running" }), livePollMs],
  ])("an %s repository polls at its interval", (_, data, want) => {
    const client = seededClient();
    client.setQueryData(keys.repository(7), data);
    const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    renderHook(() => useRepository(7), { wrapper });
    expect(interval(client, keys.repository(7))).toBe(want);
  });

  test("lists poll every 30 s and the event stream every 15 s", () => {
    const client = seededClient();
    const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    const page = { pages: [{ items: [], next_cursor: null }], pageParams: [undefined] };
    client.setQueryData(keys.runs({}), page);
    client.setQueryData(keys.events({}), page);
    renderHook(() => [useRuns({}), useEvents({})], { wrapper });
    expect(interval(client, keys.runs({}))).toBe(listPollMs);
    expect(interval(client, keys.events({}))).toBe(eventPollMs);
    expect([listPollMs, eventPollMs, livePollMs]).toEqual([30_000, 15_000, 5_000]);
  });
});
