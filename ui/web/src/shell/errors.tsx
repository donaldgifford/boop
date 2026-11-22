import { EmptyState } from "@donaldgifford/design-system/blocks/empty-state";
import { Button } from "@donaldgifford/design-system/components/button";
import { isApiError } from "@donaldgifford/design-system/lib/errors";
import { RouteErrorLayout } from "@donaldgifford/design-system/shell/route-error";
import { IconLock, IconMapOff } from "@tabler/icons-react";
import { type ErrorComponentProps, Link, useRouter } from "@tanstack/react-router";

// RouteError is every route's error boundary: an API failure shows its
// reason, status and request id; anything else its message. Retry
// re-runs the route.
export function RouteError({ error, reset }: ErrorComponentProps) {
  const router = useRouter();
  const retry = () => {
    reset();
    void router.invalidate();
  };
  if (isApiError(error)) {
    return <RouteErrorLayout title={error.reason ?? error.problem?.title ?? "Request failed"} message={error.message} status={error.status} requestId={error.requestId} onRetry={retry} />;
  }
  return <RouteErrorLayout title="Something went wrong" message={error instanceof Error ? error.message : String(error)} onRetry={retry} />;
}

// NotFound is an unknown route, and an owner or repository the caller
// cannot see: the API answers both with 404 (DESIGN-0004 § Authorisation).
export function NotFound() {
  return (
    <EmptyState
      icon={IconMapOff}
      title="Not found"
      description="There is nothing here, or it belongs to an owner you cannot see."
      action={
        <Button variant="outline" size="sm" nativeButton={false} render={<Link to="/" />}>
          Back to the estate
        </Button>
      }
    />
  );
}

// NoAccess is a caller whose groups map to no owner: signed in, but the
// API answers 403 everywhere.
export function NoAccess({ name }: { name?: string | undefined }) {
  return (
    <EmptyState
      icon={IconLock}
      title="No owners visible"
      description={`${name ? `${name}, your` : "Your"} groups map to no GitHub owner in boop's authorisation file. Ask a platform operator to add one.`}
      action={
        <form method="post" action="/auth/logout">
          <Button type="submit" variant="outline" size="sm">
            Sign out
          </Button>
        </form>
      }
    />
  );
}
