import { resolve, sep } from "node:path";

import type { Hono, MiddlewareHandler } from "hono";

import type { SealedCookie, Session } from "./session";

// contentSecurityPolicy: the SPA loads from and talks to its own origin
// only, and is never framed (DESIGN-0004 § The UI).
export const contentSecurityPolicy = "default-src 'self'; connect-src 'self'; frame-ancestors 'none'; script-src 'self'";

// securityHeaders sets the headers on every response, the proxy's too.
export const securityHeaders: MiddlewareHandler = async (c, next) => {
  await next();
  c.header("content-security-policy", contentSecurityPolicy);
  c.header("x-content-type-options", "nosniff");
  // same-origin rather than no-referrer: under no-referrer Chrome sends
  // "Origin: null" on a same-origin form POST, and the sign-out and
  // action Origin checks would refuse it.
  c.header("referrer-policy", "same-origin");
};

// publicAppPaths render without a session: the status page.
const publicAppPaths = new Set(["/status"]);

// serverPrefixes belong to server routes; the SPA fallback never answers
// under them, so a typo there is a 404, not the app.
const serverPrefixes = ["/api/", "/auth/", "/ui/", "/assets/"];

export interface StaticDeps {
  // dir is the Vite build (ui/dist).
  dir: string;
  session: SealedCookie<Session>;
}

// mountStatic serves the SPA build. Hashed assets are cached for a year
// and index.html never, so a deploy shows on the next load. Any other GET
// gets the app, after a sign-in redirect except on the public paths.
// Mount it last: it is the catch-all.
export function mountStatic(app: Hono, { dir, session }: StaticDeps): void {
  const root = resolve(dir);

  app.get("/assets/*", async (c) => {
    const file = await within(root, c.req.path);
    if (!file) {
      return c.notFound();
    }
    c.header("cache-control", "public, max-age=31536000, immutable");
    return c.body(file.stream(), 200, { "content-type": file.type });
  });

  app.get("*", async (c) => {
    const path = c.req.path;
    if (serverPrefixes.some((p) => path.startsWith(p))) {
      return c.notFound();
    }
    // Root files of the build (favicon) are served as they are, cached
    // briefly since their names carry no hash.
    if (path !== "/" && path !== "/index.html") {
      const file = await within(root, path);
      if (file) {
        c.header("cache-control", "public, max-age=3600");
        return c.body(file.stream(), 200, { "content-type": file.type });
      }
    }
    if (!publicAppPaths.has(path) && !(await session.read(c))) {
      const url = new URL(c.req.url);
      return c.redirect(`/auth/login?return_to=${encodeURIComponent(url.pathname + url.search)}`, 302);
    }
    const index = Bun.file(resolve(root, "index.html"));
    if (!(await index.exists())) {
      return c.text("the UI build is missing", 503);
    }
    c.header("cache-control", "no-cache");
    return c.body(index.stream(), 200, { "content-type": "text/html; charset=utf-8" });
  });
}

// within is the file at urlPath under root; undefined when it is
// missing, malformed or resolves outside root.
async function within(root: string, urlPath: string): Promise<ReturnType<typeof Bun.file> | undefined> {
  let decoded: string;
  try {
    decoded = decodeURIComponent(urlPath);
  } catch {
    return undefined;
  }
  if (decoded.includes("\0")) {
    return undefined;
  }
  const full = resolve(root, `.${decoded}`);
  if (!full.startsWith(root + sep)) {
    return undefined;
  }
  const file = Bun.file(full);
  return (await file.exists()) ? file : undefined;
}
