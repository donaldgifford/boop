import * as client from "openid-client";

import { type Config, isLoopback } from "./config";

// Oidc is the issuer's discovered configuration, loaded lazily. A failed
// discovery is forgotten so the next caller retries, and ready() stays
// false until one succeeds (readiness depends on it).
export class Oidc {
  private pending: Promise<client.Configuration> | undefined;
  private loaded: client.Configuration | undefined;

  constructor(private readonly config: Config) {}

  configuration(): Promise<client.Configuration> {
    if (this.loaded) {
      return Promise.resolve(this.loaded);
    }
    this.pending ??= this.discover().then(
      (c) => {
        this.loaded = c;
        return c;
      },
      (err: unknown) => {
        this.pending = undefined;
        throw err;
      },
    );
    return this.pending;
  }

  ready(): boolean {
    return this.loaded !== undefined;
  }

  private discover(): Promise<client.Configuration> {
    const { issuer, clientId, clientSecret, tokenAuthMethod } = this.config.oidc;
    // Config refuses plain http except on loopback (development, tests).
    const execute = issuer.protocol === "http:" && isLoopback(issuer) ? [client.allowInsecureRequests] : [];
    const auth =
      tokenAuthMethod === "client_secret_basic" ? client.ClientSecretBasic(clientSecret) : client.ClientSecretPost(clientSecret);
    return client.discovery(issuer, clientId, clientSecret, auth, { execute });
  }
}
