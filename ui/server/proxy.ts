import type { Context, Hono } from "hono";
import type { StatusCode } from "hono/utils/http-status";
import * as client from "openid-client";

import type { Config } from "./config";
import type { Logger } from "./log";
import type { Oidc } from "./oidc";
import type { SealedCookie, Session } from "./session";

// publicApiPaths are the operations api/openapi.yaml declares with
// `security: []`, proxied without a token. proxy.test.ts holds this list
// to the spec.
export const publicApiPaths = new Set(["/api/v1/status", "/api/v1/openapi.yaml"]);

// actionPaths are the POSTs the proxy forwards: the API's actions,
// under the same Origin check as sign-out, since a cross-site form could
// otherwise fire one with the user's session.
const actionPaths = [/^\/api\/v1\/repositories\/[1-9][0-9]*\/recheck$/];

// refreshSkewSeconds: a token expiring within this is refreshed before
// it is forwarded.
const refreshSkewSeconds = 60;

// Hop-by-hop headers never cross the proxy (RFC 9110 § 7.6.1). Nor do
// Cookie (the session is the BFF's), Host and Authorization, which is set
// from one place below.
const dropRequest = new Set([
  "connection",
  "keep-alive",
  "proxy-authenticate",
  "proxy-authorization",
  "proxy-connection",
  "te",
  "trailer",
  "transfer-encoding",
  "upgrade",
  "host",
  "cookie",
  "authorization",
  "content-length",
]);

const dropResponse = new Set([
  "connection",
  "keep-alive",
  "proxy-authenticate",
  "proxy-connection",
  "te",
  "trailer",
  "transfer-encoding",
  "upgrade",
  "set-cookie",
  "content-encoding",
  "content-length",
]);

// UpstreamFetch is the part of fetch the proxy and health probe use.
export type UpstreamFetch = (url: URL, init: RequestInit) => Promise<Response>;

export interface ProxyDeps {
  config: Config;
  oidc: Oidc;
  session: SealedCookie<Session>;
  log: Logger;
  fetch?: UpstreamFetch;
}

// mountProxy forwards /api/* to API_UPSTREAM: GET and HEAD, and the
// action POSTs from our own origin. The Authorization header comes from
// one place: the caller's own Bearer if it sent one, else the session.
export function mountProxy(app: Hono, deps: ProxyDeps): void {
  const upstreamFetch: UpstreamFetch = deps.fetch ?? fetch;

  app.all("/api/*", async (c) => {
    const method = c.req.method;
    const url = new URL(c.req.url);
    const isAction = method === "POST" && actionPaths.some((p) => p.test(url.pathname));
    if (method !== "GET" && method !== "HEAD" && !isAction) {
      return c.json({ error: "method_not_allowed", message: "the API is read-only here" }, 405, { allow: "GET, HEAD" });
    }
    if (isAction && c.req.header("origin") !== deps.config.publicUrl.origin) {
      return c.json({ error: "forbidden", message: "cross-origin request refused" }, 403);
    }

    const requestId = c.req.header("x-request-id") || crypto.randomUUID();
    const auth = await authorization(c, deps, publicApiPaths.has(url.pathname));
    if (auth.kind === "unauthenticated") {
      return c.json({ error: "unauthenticated", message: "sign in at /auth/login" }, 401, { "x-request-id": requestId });
    }

    // The upstream's origin plus the request's path: nothing in the
    // request chooses the host.
    const target = new URL(`${deps.config.apiUpstream.origin}${url.pathname}${url.search}`);
    const named = new Set(
      (c.req.header("connection") ?? "")
        .split(",")
        .map((h) => h.trim().toLowerCase())
        .filter(Boolean),
    );
    const headers = new Headers();
    c.req.raw.headers.forEach((v, k) => {
      if (!dropRequest.has(k) && !named.has(k)) {
        headers.set(k, v);
      }
    });
    headers.set("x-request-id", requestId);
    if (auth.kind === "bearer") {
      headers.set("authorization", auth.header);
    }

    let upstream: Response;
    try {
      upstream = await upstreamFetch(target, { method, headers, redirect: "manual" });
    } catch (err) {
      deps.log("error", "api upstream unreachable", { request_id: requestId, error: String(err) });
      return c.json({ error: "bad_gateway", message: "the API is unreachable" }, 502, { "x-request-id": requestId });
    }

    // Through c, so a session cookie rewritten by a refresh is kept.
    upstream.headers.forEach((v, k) => {
      if (!dropResponse.has(k)) {
        c.header(k, v, { append: true });
      }
    });
    c.header("x-request-id", requestId);
    c.status(upstream.status as StatusCode);
    return c.newResponse(method === "HEAD" ? null : upstream.body);
  });
}

type Authorization = { kind: "bearer"; header: string } | { kind: "anonymous" } | { kind: "unauthenticated" };

async function authorization(c: Context, deps: ProxyDeps, isPublic: boolean): Promise<Authorization> {
  const own = c.req.header("authorization");
  if (own && /^Bearer\s+\S/i.test(own)) {
    return { kind: "bearer", header: own };
  }
  const nobody: Authorization = isPublic ? { kind: "anonymous" } : { kind: "unauthenticated" };
  const s = await deps.session.read(c);
  if (!s) {
    return nobody;
  }
  const fresh = await ensureFresh(c, deps, s);
  if (!fresh) {
    deps.session.clear(c);
    return nobody;
  }
  return { kind: "bearer", header: `Bearer ${fresh.accessToken}` };
}

// ensureFresh refreshes an access token inside the skew and writes the
// session back; undefined when the token is stale and cannot be.
async function ensureFresh(c: Context, deps: ProxyDeps, s: Session): Promise<Session | undefined> {
  const now = Math.floor(Date.now() / 1000);
  if (s.accessTokenExpiresAt - now > refreshSkewSeconds) {
    return s;
  }
  if (!s.refreshToken) {
    return s.accessTokenExpiresAt > now ? s : undefined;
  }
  try {
    const tokens = await client.refreshTokenGrant(await deps.oidc.configuration(), s.refreshToken);
    const next: Session = {
      ...s,
      accessToken: tokens.access_token,
      accessTokenExpiresAt: now + (tokens.expiresIn() ?? 0),
      refreshToken: tokens.refresh_token ?? s.refreshToken,
      ...(tokens.id_token ? { idToken: tokens.id_token } : {}),
    };
    // expiresAt is kept: a refresh never extends the session.
    await deps.session.write(c, next, next.expiresAt);
    return next;
  } catch (err) {
    deps.log("warn", "token refresh failed; signing out", { sub: s.sub, error: String(err) });
    return undefined;
  }
}
