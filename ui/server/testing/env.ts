// Fixtures shared by the BFF's tests.

export const testKey = "k".repeat(32);

export const validEnv = {
  OIDC_ISSUER: "https://idp.example.com/realms/boop",
  OIDC_CLIENT_ID: "boopd-ui",
  OIDC_CLIENT_SECRET: "client-secret",
  API_UPSTREAM: "http://boopd-api:80",
  PUBLIC_URL: "https://boop.example.com",
  UI_SESSION_KEYS: testKey,
};
