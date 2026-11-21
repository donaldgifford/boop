import type { Hono } from "hono";

// Browser drives a Hono app as a browser would: it keeps cookies, drops
// the ones set with Max-Age=0, and sends the rest on each request.
export class Browser {
  readonly jar = new Map<string, string>();

  constructor(
    private readonly app: Hono,
    private readonly origin = "https://boop.example.com",
  ) {}

  async request(path: string, init: RequestInit = {}): Promise<Response> {
    const headers = new Headers(init.headers);
    if (this.jar.size > 0) {
      headers.set("cookie", [...this.jar].map(([k, v]) => `${k}=${v}`).join("; "));
    }
    const res = await this.app.request(new URL(path, this.origin).toString(), { ...init, headers });
    for (const set of res.headers.getSetCookie()) {
      const [pair = ""] = set.split(";");
      const eq = pair.indexOf("=");
      const name = pair.slice(0, eq);
      if (/Max-Age=0\b/i.test(set)) {
        this.jar.delete(name);
      } else {
        this.jar.set(name, pair.slice(eq + 1));
      }
    }
    return res;
  }

  cookie(name: string): string | undefined {
    return this.jar.get(name);
  }
}
