import type { Hono } from "hono";

import type { Config } from "./config";
import type { Logger } from "./log";
import type { Oidc } from "./oidc";
import type { UpstreamFetch } from "./proxy";

// A probe result is reused for upstreamCacheMs, so a burst of kubelet
// probes costs the API one request; one probe gets upstreamTimeoutMs.
const upstreamCacheMs = 10_000;
const upstreamTimeoutMs = 2_000;

export interface HealthDeps {
  config: Config;
  oidc: Oidc;
  log: Logger;
  fetch: UpstreamFetch;
  now?: () => number;
}

// mountHealth adds /healthz (the process is up) and /readyz (discovery
// has loaded and the API answers its /healthz).
export function mountHealth(app: Hono, deps: HealthDeps): void {
  const now = deps.now ?? Date.now;
  let cached: { ok: boolean; at: number } | undefined;

  const upstreamOK = async (): Promise<boolean> => {
    if (cached && now() - cached.at < upstreamCacheMs) {
      return cached.ok;
    }
    let ok = false;
    try {
      const res = await deps.fetch(new URL("/healthz", deps.config.apiUpstream.origin), {
        method: "GET",
        signal: AbortSignal.timeout(upstreamTimeoutMs),
      });
      ok = res.ok;
    } catch (err) {
      deps.log("warn", "api upstream health probe failed", { error: String(err) });
    }
    cached = { ok, at: now() };
    return ok;
  };

  // issuerOK retries a discovery that failed at start.
  const issuerOK = async (): Promise<boolean> => {
    if (deps.oidc.ready()) {
      return true;
    }
    try {
      await deps.oidc.configuration();
      return true;
    } catch (err) {
      deps.log("warn", "issuer discovery failed", { error: String(err) });
      return false;
    }
  };

  app.get("/healthz", (c) => c.text("ok"));
  app.get("/readyz", async (c) => {
    const [issuer, upstream] = await Promise.all([issuerOK(), upstreamOK()]);
    return c.json({ issuer, upstream }, issuer && upstream ? 200 : 503, { "cache-control": "no-store" });
  });
}
