import { afterAll, beforeAll, describe, expect, test } from "bun:test";

import { publicApiPaths, type UpstreamFetch } from "./proxy";
import { type Session, sessionCookie } from "./session";
import { type MockIssuer, startMockIssuer } from "./testing/mock-issuer";
import { type Harness, harness, signIn } from "./testing/signin";

const sessionName = "__Host-boop_session";
const ours = "https://boop.example.com";

interface Seen {
  url: string;
  method: string;
  headers: Headers;
}

let issuer: MockIssuer;
let shortIssuer: MockIssuer;

beforeAll(async () => {
  issuer = await startMockIssuer();
  // Its tokens are inside the refresh window from the start.
  shortIssuer = await startMockIssuer({ accessTokenTtl: 30 });
});

afterAll(() => {
  issuer.stop();
  shortIssuer.stop();
});

function setup(from = issuer, reply: () => Response = () => Response.json({ ok: true })): Harness & { seen: Seen[] } {
  const seen: Seen[] = [];
  const upstreamFetch: UpstreamFetch = async (url, init) => {
    seen.push({ url: url.toString(), method: init.method ?? "GET", headers: new Headers(init.headers) });
    return reply();
  };
  return { ...harness(from, { upstreamFetch }), seen };
}

async function signedIn(from = issuer, reply?: () => Response) {
  const h = setup(from, reply);
  await signIn(from, h);
  return h;
}

describe("api proxy", () => {
  test("a signed-in GET goes upstream with the session's token and no cookie", async () => {
    const { browser, seen } = await signedIn();
    const res = await browser.request("/api/v1/pull-requests?state=open");
    expect(res.status).toBe(200);
    expect(seen).toHaveLength(1);
    expect(seen[0]?.url).toBe("http://boopd-api/api/v1/pull-requests?state=open");
    expect(seen[0]?.headers.get("authorization")).toMatch(/^Bearer \S+$/);
    expect(seen[0]?.headers.get("cookie")).toBeNull();
  });

  test("anything but GET and HEAD is refused", async () => {
    const { browser, seen } = await signedIn();
    for (const method of ["POST", "PUT", "PATCH", "DELETE"]) {
      const res = await browser.request("/api/v1/repositories", { method, headers: { origin: ours } });
      expect(res.status).toBe(405);
      expect(res.headers.get("allow")).toBe("GET, HEAD");
    }
    expect(seen).toHaveLength(0);
  });

  test("the recheck POST is forwarded from our own origin only", async () => {
    const { browser, seen } = await signedIn();
    const path = "/api/v1/repositories/42/recheck";
    expect((await browser.request(path, { method: "POST", headers: { origin: "https://evil.example" } })).status).toBe(403);
    expect((await browser.request(path, { method: "POST" })).status).toBe(403);
    expect(seen).toHaveLength(0);

    const res = await browser.request(path, { method: "POST", headers: { origin: ours } });
    expect(res.status).toBe(200);
    expect(seen).toHaveLength(1);
    expect(seen[0]?.method).toBe("POST");
    expect(seen[0]?.url).toBe(`http://boopd-api${path}`);
    expect(seen[0]?.headers.get("authorization")).toMatch(/^Bearer \S+$/);
  });

  test("no other POST, even from our origin", async () => {
    const { browser, seen } = await signedIn();
    for (const path of ["/api/v1/repositories/42", "/api/v1/repositories/x/recheck", "/api/v1/repositories/0/recheck", "/api/v1/repositories/42/recheck/x"]) {
      expect((await browser.request(path, { method: "POST", headers: { origin: ours } })).status).toBe(405);
    }
    expect(seen).toHaveLength(0);
  });

  test("HEAD is forwarded and answered without a body", async () => {
    const { browser, seen } = await signedIn();
    const res = await browser.request("/api/v1/estate", { method: "HEAD" });
    expect(res.status).toBe(200);
    expect(await res.text()).toBe("");
    expect(seen[0]?.method).toBe("HEAD");
  });

  test("the request never chooses the upstream host", async () => {
    const { browser, seen } = await signedIn();
    await browser.request("/api/v1/estate", { headers: { host: "evil.example", "x-forwarded-host": "evil.example", forwarded: "host=evil.example" } });
    expect(new URL(seen[0]?.url ?? "").host).toBe("boopd-api");
  });

  test("hop-by-hop headers, and any named in Connection, are stripped", async () => {
    const { browser, seen } = await signedIn();
    await browser.request("/api/v1/estate", {
      headers: {
        connection: "keep-alive, x-secret-hop",
        "keep-alive": "timeout=5",
        "x-secret-hop": "1",
        "proxy-authorization": "Basic Zm9vOmJhcg==",
        te: "trailers",
        upgrade: "websocket",
        accept: "application/json",
      },
    });
    const h = seen[0]?.headers ?? new Headers();
    for (const name of ["connection", "keep-alive", "x-secret-hop", "proxy-authorization", "te", "upgrade", "cookie"]) {
      expect(h.get(name)).toBeNull();
    }
    expect(h.get("accept")).toBe("application/json");
  });

  test("the upstream's hop-by-hop headers and Set-Cookie are stripped", async () => {
    const { browser } = await signedIn(issuer, () =>
      Response.json({}, { headers: { "set-cookie": "upstream=1", connection: "close", "cache-control": "no-store" } }),
    );
    const res = await browser.request("/api/v1/estate");
    expect(res.headers.getSetCookie()).toHaveLength(0);
    expect(res.headers.get("connection")).toBeNull();
    expect(res.headers.get("cache-control")).toBe("no-store");
  });

  test("a caller's own Bearer goes through untouched, with or without a session", async () => {
    const signed = await signedIn();
    await signed.browser.request("/api/v1/estate", { headers: { authorization: "Bearer caller-token" } });
    expect(signed.seen[0]?.headers.get("authorization")).toBe("Bearer caller-token");

    const bare = setup();
    expect((await bare.browser.request("/api/v1/estate", { headers: { authorization: "Bearer caller-token" } })).status).toBe(200);
    expect(bare.seen[0]?.headers.get("authorization")).toBe("Bearer caller-token");
  });

  test("an anonymous call to a private endpoint is 401 and never goes upstream", async () => {
    const { browser, seen } = setup();
    expect((await browser.request("/api/v1/repositories")).status).toBe(401);
    expect(seen).toHaveLength(0);
  });

  test("public endpoints go without a token", async () => {
    const { browser, seen } = setup();
    for (const path of publicApiPaths) {
      expect((await browser.request(path)).status).toBe(200);
    }
    expect(seen).toHaveLength(publicApiPaths.size);
    for (const s of seen) {
      expect(s.headers.get("authorization")).toBeNull();
    }
  });

  test("X-Request-ID is carried, and minted when absent", async () => {
    const { browser, seen } = await signedIn();
    const given = await browser.request("/api/v1/estate", { headers: { "x-request-id": "req-123" } });
    expect(given.headers.get("x-request-id")).toBe("req-123");
    expect(seen[0]?.headers.get("x-request-id")).toBe("req-123");

    const minted = await browser.request("/api/v1/estate");
    const id = minted.headers.get("x-request-id");
    expect(id).toMatch(/^[0-9a-f-]{36}$/);
    expect(seen[1]?.headers.get("x-request-id")).toBe(id);
  });

  test("an unreachable upstream is 502", async () => {
    const { browser } = await signedIn(issuer, () => {
      throw new TypeError("connection refused");
    });
    expect((await browser.request("/api/v1/estate")).status).toBe(502);
  });
});

describe("token refresh", () => {
  test("a token within 60 s of expiry is refreshed; the session's end stays", async () => {
    const { browser, seen, config } = await signedIn(shortIssuer);
    const cookies = sessionCookie(config.sessionKeys);
    const before = await cookies.open(browser.cookie(sessionName) ?? "");
    const refreshes = shortIssuer.refreshes;

    await browser.request("/api/v1/estate");
    expect(shortIssuer.refreshes).toBe(refreshes + 1);
    const after = await cookies.open(browser.cookie(sessionName) ?? "");
    expect(after?.accessToken).not.toBe(before?.accessToken);
    expect(after?.expiresAt).toBe(before?.expiresAt ?? -1);
    expect(seen[0]?.headers.get("authorization")).toBe(`Bearer ${after?.accessToken}`);
  });

  test("a token with time to spare is not refreshed", async () => {
    const { browser } = await signedIn();
    const refreshes = issuer.refreshes;
    await browser.request("/api/v1/estate");
    expect(issuer.refreshes).toBe(refreshes);
  });

  test("a failed refresh signs the user out", async () => {
    const { browser, seen } = await withSession((s) => ({ ...s, refreshToken: "forged" }));
    expect((await browser.request("/api/v1/repositories")).status).toBe(401);
    expect(seen).toHaveLength(0);
    expect(browser.cookie(sessionName)).toBeUndefined();
  });
});

// withSession signs in against the short issuer and replaces the session
// with edit's.
async function withSession(edit: (s: Session) => Session) {
  const h = await signedIn(shortIssuer);
  const cookies = sessionCookie(h.config.sessionKeys);
  const s = await cookies.open(h.browser.cookie(sessionName) ?? "");
  if (!s) {
    throw new Error("no session");
  }
  const next = edit(s);
  h.browser.jar.set(sessionName, await cookies.seal(next, next.expiresAt));
  return h;
}

describe("expiry", () => {
  test("an expired access token with no refresh token is 401", async () => {
    const now = Math.floor(Date.now() / 1000);
    const { browser, seen } = await withSession(({ refreshToken: _, ...s }) => ({ ...s, accessTokenExpiresAt: now - 1 }));
    expect((await browser.request("/api/v1/repositories")).status).toBe(401);
    expect(seen).toHaveLength(0);
  });

  test("a session past its end is 401 and never refreshed", async () => {
    const now = Math.floor(Date.now() / 1000);
    const { browser, seen } = await withSession((s) => ({ ...s, expiresAt: now - 1 }));
    const refreshes = shortIssuer.refreshes;
    expect((await browser.request("/api/v1/repositories")).status).toBe(401);
    expect(seen).toHaveLength(0);
    expect(shortIssuer.refreshes).toBe(refreshes);
  });
});

test("publicApiPaths are the operations api/openapi.yaml leaves unauthenticated", async () => {
  const doc = Bun.YAML.parse(await Bun.file(new URL("../../api/openapi.yaml", import.meta.url)).text()) as {
    servers: { url: string }[];
    paths: Record<string, Record<string, { security?: unknown[] }>>;
  };
  const base = doc.servers[0]?.url ?? "";
  const open = Object.entries(doc.paths)
    .filter(([, ops]) => Object.values(ops).some((op) => Array.isArray(op.security) && op.security.length === 0))
    .map(([p]) => `${base}${p}`);
  expect(new Set(open)).toEqual(publicApiPaths);
});

test("the only POSTs the proxy forwards are the spec's POST operations", async () => {
  const doc = Bun.YAML.parse(await Bun.file(new URL("../../api/openapi.yaml", import.meta.url)).text()) as {
    paths: Record<string, Record<string, unknown>>;
  };
  const posts = Object.entries(doc.paths)
    .filter(([, ops]) => "post" in ops)
    .map(([p]) => p);
  expect(posts).toEqual(["/repositories/{id}/recheck"]);
});
