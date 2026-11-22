// The DOM must exist before the overlays load: keep this first.
import { withDOM } from "../../testing/dom";

import { expect, test } from "bun:test";
import { ApiError } from "@donaldgifford/design-system/lib/errors";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { cleanup, render } from "@testing-library/react";

import { RouteError } from "./errors";

withDOM(cleanup);

function throwing(error: unknown) {
  const root = createRootRoute({
    component: () => {
      throw error;
    },
    errorComponent: RouteError,
  });
  return createRouter({ routeTree: root, history: createMemoryHistory({ initialEntries: ["/"] }) });
}

test("an API failure shows its reason, status and request id, and Retry", async () => {
  const router = throwing(new ApiError(409, "workflow_missing", "repository 3 has no RepoWorkflow", "req-9"));
  const { findByRole, getByText, getByRole } = render(<RouterProvider router={router} />);
  const alert = await findByRole("alert");
  expect(alert.textContent).toContain("workflow_missing");
  expect(alert.textContent).toContain("HTTP 409");
  expect(getByText("repository 3 has no RepoWorkflow")).toBeTruthy();
  expect(getByText("request req-9")).toBeTruthy();
  expect(getByRole("button", { name: "Retry" })).toBeTruthy();
});

test("any other failure shows its message", async () => {
  const { findByRole } = render(<RouterProvider router={throwing(new Error("boom"))} />);
  const alert = await findByRole("alert");
  expect(alert.textContent).toContain("Something went wrong");
  expect(alert.textContent).toContain("boom");
});
