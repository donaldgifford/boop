import { describe, expect, test } from "bun:test";
import { Hono } from "hono";

import { loadConfig } from "./config";
import { maxChunkBytes, newSessionExpiry, type SealedCookie, type Session, sessionCookie } from "./session";
import { validEnv } from "./testing/env";

const keysOf = (...secrets: string[]) => loadConfig({ ...validEnv, UI_SESSION_KEYS: secrets.join(",") }).sessionKeys;
const keyA = `A${"a".repeat(31)}`;
const keyB = `B${"b".repeat(31)}`;

function session(extra: Partial<Session> = {}): Session {
  return {
    sub: "u1",
    name: "Grace",
    accessToken: "access-token-plaintext",
    accessTokenExpiresAt: Math.floor(Date.now() / 1000) + 300,
    refreshToken: "refresh-token-plaintext",
    expiresAt: newSessionExpiry(3600),
    ...extra,
  };
}

// harness puts write, read and clear routes over one cookie, and keeps a
// jar between requests.
function harness(cookie: SealedCookie<Session>) {
  const app = new Hono();
  let value = session();
  app.get("/write", async (c) => {
    await cookie.write(c, value, value.expiresAt);
    return c.text("ok");
  });
  app.get("/read", async (c) => c.json((await cookie.read(c)) ?? null));
  app.get("/clear", (c) => {
    cookie.clear(c);
    return c.text("ok");
  });

  const jar = new Map<string, string>();
  const send = async (path: string) => {
    const res = await app.request(path, { headers: { cookie: [...jar].map(([k, v]) => `${k}=${v}`).join("; ") } });
    for (const set of res.headers.getSetCookie()) {
      const [pair = ""] = set.split(";");
      const eq = pair.indexOf("=");
      if (/Max-Age=0/.test(set)) {
        jar.delete(pair.slice(0, eq));
      } else {
        jar.set(pair.slice(0, eq), pair.slice(eq + 1));
      }
    }
    return res;
  };
  return { send, jar, set: (s: Session) => (value = s) };
}

describe("SealedCookie", () => {
  test("a session round-trips through __Host-boop_session", async () => {
    const h = harness(sessionCookie(keysOf(keyA)));
    const set = (await h.send("/write")).headers.getSetCookie()[0] ?? "";
    expect(set).toStartWith("__Host-boop_session=");
    for (const attr of ["HttpOnly", "Secure", "SameSite=Lax", "Path=/"]) {
      expect(set).toContain(attr);
    }
    expect(set).not.toContain("Domain");
    expect(await (await h.send("/read")).json()).toMatchObject({
      sub: "u1",
      accessToken: "access-token-plaintext",
      refreshToken: "refresh-token-plaintext",
    });
  });

  test("the value is an opaque JWE", async () => {
    const h = harness(sessionCookie(keysOf(keyA)));
    await h.send("/write");
    const token = h.jar.get("__Host-boop_session") ?? "";
    expect(token.split(".")).toHaveLength(5);
    expect(token).not.toContain("plaintext");
  });

  test("a large session is chunked and reassembled, and stale chunks go", async () => {
    const h = harness(sessionCookie(keysOf(keyA)));
    h.set(session({ idToken: "x".repeat(9000) }));
    await h.send("/write");
    expect(h.jar.has("__Host-boop_session")).toBe(false);
    expect(h.jar.has("__Host-boop_session.0")).toBe(true);
    expect(h.jar.has("__Host-boop_session.2")).toBe(true);
    for (const v of h.jar.values()) {
      expect(v.length).toBeLessThanOrEqual(maxChunkBytes);
    }
    expect(((await (await h.send("/read")).json()) as Session).idToken).toHaveLength(9000);

    h.set(session());
    await h.send("/write");
    expect([...h.jar.keys()]).toEqual(["__Host-boop_session"]);
  });

  test("clear removes every chunk", async () => {
    const h = harness(sessionCookie(keysOf(keyA)));
    h.set(session({ idToken: "x".repeat(9000) }));
    await h.send("/write");
    await h.send("/clear");
    expect(h.jar.size).toBe(0);
  });

  test("rotation: the old key still opens, the new key seals", async () => {
    const old = sessionCookie(keysOf(keyA));
    const rotated = sessionCookie(keysOf(keyB, keyA));
    expect(await rotated.open(await old.seal(session(), newSessionExpiry(60)))).toMatchObject({ sub: "u1" });

    const fresh = await rotated.seal(session(), newSessionExpiry(60));
    expect(await old.open(fresh)).toBeUndefined();
    expect(await sessionCookie(keysOf(keyB)).open(fresh)).toMatchObject({ sub: "u1" });
  });

  test("a dropped key no longer opens its sessions", async () => {
    const token = await sessionCookie(keysOf(keyA)).seal(session(), newSessionExpiry(60));
    expect(await sessionCookie(keysOf(keyB)).open(token)).toBeUndefined();
  });

  test("an expired or tampered token does not open", async () => {
    const c = sessionCookie(keysOf(keyA));
    expect(await c.open(await c.seal(session(), Math.floor(Date.now() / 1000) - 1))).toBeUndefined();

    const parts = (await c.seal(session(), newSessionExpiry(60))).split(".");
    parts[3] = `${parts[3]?.slice(0, -2)}AA`;
    expect(await c.open(parts.join("."))).toBeUndefined();
  });
});
