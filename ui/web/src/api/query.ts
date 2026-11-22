import { isApiError } from "@donaldgifford/design-system/lib/errors";
import { QueryClient } from "@tanstack/react-query";

// staleTime is DESIGN-0004's 30 s; the polling intervals (OQ10) are per
// query.
export const staleTimeMs = 30_000;
const maxRetries = 2;

// shouldRetry retries a server error or a network failure, never a 4xx:
// a 404 or 403 will not change by asking again.
export function shouldRetry(failures: number, error: unknown): boolean {
  if (failures >= maxRetries) {
    return false;
  }
  return !isApiError(error) || error.status >= 500;
}

export function newQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: { staleTime: staleTimeMs, retry: shouldRetry, refetchOnWindowFocus: false },
      mutations: { retry: false },
    },
  });
}
