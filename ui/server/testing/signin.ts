import { createApp } from "../app";
import { type Config, loadConfig } from "../config";
import { discardLogger, type Logger } from "../log";
import type { UpstreamFetch } from "../proxy";
import { Browser } from "./browser";
import { validEnv } from "./env";
import type { MockIssuer } from "./mock-issuer";

export interface Harness {
  config: Config;
  browser: Browser;
}

export interface HarnessOptions {
  env?: Record<string, string>;
  log?: Logger;
  upstreamFetch?: UpstreamFetch;
  staticDir?: string;
  // edit changes the loaded config, bypassing validation on purpose.
  edit?: (c: Config) => void;
}

// harness is the BFF against issuer, driven by a Browser.
export function harness(issuer: MockIssuer, opts: HarnessOptions = {}): Harness {
  const config = loadConfig({ ...validEnv, OIDC_ISSUER: issuer.url.toString(), ...opts.env });
  opts.edit?.(config);
  const app = createApp({
    config,
    log: opts.log ?? discardLogger,
    ...(opts.upstreamFetch ? { upstreamFetch: opts.upstreamFetch } : {}),
    ...(opts.staticDir ? { staticDir: opts.staticDir } : {}),
  });
  return { config, browser: new Browser(app) };
}

// signIn runs the whole code flow and returns the callback's response.
export async function signIn(issuer: MockIssuer, h: Harness, returnTo?: string): Promise<{ login: Response; callback: Response }> {
  const login = await h.browser.request(returnTo === undefined ? "/auth/login" : `/auth/login?return_to=${encodeURIComponent(returnTo)}`);
  const back = await issuer.authorize(login.headers.get("location") ?? "");
  const callback = await h.browser.request(`${back.pathname}${back.search}`);
  return { login, callback };
}
