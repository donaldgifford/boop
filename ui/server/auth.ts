import type { Hono } from "hono";
import * as client from "openid-client";

import type { Config } from "./config";
import type { Logger } from "./log";
import type { Oidc } from "./oidc";
import { safeReturnPath } from "./redirect";
import { newSessionExpiry, SealedCookie, type Session } from "./session";

// LoginTransaction carries the PKCE verifier, state, nonce and return
// path from /auth/login to /auth/callback, in its own sealed cookie.
interface LoginTransaction {
  verifier: string;
  state: string;
  nonce: string;
  returnTo: string;
}

// loginTtlSeconds bounds how long a sign-in may sit at the issuer.
const loginTtlSeconds = 600;

// exchangeShareMs is how long a finished code exchange is still shared
// with a duplicate callback.
const exchangeShareMs = 30_000;

type Tokens = Awaited<ReturnType<typeof client.authorizationCodeGrant>>;

export interface AuthDeps {
  config: Config;
  oidc: Oidc;
  session: SealedCookie<Session>;
  log: Logger;
}

// mountAuth adds /auth/login, /auth/callback and /auth/logout: the code
// flow with PKCE as the confidential client boopd-ui (DESIGN-0004).
export function mountAuth(app: Hono, { config, oidc, session, log }: AuthDeps): void {
  const login = new SealedCookie<LoginTransaction>("boop_login", config.sessionKeys);
  const redirectUri = new URL("/auth/callback", config.publicUrl).toString();

  // A browser can reach the callback twice for one sign-in (a prefetch, a
  // double navigation). The issuer redeems a code once, so the exchange
  // is shared per (state, code): the second request gets the first's
  // tokens rather than an error. Per replica.
  const exchanges = new Map<string, Promise<Tokens>>();
  const exchangeOnce = (key: string, run: () => Promise<Tokens>): Promise<Tokens> => {
    const found = exchanges.get(key);
    if (found) {
      return found;
    }
    const p = run();
    exchanges.set(key, p);
    const forget = () => setTimeout(() => exchanges.delete(key), exchangeShareMs).unref();
    p.then(forget, forget);
    return p;
  };

  app.get("/auth/login", async (c) => {
    const oc = await oidc.configuration();
    const tx: LoginTransaction = {
      verifier: client.randomPKCECodeVerifier(),
      state: client.randomState(),
      nonce: client.randomNonce(),
      returnTo: safeReturnPath(c.req.query("return_to"), config.publicUrl),
    };
    await login.write(c, tx, newSessionExpiry(loginTtlSeconds));

    // The operator's parameters first, so the flow's own always win.
    const to = client.buildAuthorizationUrl(oc, {
      ...config.oidc.authorizationParams,
      redirect_uri: redirectUri,
      scope: config.oidc.scopes,
      code_challenge: await client.calculatePKCECodeChallenge(tx.verifier),
      code_challenge_method: "S256",
      state: tx.state,
      nonce: tx.nonce,
    });
    return c.redirect(to.toString(), 302);
  });

  app.get("/auth/callback", async (c) => {
    const tx = await login.read(c);
    login.clear(c);
    if (!tx) {
      return c.text("the sign-in expired or was not started here; start again at /auth/login", 400);
    }

    // The issuer sent the browser to PUBLIC_URL; behind a proxy the
    // request URL may carry an internal host, so it is rebuilt.
    const req = new URL(c.req.url);
    const current = new URL(`${req.pathname}${req.search}`, config.publicUrl);

    let tokens: Tokens;
    try {
      const oc = await oidc.configuration();
      tokens = await exchangeOnce(`${tx.state}\n${current.searchParams.get("code") ?? ""}`, () =>
        client.authorizationCodeGrant(oc, current, {
          pkceCodeVerifier: tx.verifier,
          expectedState: tx.state,
          expectedNonce: tx.nonce,
          idTokenExpected: true,
        }),
      );
    } catch (err) {
      log("warn", "sign-in callback rejected", { error: String(err) });
      return c.text("sign-in failed", 400);
    }

    const claims = tokens.claims();
    if (!claims) {
      return c.text("sign-in failed", 400);
    }
    const now = Math.floor(Date.now() / 1000);
    const s: Session = {
      sub: claims.sub,
      name: displayName(claims),
      accessToken: tokens.access_token,
      accessTokenExpiresAt: now + (tokens.expiresIn() ?? 0),
      expiresAt: newSessionExpiry(config.sessionTtlSeconds),
      ...(tokens.refresh_token ? { refreshToken: tokens.refresh_token } : {}),
      ...(tokens.id_token ? { idToken: tokens.id_token } : {}),
    };
    await session.write(c, s, s.expiresAt);
    log("info", "signed in", { sub: s.sub });
    return c.redirect(tx.returnTo, 302);
  });

  app.post("/auth/logout", async (c) => {
    // CSRF: sign-out is a POST from our own pages only.
    if (c.req.header("origin") !== config.publicUrl.origin) {
      return c.text("forbidden", 403);
    }
    const s = await session.read(c);
    session.clear(c);

    let target = "/";
    try {
      const oc = await oidc.configuration();
      const meta = oc.serverMetadata();
      if (s?.refreshToken && meta.revocation_endpoint) {
        await client.tokenRevocation(oc, s.refreshToken, { token_type_hint: "refresh_token" });
      }
      if (meta.end_session_endpoint) {
        target = client
          .buildEndSessionUrl(oc, {
            post_logout_redirect_uri: new URL("/", config.publicUrl).toString(),
            ...(s?.idToken ? { id_token_hint: s.idToken } : {}),
          })
          .toString();
      }
    } catch (err) {
      // The local session is gone either way; the issuer's side is best effort.
      log("warn", "sign-out: revocation or end-session failed", { error: String(err) });
    }
    return c.redirect(target, 303);
  });
}

function displayName(claims: { sub: string; [k: string]: unknown }): string {
  for (const k of ["name", "preferred_username", "email"]) {
    const v = claims[k];
    if (typeof v === "string" && v) {
      return v;
    }
  }
  return claims.sub;
}
