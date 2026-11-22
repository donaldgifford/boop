import { createRootRoute, createRoute, createRouter, Outlet, retainSearchParams } from "@tanstack/react-router";

import { parseScopeSearch } from "./components/PageControls";
import { NotFound, RouteError } from "./shell/errors";
import { Shell } from "./shell/Shell";
import { EstateView } from "./views/estate";
import { PlaceholderView } from "./views/placeholder";
import { parsePullsSearch, PullsView } from "./views/pulls";
import { RepositoryView } from "./views/repository";
import { StatusView } from "./views/status";

const rootRoute = createRootRoute({ component: Outlet });

// The owner scope and the range ride along from page to page inside the
// shell; a page's own filters stay behind.
const shellRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: "shell",
  component: Shell,
  errorComponent: RouteError,
  notFoundComponent: NotFound,
  validateSearch: parseScopeSearch,
  search: { middlewares: [retainSearchParams(["owner", "range"])] },
});

const estateRoute = createRoute({ getParentRoute: () => shellRoute, path: "/", component: EstateView });
const pullsRoute = createRoute({ getParentRoute: () => shellRoute, path: "/pulls", validateSearch: parsePullsSearch, component: PullsView });
const repositoryRoute = createRoute({ getParentRoute: () => shellRoute, path: "/repositories/$id", component: RepositoryView });
const placeholders = (["/waiting", "/packages", "/repositories", "/runs", "/events", "/deliveries"] as const).map((path) =>
  createRoute({ getParentRoute: () => shellRoute, path, component: PlaceholderView }),
);

// Any other path is not found, inside the frame.
const notFoundRoute = createRoute({ getParentRoute: () => shellRoute, path: "$", component: NotFound });

// The status page is public: it sits outside the shell, which is where a
// session will be asked for (DESIGN-0004).
const statusRoute = createRoute({ getParentRoute: () => rootRoute, path: "/status", component: StatusView });

const routeTree = rootRoute.addChildren([shellRoute.addChildren([estateRoute, pullsRoute, repositoryRoute, ...placeholders, notFoundRoute]), statusRoute]);

export const router = createRouter({ routeTree, defaultErrorComponent: RouteError, defaultNotFoundComponent: NotFound });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
