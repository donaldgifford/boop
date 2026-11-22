import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { contentSecurityPolicy } from "./static";
import type { Browser } from "./testing/browser";
import { type MockIssuer, startMockIssuer } from "./testing/mock-issuer";
import { harness, signIn } from "./testing/signin";

let issuer: MockIssuer;
let base: string;
let dist: string;

beforeAll(async () => {
  issuer = await startMockIssuer();
  base = mkdtempSync(join(tmpdir(), "boop-ui-"));
  dist = join(base, "dist");
  mkdirSync(join(dist, "assets"), { recursive: true });
  writeFileSync(join(dist, "index.html"), "<!doctype html><div id=root></div>");
  writeFileSync(join(dist, "assets", "app-3f2a1b.js"), "console.log(1)");
  writeFileSync(join(dist, "favicon.svg"), "<svg/>");
  writeFileSync(join(base, "secret.txt"), "outside the root");
});

afterAll(() => {
  issuer.stop();
  rmSync(base, { recursive: true, force: true });
});

function browser(): Browser {
  return harness(issuer, { staticDir: dist, upstreamFetch: async () => Response.json({}) }).browser;
}

async function signedIn(): Promise<Browser> {
  const h = harness(issuer, { staticDir: dist, upstreamFetch: async () => Response.json({}) });
  await signIn(issuer, h);
  return h.browser;
}

describe("static files", () => {
  test("hashed assets are cached for a year", async () => {
    const res = await browser().request("/assets/app-3f2a1b.js");
    expect(res.status).toBe(200);
    expect(res.headers.get("cache-control")).toBe("public, max-age=31536000, immutable");
    expect(res.headers.get("content-type")).toContain("javascript");
  });

  test("a missing asset is 404, not the app", async () => {
    expect((await browser().request("/assets/gone-000000.js")).status).toBe(404);
  });

  test("index.html is never cached, and app paths fall back to it", async () => {
    const b = await signedIn();
    for (const path of ["/", "/repositories/42", "/pulls?state=open"]) {
      const res = await b.request(path);
      expect(res.status).toBe(200);
      expect(res.headers.get("cache-control")).toBe("no-cache");
      expect(await res.text()).toContain("<div id=root>");
    }
  });

  test("without a session, app paths redirect to sign-in, keeping the path", async () => {
    const res = await browser().request("/repositories/42?tab=runs");
    expect(res.status).toBe(302);
    expect(res.headers.get("location")).toBe(`/auth/login?return_to=${encodeURIComponent("/repositories/42?tab=runs")}`);
  });

  test("/status is public", async () => {
    const res = await browser().request("/status");
    expect(res.status).toBe(200);
    expect(res.headers.get("cache-control")).toBe("no-cache");
  });

  test("root build files need no session", async () => {
    const res = await browser().request("/favicon.svg");
    expect(res.status).toBe(200);
    expect(res.headers.get("cache-control")).toBe("public, max-age=3600");
  });

  test("nothing outside the build is served", async () => {
    const b = await signedIn();
    for (const path of ["/assets/..%2f..%2fsecret.txt", "/..%2fsecret.txt", "/assets/%2e%2e/%2e%2e/secret.txt"]) {
      expect(await (await b.request(path)).text()).not.toContain("outside the root");
    }
  });

  test("server prefixes never fall back to the app", async () => {
    const b = await signedIn();
    for (const path of ["/auth/nope", "/ui/nope"]) {
      expect((await b.request(path)).status).toBe(404);
    }
  });
});

describe("security headers", () => {
  test("every response carries the 'self' CSP, nosniff and Referrer-Policy", async () => {
    const b = browser();
    for (const path of ["/", "/status", "/assets/app-3f2a1b.js", "/healthz", "/ui/config", "/api/v1/status", "/auth/login"]) {
      const res = await b.request(path);
      expect(res.headers.get("content-security-policy")).toBe(contentSecurityPolicy);
      expect(res.headers.get("x-content-type-options")).toBe("nosniff");
      expect(res.headers.get("referrer-policy")).toBe("same-origin");
    }
  });

  test("the CSP allows only the page's own origin and no framing", () => {
    const directives = new Map(contentSecurityPolicy.split(";").map((d) => d.trim().split(/\s+/) as [string, ...string[]]).map(([k, ...v]) => [k, v]));
    expect(directives.get("default-src")).toEqual(["'self'"]);
    expect(directives.get("script-src")).toEqual(["'self'"]);
    expect(directives.get("connect-src")).toEqual(["'self'"]);
    expect(directives.get("frame-ancestors")).toEqual(["'none'"]);
    expect(contentSecurityPolicy).not.toContain("unsafe");
    expect(contentSecurityPolicy).not.toContain("data:");
  });
});
