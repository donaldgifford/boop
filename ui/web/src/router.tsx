import { createRootRoute, createRoute, createRouter, retainSearchParams } from "@tanstack/react-router";

import { parseScopeSearch } from "./components/PageControls";
import { Shell } from "./shell/Shell";
import { EstateView } from "./views/estate";
import { PlaceholderView } from "./views/placeholder";
import { parsePullsSearch, PullsView } from "./views/pulls";
import { RepositoryView } from "./views/repository";

// The owner scope and the range ride along from page to page; a page's
// own filters stay behind.
const rootRoute = createRootRoute({
  component: Shell,
  validateSearch: parseScopeSearch,
  search: { middlewares: [retainSearchParams(["owner", "range"])] },
});

const estateRoute = createRoute({ getParentRoute: () => rootRoute, path: "/", component: EstateView });
const pullsRoute = createRoute({ getParentRoute: () => rootRoute, path: "/pulls", validateSearch: parsePullsSearch, component: PullsView });
const repositoryRoute = createRoute({ getParentRoute: () => rootRoute, path: "/repositories/$id", component: RepositoryView });
const placeholders = (["/waiting", "/packages", "/repositories", "/runs", "/events", "/deliveries", "/status"] as const).map((path) =>
  createRoute({ getParentRoute: () => rootRoute, path, component: PlaceholderView }),
);

const routeTree = rootRoute.addChildren([estateRoute, pullsRoute, repositoryRoute, ...placeholders]);

export const router = createRouter({ routeTree });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
