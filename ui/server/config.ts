import { hkdfSync } from "node:crypto";

// Config is the BFF's environment, validated once at start (DESIGN-0004
// § The UI). The chart sets every variable.
export interface Config {
  port: number;
  oidc: {
    issuer: URL;
    clientId: string;
    clientSecret: string;
    scopes: string;
    // tokenAuthMethod is how the secret reaches the token endpoint
    // (RFC 6749 § 2.3.1); issuers differ in which they accept.
    tokenAuthMethod: TokenAuthMethod;
    // authorizationParams are extra authorize parameters an issuer needs
    // (an audience, say). The flow's own parameters always win.
    authorizationParams: Record<string, string>;
  };
  // apiUpstream is the boopd api Service. The proxy forwards nowhere else.
  apiUpstream: URL;
  // sessionKeys are HKDF-derived A256GCM keys, newest first: the first
  // seals, any opens, so a key rotates without signing anyone out.
  sessionKeys: Uint8Array[];
  sessionTtlSeconds: number;
  publicUrl: URL;
  // links are the optional external links shown in the sidebar (INV-0005
  // OQ4). The UI knows nothing about what they point to.
  links: Link[];
}

export interface Link {
  label: string;
  url: string;
}

export type TokenAuthMethod = "client_secret_post" | "client_secret_basic";

const tokenAuthMethods: readonly TokenAuthMethod[] = ["client_secret_post", "client_secret_basic"];

// reservedAuthorizationParams belong to the login flow; OIDC_AUTHORIZATION_PARAMS
// may not set them.
export const reservedAuthorizationParams: readonly string[] = [
  "redirect_uri",
  "scope",
  "state",
  "nonce",
  "code_challenge",
  "code_challenge_method",
  "response_type",
  "client_id",
];

// ConfigError carries every problem at once, so a bad deployment fails
// its first start with the whole list.
export class ConfigError extends Error {
  constructor(readonly problems: string[]) {
    super(`invalid configuration:\n  ${problems.join("\n  ")}`);
    this.name = "ConfigError";
  }
}

const minKeyBytes = 32;
const maxLinks = 10;
const defaultScopes = "openid profile email offline_access";
const defaultTtl = "8h";

// loadConfig validates env, throwing a ConfigError that lists every
// missing or malformed variable.
export function loadConfig(env: Record<string, string | undefined>): Config {
  const problems: string[] = [];

  const required = (name: string): string => {
    const v = env[name]?.trim();
    if (!v) {
      problems.push(`${name} is required`);
    }
    return v ?? "";
  };

  const url = (name: string): URL | undefined => {
    const raw = required(name);
    if (!raw) {
      return undefined;
    }
    try {
      return new URL(raw);
    } catch {
      problems.push(`${name} must be an absolute URL, got ${JSON.stringify(raw)}`);
      return undefined;
    }
  };

  const issuer = url("OIDC_ISSUER");
  if (issuer && issuer.protocol !== "https:" && !isLoopback(issuer)) {
    problems.push("OIDC_ISSUER must be https (http is allowed on localhost only)");
  }
  const clientId = required("OIDC_CLIENT_ID");
  const clientSecret = required("OIDC_CLIENT_SECRET");
  const apiUpstream = url("API_UPSTREAM");
  const publicUrl = url("PUBLIC_URL");
  if (publicUrl && (publicUrl.pathname !== "/" || publicUrl.search || publicUrl.hash)) {
    problems.push("PUBLIC_URL must be an origin with no path, query or fragment");
  }
  if (publicUrl && publicUrl.protocol !== "https:" && !isLoopback(publicUrl)) {
    problems.push("PUBLIC_URL must be https: the session cookie is Secure (http is allowed on localhost only)");
  }

  const scopes = env.OIDC_SCOPES?.trim() || defaultScopes;
  if (!scopes.split(/\s+/).includes("openid")) {
    problems.push("OIDC_SCOPES must include openid");
  }

  const tokenAuthMethod = parseTokenAuthMethod(env.OIDC_TOKEN_AUTH_METHOD, problems);
  const authorizationParams = parseAuthorizationParams(env.OIDC_AUTHORIZATION_PARAMS, problems);

  const keys = (env.UI_SESSION_KEYS ?? "")
    .split(",")
    .map((k) => k.trim())
    .filter(Boolean);
  if (keys.length === 0) {
    problems.push("UI_SESSION_KEYS is required: a comma-separated list, newest first");
  }
  keys.forEach((k, i) => {
    if (new TextEncoder().encode(k).length < minKeyBytes) {
      problems.push(`UI_SESSION_KEYS entry ${i + 1} is shorter than ${minKeyBytes} bytes`);
    }
  });

  const ttl = parseDuration(env.UI_SESSION_TTL?.trim() || defaultTtl);
  if (ttl === undefined || ttl <= 0) {
    problems.push(`UI_SESSION_TTL must be a positive duration like 8h or 90m, got ${JSON.stringify(env.UI_SESSION_TTL)}`);
  }

  const port = Number(env.PORT ?? 3000);
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    problems.push(`PORT must be a TCP port, got ${JSON.stringify(env.PORT)}`);
  }

  const links = parseLinks(env.UI_LINKS, problems);

  if (problems.length > 0 || !issuer || !apiUpstream || !publicUrl) {
    throw new ConfigError(problems);
  }
  return {
    port,
    oidc: { issuer, clientId, clientSecret, scopes, tokenAuthMethod, authorizationParams },
    apiUpstream,
    sessionKeys: keys.map(deriveKey),
    sessionTtlSeconds: ttl ?? 0,
    publicUrl,
    links,
  };
}

function parseTokenAuthMethod(raw: string | undefined, problems: string[]): TokenAuthMethod {
  const v = raw?.trim();
  if (!v) {
    return "client_secret_post";
  }
  const m = tokenAuthMethods.find((t) => t === v);
  if (!m) {
    problems.push(`OIDC_TOKEN_AUTH_METHOD must be one of ${tokenAuthMethods.join(", ")}, got ${JSON.stringify(raw)}`);
    return "client_secret_post";
  }
  return m;
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function parseJSON(raw: string): unknown {
  try {
    return JSON.parse(raw) as unknown;
  } catch {
    return undefined;
  }
}

// parseAuthorizationParams reads a JSON object of strings, reporting
// every reserved and non-string key.
function parseAuthorizationParams(raw: string | undefined, problems: string[]): Record<string, string> {
  const v = raw?.trim();
  if (!v) {
    return {};
  }
  const parsed = parseJSON(v);
  if (!isRecord(parsed)) {
    problems.push("OIDC_AUTHORIZATION_PARAMS must be a JSON object of string values");
    return {};
  }
  const entries = Object.entries(parsed);
  const reserved = entries.map(([k]) => k).filter((k) => reservedAuthorizationParams.includes(k));
  if (reserved.length > 0) {
    problems.push(`OIDC_AUTHORIZATION_PARAMS may not set ${reserved.join(", ")}: the login flow sets them`);
  }
  const nonString = entries.filter(([, val]) => typeof val !== "string").map(([k]) => k);
  if (nonString.length > 0) {
    problems.push(`OIDC_AUTHORIZATION_PARAMS values must be strings: ${nonString.join(", ")}`);
  }
  const out: Record<string, string> = {};
  for (const [k, val] of entries) {
    if (typeof val === "string" && !reservedAuthorizationParams.includes(k)) {
      out[k] = val;
    }
  }
  return out;
}

// parseLinks reads UI_LINKS, a JSON array of {label, url}. Each URL must
// be https, since the browser follows it from the sidebar.
function parseLinks(raw: string | undefined, problems: string[]): Link[] {
  const v = raw?.trim();
  if (!v) {
    return [];
  }
  const parsed = parseJSON(v);
  if (!Array.isArray(parsed)) {
    problems.push('UI_LINKS must be a JSON array of {"label", "url"} objects');
    return [];
  }
  if (parsed.length > maxLinks) {
    problems.push(`UI_LINKS has ${parsed.length} entries, over the limit of ${maxLinks}`);
  }
  const links: Link[] = [];
  parsed.forEach((item: unknown, i) => {
    const n = i + 1;
    if (!isRecord(item) || typeof item.label !== "string" || !item.label.trim() || typeof item.url !== "string") {
      problems.push(`UI_LINKS entry ${n} needs a non-empty "label" and a "url"`);
      return;
    }
    let u: URL;
    try {
      u = new URL(item.url);
    } catch {
      problems.push(`UI_LINKS entry ${n} url must be an absolute https URL, got ${JSON.stringify(item.url)}`);
      return;
    }
    if (u.protocol !== "https:") {
      problems.push(`UI_LINKS entry ${n} url must be https, got ${JSON.stringify(item.url)}`);
      return;
    }
    links.push({ label: item.label.trim(), url: u.href });
  });
  return links;
}

// deriveKey stretches an operator secret (any string of 32 bytes or
// more) into a 32-byte A256GCM key.
function deriveKey(secret: string): Uint8Array {
  return new Uint8Array(hkdfSync("sha256", secret, "", "boopd-ui session v1", 32));
}

export function isLoopback(u: URL): boolean {
  return u.hostname === "localhost" || u.hostname === "127.0.0.1" || u.hostname === "[::1]";
}

const unitSeconds: Record<string, number> = { s: 1, m: 60, h: 3600 };

// parseDuration reads a Go-style duration of h, m and s parts ("8h",
// "1h30m") into seconds; undefined when it does not parse.
export function parseDuration(s: string): number | undefined {
  let total = 0;
  let consumed = 0;
  for (const m of s.matchAll(/(\d+)([smh])/g)) {
    if (m.index !== consumed) {
      return undefined;
    }
    total += Number(m[1]) * (unitSeconds[m[2] ?? ""] ?? 0);
    consumed += m[0].length;
  }
  return consumed === s.length && consumed > 0 ? total : undefined;
}

// PublicConfig is /ui/config: display values for the browser, never a
// secret, key or upstream address.
export interface PublicConfig {
  issuer: string;
  public_url: string;
  session_ttl_seconds: number;
  links: Link[];
}

export function publicConfig(cfg: Config): PublicConfig {
  return {
    issuer: cfg.oidc.issuer.origin,
    public_url: cfg.publicUrl.origin,
    session_ttl_seconds: cfg.sessionTtlSeconds,
    links: cfg.links,
  };
}
