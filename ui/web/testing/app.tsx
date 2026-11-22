import { TimeZoneProvider } from "@donaldgifford/design-system/blocks/time";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRoute, createRouter, Outlet, RouterProvider } from "@tanstack/react-router";

import type { Schemas } from "../src/api/client";
import type { UIConfig } from "../src/api/queries";
import { NotFound, RouteError } from "../src/shell/errors";
import { NAV } from "../src/shell/nav";
import { Shell } from "../src/shell/Shell";

// Test doubles for the shell: the query cache is seeded, so no request
// leaves a test, and the router is in memory.

export const testMe: Schemas["Me"] = {
  subject: "user-1",
  name: "Grace Hopper",
  groups: ["team-web"],
  all_owners: false,
  owners: ["acme-web"],
  operator: false,
};

export const testConfig: UIConfig = { issuer: "https://idp.example.com", public_url: "https://boop.example.com", session_ttl_seconds: 3600, links: [] };

export interface Seed {
  me?: Schemas["Me"] | undefined;
  config?: UIConfig | undefined;
}

export function seededClient(seed: Seed = {}): QueryClient {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  client.setQueryData(["ui-config"], seed.config ?? testConfig);
  if (seed.me) client.setQueryData(["me"], seed.me);
  return client;
}

// shellRouter is the app's shell over stand-in pages: every nav path and
// /repositories/$id render "page <path>".
export function shellRouter(initial = "/") {
  const root = createRootRoute({ component: Outlet });
  const shell = createRoute({ getParentRoute: () => root, id: "shell", component: Shell, errorComponent: RouteError, notFoundComponent: NotFound });
  const pages = [...NAV.map((n) => n.to).filter((p) => p !== "/status"), "/repositories/$id"].map((path) =>
    createRoute({ getParentRoute: () => shell, path, component: () => <p>page {path}</p> }),
  );
  const rest = createRoute({ getParentRoute: () => shell, path: "$", component: NotFound });
  return createRouter({ routeTree: root.addChildren([shell.addChildren([...pages, rest])]), history: createMemoryHistory({ initialEntries: [initial] }) });
}

export function ShellHarness({ seed, router }: { seed?: Seed; router: ReturnType<typeof shellRouter> }) {
  return (
    <QueryClientProvider client={seededClient(seed)}>
      <TimeZoneProvider storageKey="boop.zone.test">
        <RouterProvider router={router} />
      </TimeZoneProvider>
    </QueryClientProvider>
  );
}
