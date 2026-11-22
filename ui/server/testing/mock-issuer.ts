// A small OIDC provider for the BFF's tests and the UI's local and e2e
// runs: discovery, JWKS, an authorize endpoint that approves at once, the
// token endpoint (authorization_code with PKCE, refresh_token), RFC 7009
// revocation and end-session. It records what it saw.
//
// Clients may authenticate with client_secret_post or client_secret_basic.
// An `audience` authorize parameter, or the apiAudience option, makes the
// access token an RS256 JWT for it (what boopd's API verifies); otherwise
// the access token is opaque.
//
// The `users` option lets a browser pick who signs in: /authorize reads
// the issuer's own `mock_user` cookie and signs that user's subject, name
// and groups instead of the defaults (the Playwright suite's operator and
// viewer).
import { createHash, randomBytes } from "node:crypto";

import { exportJWK, generateKeyPair, SignJWT } from "jose";

export type ClientAuthMethod = "client_secret_post" | "client_secret_basic";

export interface MockIssuerOptions {
  port?: number;
  clientId?: string;
  clientSecret?: string;
  // accessTokenTtl is in seconds.
  accessTokenTtl?: number;
  sub?: string;
  name?: string;
  apiAudience?: string;
  groups?: string[];
  users?: Record<string, MockUser>;
  // publicURL is the issuer as its clients name it (a Service name in the
  // e2e cluster); the default is http://127.0.0.1:<port>.
  publicURL?: string;
  hostname?: string;
  // tls serves https with these PEMs.
  tls?: { cert: string; key: string };
}

export interface MockUser {
  sub: string;
  name: string;
  groups: string[];
}

export interface MockIssuer {
  url: URL;
  // revoked lists the tokens revoked, in order.
  readonly revoked: string[];
  // refreshes counts refresh_token grants.
  readonly refreshes: number;
  // authMethods lists how the client authenticated, per token or
  // revocation request.
  readonly authMethods: ClientAuthMethod[];
  // authorize follows an authorization URL as a browser would and returns
  // the callback URL the issuer sends it back to.
  authorize(authorizationUrl: string): Promise<URL>;
  stop(): void;
}

interface Grant {
  redirectUri: string;
  nonce: string;
  challenge: string;
  audience: string | undefined;
  user: MockUser;
}

export async function startMockIssuer(opts: MockIssuerOptions = {}): Promise<MockIssuer> {
  const clientId = opts.clientId ?? "boopd-ui";
  const clientSecret = opts.clientSecret ?? "client-secret";
  const ttl = opts.accessTokenTtl ?? 300;
  const fallback: MockUser = { sub: opts.sub ?? "user-1", name: opts.name ?? "Grace Hopper", groups: opts.groups ?? [] };
  const userFrom = (req: Request): MockUser => {
    const picked = /(?:^|;\s*)mock_user=([^;]+)/.exec(req.headers.get("cookie") ?? "")?.[1];
    return (picked && opts.users?.[decodeURIComponent(picked)]) || fallback;
  };

  const { privateKey, publicKey } = await generateKeyPair("RS256");
  const jwk = { ...(await exportJWK(publicKey)), kid: "k1", alg: "RS256", use: "sig" };

  const codes = new Map<string, Grant>();
  // refreshTokens maps a live refresh token to its grant's audience.
  const refreshTokens = new Map<string, { audience: string | undefined; user: MockUser }>();
  const revoked: string[] = [];
  const authMethods: ClientAuthMethod[] = [];
  let refreshes = 0;
  let base = "";

  const sign = (sub: string, claims: Record<string, unknown>, typ: string | undefined, aud: string | string[], exp: string) =>
    new SignJWT(claims)
      .setProtectedHeader({ alg: "RS256", kid: "k1", ...(typ ? { typ } : {}) })
      .setIssuer(base)
      .setAudience(aud)
      .setSubject(sub)
      .setIssuedAt()
      .setExpirationTime(exp)
      .sign(privateKey);

  const accessToken = (audience: string | undefined, user: MockUser): Promise<string> => {
    const aud = [opts.apiAudience, audience].filter((a): a is string => Boolean(a));
    if (aud.length === 0) {
      return Promise.resolve(`at-${randomBytes(8).toString("hex")}`);
    }
    return sign(user.sub, { azp: clientId, groups: user.groups, name: user.name, preferred_username: "grace" }, "at+jwt", aud, `${ttl}s`);
  };

  const issue = async (nonce: string | undefined, audience: string | undefined, user: MockUser) => {
    const refresh = randomBytes(16).toString("hex");
    refreshTokens.set(refresh, { audience, user });
    return {
      access_token: await accessToken(audience, user),
      token_type: "Bearer",
      expires_in: ttl,
      refresh_token: refresh,
      id_token: await sign(user.sub, { nonce, name: user.name, preferred_username: "grace" }, undefined, clientId, "5m"),
    };
  };

  // authenticate reads the client's credentials from a Basic header (each
  // part form-urlencoded, RFC 6749 § 2.3.1) or the form body.
  const authenticate = (req: Request, form: URLSearchParams): boolean => {
    const header = req.headers.get("authorization");
    if (header?.startsWith("Basic ")) {
      authMethods.push("client_secret_basic");
      const decoded = Buffer.from(header.slice(6), "base64").toString();
      const colon = decoded.indexOf(":");
      const unform = (s: string) => decodeURIComponent(s.replaceAll("+", " "));
      return colon >= 0 && unform(decoded.slice(0, colon)) === clientId && unform(decoded.slice(colon + 1)) === clientSecret;
    }
    authMethods.push("client_secret_post");
    return form.get("client_id") === clientId && form.get("client_secret") === clientSecret;
  };

  const token = async (req: Request): Promise<Response> => {
    const form = new URLSearchParams(await req.text());
    if (!authenticate(req, form)) {
      return Response.json({ error: "invalid_client" }, { status: 401 });
    }
    switch (form.get("grant_type")) {
      case "authorization_code": {
        const code = form.get("code") ?? "";
        const grant = codes.get(code);
        codes.delete(code);
        const challenge = createHash("sha256")
          .update(form.get("code_verifier") ?? "")
          .digest("base64url");
        if (!grant || grant.challenge !== challenge || grant.redirectUri !== form.get("redirect_uri")) {
          return Response.json({ error: "invalid_grant" }, { status: 400 });
        }
        return Response.json(await issue(grant.nonce, grant.audience, grant.user));
      }
      case "refresh_token": {
        const rt = form.get("refresh_token") ?? "";
        if (!refreshTokens.has(rt)) {
          return Response.json({ error: "invalid_grant" }, { status: 400 });
        }
        const prior = refreshTokens.get(rt);
        refreshTokens.delete(rt);
        refreshes++;
        return Response.json(await issue(undefined, prior?.audience, prior?.user ?? fallback));
      }
      default:
        return Response.json({ error: "unsupported_grant_type" }, { status: 400 });
    }
  };

  const server = Bun.serve({
    port: opts.port ?? 0,
    ...(opts.hostname ? { hostname: opts.hostname } : {}),
    ...(opts.tls ? { tls: opts.tls } : {}),
    async fetch(req) {
      const url = new URL(req.url);
      switch (url.pathname) {
        case "/.well-known/openid-configuration":
          return Response.json({
            issuer: base,
            authorization_endpoint: `${base}/authorize`,
            token_endpoint: `${base}/token`,
            jwks_uri: `${base}/jwks`,
            revocation_endpoint: `${base}/revoke`,
            end_session_endpoint: `${base}/logout`,
            response_types_supported: ["code"],
            subject_types_supported: ["public"],
            id_token_signing_alg_values_supported: ["RS256"],
            code_challenge_methods_supported: ["S256"],
          });
        case "/jwks":
          return Response.json({ keys: [jwk] });
        case "/authorize": {
          const p = url.searchParams;
          const code = randomBytes(12).toString("hex");
          codes.set(code, {
            redirectUri: p.get("redirect_uri") ?? "",
            nonce: p.get("nonce") ?? "",
            challenge: p.get("code_challenge") ?? "",
            audience: p.get("audience") ?? undefined,
            user: userFrom(req),
          });
          const back = new URL(p.get("redirect_uri") ?? "");
          back.searchParams.set("code", code);
          back.searchParams.set("state", p.get("state") ?? "");
          back.searchParams.set("iss", base);
          return Response.redirect(back.toString(), 302);
        }
        case "/token":
          return token(req);
        case "/revoke": {
          const form = new URLSearchParams(await req.text());
          if (!authenticate(req, form)) {
            return Response.json({ error: "invalid_client" }, { status: 401 });
          }
          const t = form.get("token") ?? "";
          revoked.push(t);
          refreshTokens.delete(t);
          return new Response(null, { status: 200 });
        }
        case "/logout":
          return new Response("signed out");
        default:
          return new Response("not found", { status: 404 });
      }
    },
  });
  base = opts.publicURL?.replace(/\/$/, "") ?? `http://127.0.0.1:${server.port}`;

  return {
    url: new URL(base),
    revoked,
    get refreshes() {
      return refreshes;
    },
    authMethods,
    async authorize(authorizationUrl) {
      const res = await fetch(authorizationUrl, { redirect: "manual" });
      return new URL(res.headers.get("location") ?? "");
    },
    stop: () => void server.stop(true),
  };
}
