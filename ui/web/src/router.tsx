import { createRootRoute, createRoute, createRouter, Outlet, retainSearchParams } from "@tanstack/react-router";

import { parseScopeSearch } from "./components/PageControls";
import { NotFound, RouteError } from "./shell/errors";
import { Shell } from "./shell/Shell";
import { EstateView } from "./views/estate";
import { DeliveriesView, parseDeliveriesSearch } from "./views/deliveries";
import { EventsView, parseEventsSearch } from "./views/events";
import { PackagesView, PackageView } from "./views/packages";
import { parseRepositoriesSearch, RepositoriesView } from "./views/repositories";
import { parseRunsSearch, RunsView } from "./views/runs";
import { parsePullsSearch, PullsView } from "./views/pulls";
import { parseRepositorySearch, RepositoryView } from "./views/repository";
import { StatusView } from "./views/status";
import { parseWaitingSearch, WaitingView } from "./views/waiting";

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
const repositoryRoute = createRoute({ getParentRoute: () => shellRoute, path: "/repositories/$id", validateSearch: parseRepositorySearch, component: RepositoryView });
const waitingRoute = createRoute({ getParentRoute: () => shellRoute, path: "/waiting", validateSearch: parseWaitingSearch, component: WaitingView });
const repositoriesRoute = createRoute({ getParentRoute: () => shellRoute, path: "/repositories", validateSearch: parseRepositoriesSearch, component: RepositoriesView });
const packagesRoute = createRoute({ getParentRoute: () => shellRoute, path: "/packages", component: PackagesView });
const packageRoute = createRoute({ getParentRoute: () => shellRoute, path: "/packages/$purl", component: PackageView });
const runsRoute = createRoute({ getParentRoute: () => shellRoute, path: "/runs", validateSearch: parseRunsSearch, component: RunsView });
const eventsRoute = createRoute({ getParentRoute: () => shellRoute, path: "/events", validateSearch: parseEventsSearch, component: EventsView });
const deliveriesRoute = createRoute({ getParentRoute: () => shellRoute, path: "/deliveries", validateSearch: parseDeliveriesSearch, component: DeliveriesView });

// Any other path is not found, inside the frame.
const notFoundRoute = createRoute({ getParentRoute: () => shellRoute, path: "$", component: NotFound });

// The status page is public: it sits outside the shell, which is where a
// session will be asked for (DESIGN-0004).
const statusRoute = createRoute({ getParentRoute: () => rootRoute, path: "/status", component: StatusView });

const routeTree = rootRoute.addChildren([shellRoute.addChildren([estateRoute, pullsRoute, waitingRoute, repositoriesRoute, repositoryRoute, packagesRoute, packageRoute, runsRoute, eventsRoute, deliveriesRoute, notFoundRoute]), statusRoute]);

export const router = createRouter({ routeTree, defaultErrorComponent: RouteError, defaultNotFoundComponent: NotFound });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
