import { ApiError, parseProblem, problemRequestId } from "@donaldgifford/design-system/lib/errors";
import createClient from "openapi-fetch";

import type { components, paths } from "./schema.gen";

export type Schemas = components["schemas"];

// api calls the same-origin BFF, which adds the session's token: the
// browser never holds one (DESIGN-0004 § The UI).
export const api = createClient<paths>({ baseUrl: "/api/v1" });

// signInPath is where a lapsed session goes, coming back to the page.
export function signInPath(location: { pathname: string; search: string }): string {
  return `/auth/login?return_to=${encodeURIComponent(location.pathname + location.search)}`;
}

// toApiError reads a failed call's body as an RFC 9457 problem with the
// package's helpers, falling back to the status when it is not one (the
// BFF's own 401 and 502 bodies, say).
export function toApiError(body: unknown, response: Response): ApiError {
  const requestId = response.headers.get("x-request-id") ?? undefined;
  const p = parseProblem(body);
  if (p) {
    return new ApiError(p.status, p.reason ?? problemType(p.type), p.detail ?? p.title, problemRequestId(p) ?? requestId, p);
  }
  return new ApiError(response.status, undefined, response.statusText || `HTTP ${response.status}`, requestId);
}

// problemType is boopd's machine-readable code: the problem's type when
// it is a bare word (workflow_missing, temporal_unavailable).
function problemType(type: string): string | undefined {
  return /^[a-z_]+$/.test(type) ? type : undefined;
}

interface Call<T> {
  data?: T;
  error?: unknown;
  response: Response;
}

// unwrap is a call's data, or throws its ApiError. A 401 means the
// session lapsed, so the page goes through sign-in and comes back.
export async function unwrap<T>(call: Promise<Call<T>>, onUnauthenticated = reauthenticate): Promise<T> {
  const { data, error, response } = await call;
  if (response.ok && data !== undefined) {
    return data;
  }
  if (response.status === 401) {
    onUnauthenticated();
  }
  throw toApiError(error, response);
}

function reauthenticate(): void {
  window.location.assign(signInPath(window.location));
}
