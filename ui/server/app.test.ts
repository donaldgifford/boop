import { describe, expect, test } from "bun:test";

import { createApp } from "./app";
import { loadConfig } from "./config";
import { discardLogger } from "./log";
import { validEnv } from "./testing/env";

const app = (env: Record<string, string> = {}) => createApp({ config: loadConfig({ ...validEnv, ...env }), log: discardLogger });

describe("app", () => {
  test("/healthz", async () => {
    const res = await app().request("/healthz");
    expect(res.status).toBe(200);
    expect(await res.text()).toBe("ok");
  });

  test("/ui/config is display-safe and carries the links", async () => {
    const res = await app({ UI_LINKS: '[{"label":"Grafana","url":"https://grafana.example.com/d/boop"}]' }).request("/ui/config");
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual({
      issuer: "https://idp.example.com",
      public_url: "https://boop.example.com",
      session_ttl_seconds: 28800,
      links: [{ label: "Grafana", url: "https://grafana.example.com/d/boop" }],
    });
  });
});
