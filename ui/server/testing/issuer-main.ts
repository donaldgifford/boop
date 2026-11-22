// The mock issuer as a process, for the e2e cluster (IMPL-0005 14.1): the
// API verifies its tokens, the BFF signs in through it, and the
// Playwright suite picks a user with its mock_user cookie. Never part of
// a release; test/stub-oidc builds it into its own image.
//
//	ISSUER_PUBLIC_URL     the issuer URL clients use (required)
//	ISSUER_PORT           listen port (9443)
//	ISSUER_TLS_CERT/_KEY  PEM files; https when set
//	ISSUER_CLIENT_ID      the UI's client (boopd-ui)
//	ISSUER_CLIENT_SECRET  its secret (client-secret)
//	ISSUER_API_AUDIENCE   the API's audience; access tokens become JWTs for it
//	ISSUER_USERS          JSON {name: {sub, name, groups}}; the cookie picks one
//	ISSUER_GROUPS         the default user's groups, comma-separated
import { readFileSync } from "node:fs";

import { type MockUser, startMockIssuer } from "./mock-issuer";

const env = (k: string) => process.env[k] ?? "";
const publicURL = env("ISSUER_PUBLIC_URL");
if (!publicURL) {
  console.error("ISSUER_PUBLIC_URL is required");
  process.exit(2);
}
const cert = env("ISSUER_TLS_CERT");
const key = env("ISSUER_TLS_KEY");

const issuer = await startMockIssuer({
  port: Number(env("ISSUER_PORT") || 9443),
  hostname: "0.0.0.0",
  publicURL,
  clientId: env("ISSUER_CLIENT_ID") || "boopd-ui",
  clientSecret: env("ISSUER_CLIENT_SECRET") || "client-secret",
  ...(env("ISSUER_API_AUDIENCE") ? { apiAudience: env("ISSUER_API_AUDIENCE") } : {}),
  ...(env("ISSUER_GROUPS") ? { groups: env("ISSUER_GROUPS").split(",") } : {}),
  ...(env("ISSUER_USERS") ? { users: JSON.parse(env("ISSUER_USERS")) as Record<string, MockUser> } : {}),
  ...(cert && key ? { tls: { cert: readFileSync(cert, "utf8"), key: readFileSync(key, "utf8") } } : {}),
});
console.log(JSON.stringify({ msg: "mock issuer listening", issuer: issuer.url.toString() }));
