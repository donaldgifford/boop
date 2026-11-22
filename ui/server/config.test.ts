import { describe, expect, test } from "bun:test";

import { ConfigError, loadConfig, parseDuration, publicConfig } from "./config";
import { testKey as key, validEnv } from "./testing/env";

function problems(env: Record<string, string | undefined>): string[] {
  try {
    loadConfig(env);
  } catch (e) {
    if (e instanceof ConfigError) {
      return e.problems;
    }
    throw e;
  }
  return [];
}

describe("loadConfig", () => {
  test("a complete environment, with the defaults", () => {
    const cfg = loadConfig(validEnv);
    expect(cfg.oidc.scopes).toBe("openid profile email offline_access");
    expect(cfg.oidc.tokenAuthMethod).toBe("client_secret_post");
    expect(cfg.oidc.authorizationParams).toEqual({});
    expect(cfg.sessionTtlSeconds).toBe(8 * 3600);
    expect(cfg.port).toBe(3000);
    expect(cfg.sessionKeys).toHaveLength(1);
    expect(cfg.sessionKeys[0]).toHaveLength(32);
    expect(cfg.links).toEqual([]);
  });

  test("every missing variable is reported at once", () => {
    const got = problems({});
    for (const name of ["OIDC_ISSUER", "OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET", "API_UPSTREAM", "PUBLIC_URL", "UI_SESSION_KEYS"]) {
      expect(got.some((p) => p.startsWith(name))).toBe(true);
    }
  });

  test("client_secret_basic is accepted, any other method refused", () => {
    expect(loadConfig({ ...validEnv, OIDC_TOKEN_AUTH_METHOD: "client_secret_basic" }).oidc.tokenAuthMethod).toBe("client_secret_basic");
    expect(problems({ ...validEnv, OIDC_TOKEN_AUTH_METHOD: "private_key_jwt" })).toEqual([
      'OIDC_TOKEN_AUTH_METHOD must be one of client_secret_post, client_secret_basic, got "private_key_jwt"',
    ]);
  });

  test("authorization params are a JSON object of strings", () => {
    expect(loadConfig({ ...validEnv, OIDC_AUTHORIZATION_PARAMS: '{"audience":"boopd-api","prompt":"login"}' }).oidc.authorizationParams).toEqual({
      audience: "boopd-api",
      prompt: "login",
    });
    for (const raw of ["not json", "[]", '"audience"', "null"]) {
      expect(problems({ ...validEnv, OIDC_AUTHORIZATION_PARAMS: raw })).toEqual(["OIDC_AUTHORIZATION_PARAMS must be a JSON object of string values"]);
    }
    expect(problems({ ...validEnv, OIDC_AUTHORIZATION_PARAMS: '{"audience":1,"max_age":true}' })).toEqual([
      "OIDC_AUTHORIZATION_PARAMS values must be strings: audience, max_age",
    ]);
  });

  test("every reserved authorization param is refused, listed together", () => {
    const reserved = ["redirect_uri", "scope", "state", "nonce", "code_challenge", "code_challenge_method", "response_type", "client_id"];
    const raw = JSON.stringify(Object.fromEntries(reserved.map((k) => [k, "x"])));
    expect(problems({ ...validEnv, OIDC_AUTHORIZATION_PARAMS: raw })).toEqual([
      `OIDC_AUTHORIZATION_PARAMS may not set ${reserved.join(", ")}: the login flow sets them`,
    ]);
  });

  test("a short session key is refused by position", () => {
    expect(problems({ ...validEnv, UI_SESSION_KEYS: `${key},short` })).toEqual(["UI_SESSION_KEYS entry 2 is shorter than 32 bytes"]);
  });

  test("keys derive distinctly, in order", () => {
    const cfg = loadConfig({ ...validEnv, UI_SESSION_KEYS: `${key}, ${"j".repeat(40)}` });
    expect(cfg.sessionKeys).toHaveLength(2);
    expect(cfg.sessionKeys[0]).not.toEqual(cfg.sessionKeys[1]);
  });

  test("PUBLIC_URL is an https origin, http on localhost only", () => {
    expect(problems({ ...validEnv, PUBLIC_URL: "http://boop.example.com" })).toHaveLength(1);
    expect(problems({ ...validEnv, PUBLIC_URL: "http://localhost:5173" })).toEqual([]);
    expect(problems({ ...validEnv, PUBLIC_URL: "https://example.com/ui" })).toHaveLength(1);
  });

  test("the issuer is https, http on loopback only", () => {
    expect(problems({ ...validEnv, OIDC_ISSUER: "http://idp.example.com/realms/boop" })).toHaveLength(1);
    expect(problems({ ...validEnv, OIDC_ISSUER: "http://127.0.0.1:8080/realms/boop" })).toEqual([]);
  });

  test("malformed URLs, durations, scopes and ports are refused", () => {
    expect(problems({ ...validEnv, API_UPSTREAM: "not a url" })).toHaveLength(1);
    expect(problems({ ...validEnv, UI_SESSION_TTL: "8 hours" })).toHaveLength(1);
    expect(problems({ ...validEnv, OIDC_SCOPES: "profile email" })).toHaveLength(1);
    expect(problems({ ...validEnv, PORT: "70000" })).toHaveLength(1);
  });

  test("UI_LINKS is an optional array of https links", () => {
    const links = '[{"label":"Grafana","url":"https://grafana.example.com/d/boop"},{"label":"Temporal","url":"https://temporal.example.com"}]';
    expect(loadConfig({ ...validEnv, UI_LINKS: links }).links).toEqual([
      { label: "Grafana", url: "https://grafana.example.com/d/boop" },
      { label: "Temporal", url: "https://temporal.example.com/" },
    ]);
    expect(loadConfig({ ...validEnv, UI_LINKS: "" }).links).toEqual([]);
    expect(problems({ ...validEnv, UI_LINKS: '{"label":"x"}' })).toEqual(['UI_LINKS must be a JSON array of {"label", "url"} objects']);
    expect(problems({ ...validEnv, UI_LINKS: '[{"label":"x","url":"http://x.example.com"}]' })).toEqual([
      'UI_LINKS entry 1 url must be https, got "http://x.example.com"',
    ]);
    expect(problems({ ...validEnv, UI_LINKS: '[{"label":"x","url":"javascript:alert(1)"}]' })).toHaveLength(1);
    expect(problems({ ...validEnv, UI_LINKS: '[{"url":"https://x.example.com"}]' })).toEqual(['UI_LINKS entry 1 needs a non-empty "label" and a "url"']);
  });
});

describe("parseDuration", () => {
  test.each([
    ["8h", 28800],
    ["90m", 5400],
    ["1h30m", 5400],
    ["45s", 45],
  ])("%s", (s, want) => {
    expect(parseDuration(s)).toBe(want);
  });

  test.each(["", "8", "h", "8d", "1h 30m", "x8h"])("refuses %p", (s) => {
    expect(parseDuration(s)).toBeUndefined();
  });
});

describe("publicConfig", () => {
  test("holds no secret, key, client or upstream", () => {
    const out = JSON.stringify(publicConfig(loadConfig(validEnv)));
    for (const secret of ["client-secret", key, "boopd-api", "boopd-ui"]) {
      expect(out).not.toContain(secret);
    }
  });

  test("carries the links", () => {
    const cfg = loadConfig({ ...validEnv, UI_LINKS: '[{"label":"Docs","url":"https://docs.example.com/boop/"}]' });
    expect(publicConfig(cfg).links).toEqual([{ label: "Docs", url: "https://docs.example.com/boop/" }]);
  });
});
