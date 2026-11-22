import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { createHash } from "node:crypto";

import { decodeJwt } from "jose";

import { type MockIssuer, startMockIssuer } from "./mock-issuer";

// The issuer's `mock_user` cookie picks who signs in, so the Playwright
// suite can be an operator in one flow and a viewer in the next.
describe("mock issuer users", () => {
  let issuer: MockIssuer;
  beforeAll(async () => {
    issuer = await startMockIssuer({
      apiAudience: "boopd-api",
      groups: ["platform"],
      users: { viewer: { sub: "user-2", name: "Ada Lovelace", groups: ["team-web"] } },
    });
  });
  afterAll(() => issuer.stop());

  async function signInAs(cookie: string | undefined) {
    const verifier = "v".repeat(43);
    const challenge = createHash("sha256").update(verifier).digest("base64url");
    const authorize = new URL("/authorize", issuer.url);
    authorize.search = new URLSearchParams({ redirect_uri: "http://127.0.0.1/cb", state: "s", nonce: "n", code_challenge: challenge }).toString();
    const res = await fetch(authorize, { redirect: "manual", headers: cookie ? { cookie } : {} });
    const code = new URL(res.headers.get("location") ?? "").searchParams.get("code") ?? "";
    const tok = await fetch(new URL("/token", issuer.url), {
      method: "POST",
      body: new URLSearchParams({ grant_type: "authorization_code", code, code_verifier: verifier, redirect_uri: "http://127.0.0.1/cb", client_id: "boopd-ui", client_secret: "client-secret" }),
    });
    return decodeJwt(((await tok.json()) as { access_token: string }).access_token);
  }

  test.each([
    ["no cookie signs the default user", undefined, "user-1", ["platform"]],
    ["a known user signs that user", "mock_user=viewer", "user-2", ["team-web"]],
    ["an unknown user signs the default user", "mock_user=nobody", "user-1", ["platform"]],
  ])("%s", async (_, cookie, sub, groups) => {
    const claims = await signInAs(cookie);
    expect(claims.sub).toBe(sub);
    expect(claims.groups).toEqual(groups);
  });
});
