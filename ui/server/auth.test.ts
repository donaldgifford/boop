import { afterAll, beforeAll, describe, expect, test } from "bun:test";

import { sessionCookie } from "./session";
import { type MockIssuer, startMockIssuer } from "./testing/mock-issuer";
import { harness, signIn } from "./testing/signin";

const sessionName = "__Host-boop_session";

let issuer: MockIssuer;

beforeAll(async () => {
  issuer = await startMockIssuer();
});

afterAll(() => issuer.stop());

// mask hides the random values of an authorize query.
function masked(location: string | null): string {
  return new URL(location ?? "").search.replace(/(state|nonce|code_challenge)=[^&]+/g, "$1=*");
}

describe("sign-in", () => {
  test("redirects to the issuer with PKCE, state and nonce, and nothing else by default", async () => {
    const h = harness(issuer);
    const res = await h.browser.request("/auth/login");
    expect(res.status).toBe(302);
    const to = new URL(res.headers.get("location") ?? "");
    expect(to.origin).toBe(issuer.url.origin);
    expect(masked(to.toString())).toBe(
      "?redirect_uri=https%3A%2F%2Fboop.example.com%2Fauth%2Fcallback&scope=openid+profile+email+offline_access" +
        "&code_challenge=*&code_challenge_method=S256&state=*&nonce=*&client_id=boopd-ui&response_type=code",
    );
    expect(h.browser.cookie("__Host-boop_login")).toBeTruthy();
  });

  test("the callback sets the session and returns to the original path", async () => {
    const h = harness(issuer);
    const { callback } = await signIn(issuer, h, "/pulls?state=open");
    expect(callback.status).toBe(302);
    expect(callback.headers.get("location")).toBe("/pulls?state=open");
    expect(h.browser.cookie("__Host-boop_login")).toBeUndefined();

    const set = callback.headers.getSetCookie().find((c) => c.startsWith(`${sessionName}=`)) ?? "";
    for (const attr of ["HttpOnly", "Secure", "SameSite=Lax", "Path=/"]) {
      expect(set).toContain(attr);
    }
    expect(set).not.toContain("Domain");

    const s = await sessionCookie(h.config.sessionKeys).open(h.browser.cookie(sessionName) ?? "");
    expect(s).toMatchObject({ sub: "user-1", name: "Grace Hopper" });
    expect(s?.refreshToken).toBeTruthy();
    expect((s?.expiresAt ?? 0) - Math.floor(Date.now() / 1000)).toBeGreaterThan(8 * 3600 - 5);
    expect(issuer.authMethods.at(-1)).toBe("client_secret_post");
  });

  test("never returns off-origin", async () => {
    for (const evil of ["https://evil.example", "//evil.example", "/\\evil.example", "javascript:alert(1)", "/auth/logout"]) {
      const { callback } = await signIn(issuer, harness(issuer), evil);
      expect(callback.headers.get("location")).toBe("/");
    }
  });

  test("a callback with a forged state is refused", async () => {
    const h = harness(issuer);
    const login = await h.browser.request("/auth/login");
    const back = await issuer.authorize(login.headers.get("location") ?? "");
    back.searchParams.set("state", "forged");
    const res = await h.browser.request(`${back.pathname}${back.search}`);
    expect(res.status).toBe(400);
    expect(h.browser.cookie(sessionName)).toBeUndefined();
  });

  test("a callback with no sign-in in progress is refused", async () => {
    expect((await harness(issuer).browser.request("/auth/callback?code=x&state=y")).status).toBe(400);
  });

  test("a duplicate callback shares the first exchange", async () => {
    const warnings: string[] = [];
    const h = harness(issuer, { log: (level, msg) => level === "warn" && warnings.push(msg) });
    const login = await h.browser.request("/auth/login");
    const back = await issuer.authorize(login.headers.get("location") ?? "");
    const before = issuer.authMethods.length;

    const path = `${back.pathname}${back.search}`;
    const [a, b] = await Promise.all([h.browser.request(path), h.browser.request(path)]);
    expect([a.status, b.status]).toEqual([302, 302]);
    for (const res of [a, b]) {
      expect(res.headers.getSetCookie().some((c) => c.startsWith(`${sessionName}=`))).toBe(true);
    }
    expect(issuer.authMethods.length - before).toBe(1);
    expect(warnings).toEqual([]);
  });
});

describe("issuer portability", () => {
  test("client_secret_basic authenticates at the token endpoint", async () => {
    const h = harness(issuer, { env: { OIDC_TOKEN_AUTH_METHOD: "client_secret_basic" } });
    const { callback } = await signIn(issuer, h);
    expect(callback.status).toBe(302);
    expect(h.browser.cookie(sessionName)).toBeTruthy();
    expect(issuer.authMethods.at(-1)).toBe("client_secret_basic");
  });

  test("authorization params reach the issuer, and the audience the access token", async () => {
    const h = harness(issuer, { env: { OIDC_AUTHORIZATION_PARAMS: '{"audience":"boopd-api"}' } });
    const { login } = await signIn(issuer, h);
    expect(new URL(login.headers.get("location") ?? "").searchParams.get("audience")).toBe("boopd-api");

    const s = await sessionCookie(h.config.sessionKeys).open(h.browser.cookie(sessionName) ?? "");
    const payload = JSON.parse(Buffer.from((s?.accessToken ?? "").split(".")[1] ?? "", "base64url").toString()) as { aud: string[] };
    expect(payload.aud).toContain("boopd-api");
  });

  test("a reserved key cannot override the flow's own parameters", async () => {
    const h = harness(issuer, { edit: (c) => (c.oidc.authorizationParams = { state: "forged", audience: "boopd-api" }) });
    const to = new URL((await h.browser.request("/auth/login")).headers.get("location") ?? "");
    expect(to.searchParams.getAll("state")).toHaveLength(1);
    expect(to.searchParams.get("state")).not.toBe("forged");
    expect(to.searchParams.get("audience")).toBe("boopd-api");
  });
});

describe("sign-out", () => {
  test("checks Origin", async () => {
    const h = harness(issuer);
    await signIn(issuer, h);
    const res = await h.browser.request("/auth/logout", { method: "POST", headers: { origin: "https://evil.example" } });
    expect(res.status).toBe(403);
    expect(h.browser.cookie(sessionName)).toBeTruthy();
  });

  test("revokes the refresh token, clears the session and ends the issuer's", async () => {
    const h = harness(issuer);
    await signIn(issuer, h);
    const s = await sessionCookie(h.config.sessionKeys).open(h.browser.cookie(sessionName) ?? "");

    const res = await h.browser.request("/auth/logout", { method: "POST", headers: { origin: "https://boop.example.com" } });
    expect(res.status).toBe(303);
    expect(h.browser.cookie(sessionName)).toBeUndefined();
    expect(issuer.revoked).toContain(s?.refreshToken ?? "missing");

    const to = new URL(res.headers.get("location") ?? "");
    expect(`${to.origin}${to.pathname}`).toBe(`${issuer.url.origin}/logout`);
    expect(to.searchParams.get("post_logout_redirect_uri")).toBe("https://boop.example.com/");
    expect(to.searchParams.get("id_token_hint")).toBeTruthy();
  });
});
