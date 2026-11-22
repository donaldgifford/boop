import type { Context } from "hono";
import { deleteCookie, getCookie, setCookie } from "hono/cookie";
import { EncryptJWT, type JWTPayload, jwtDecrypt } from "jose";

// Session is a signed-in user. It lives only in the sealed cookie; the
// BFF keeps no server-side store.
export interface Session {
  sub: string;
  name: string;
  accessToken: string;
  // accessTokenExpiresAt is epoch seconds.
  accessTokenExpiresAt: number;
  refreshToken?: string;
  idToken?: string;
  // expiresAt is the session's absolute end (epoch seconds). A token
  // refresh never moves it, so UI_SESSION_TTL bounds every session.
  expiresAt: number;
}

// maxChunkBytes keeps each cookie under the browser's ~4 KB limit with
// room for the name and attributes.
export const maxChunkBytes = 3800;
const maxChunks = 10;

const jweHeader = { alg: "dir", enc: "A256GCM" } as const;

// SealedCookie keeps one encrypted value (a JWE, dir + A256GCM) in a
// __Host- cookie, split into numbered chunks when it outgrows one.
export class SealedCookie<T extends object> {
  // name excludes the __Host- prefix, which hono adds.
  constructor(
    private readonly name: string,
    private readonly keys: Uint8Array[],
  ) {
    if (keys.length === 0) {
      throw new Error("SealedCookie needs at least one key");
    }
  }

  // seal encrypts value under the newest key, expiring at expiresAt.
  async seal(value: T, expiresAt: number): Promise<string> {
    return new EncryptJWT({ v: value } as unknown as JWTPayload)
      .setProtectedHeader(jweHeader)
      .setIssuedAt()
      .setExpirationTime(expiresAt)
      .encrypt(this.keys[0] as Uint8Array);
  }

  // open tries every key; undefined when none opens the token, or it was
  // tampered with or has expired.
  async open(token: string): Promise<T | undefined> {
    for (const key of this.keys) {
      try {
        const { payload } = await jwtDecrypt(token, key, {
          keyManagementAlgorithms: [jweHeader.alg],
          contentEncryptionAlgorithms: [jweHeader.enc],
        });
        return (payload as { v?: T }).v;
      } catch {
        // The next key, if any.
      }
    }
    return undefined;
  }

  async write(c: Context, value: T, expiresAt: number): Promise<void> {
    const token = await this.seal(value, expiresAt);
    const maxAge = Math.max(0, expiresAt - Math.floor(Date.now() / 1000));
    const had = this.chunkCount(c);

    if (token.length <= maxChunkBytes) {
      setCookie(c, this.name, token, attrs(maxAge));
      this.dropChunks(c, 0, had);
      return;
    }

    const parts = Math.ceil(token.length / maxChunkBytes);
    if (parts > maxChunks) {
      throw new Error(`${this.name}: the sealed value needs ${parts} cookies, over the limit of ${maxChunks}`);
    }
    for (let i = 0; i < parts; i++) {
      setCookie(c, `${this.name}.${i}`, token.slice(i * maxChunkBytes, (i + 1) * maxChunkBytes), attrs(maxAge));
    }
    this.dropChunks(c, parts, had);
    if (getCookie(c, this.name, "host") !== undefined) {
      deleteCookie(c, this.name, attrs(0));
    }
  }

  async read(c: Context): Promise<T | undefined> {
    const whole = getCookie(c, this.name, "host");
    if (whole !== undefined) {
      return this.open(whole);
    }
    const n = this.chunkCount(c);
    if (n === 0) {
      return undefined;
    }
    let token = "";
    for (let i = 0; i < n; i++) {
      token += getCookie(c, `${this.name}.${i}`, "host") ?? "";
    }
    return this.open(token);
  }

  clear(c: Context): void {
    if (getCookie(c, this.name, "host") !== undefined) {
      deleteCookie(c, this.name, attrs(0));
    }
    this.dropChunks(c, 0, this.chunkCount(c));
  }

  private chunkCount(c: Context): number {
    let n = 0;
    while (n < maxChunks && getCookie(c, `${this.name}.${n}`, "host") !== undefined) {
      n++;
    }
    return n;
  }

  private dropChunks(c: Context, from: number, to: number): void {
    for (let i = from; i < to; i++) {
      deleteCookie(c, `${this.name}.${i}`, attrs(0));
    }
  }
}

// attrs are a __Host- cookie's: HttpOnly, Secure, SameSite=Lax, Path=/,
// no Domain.
function attrs(maxAge: number) {
  return { prefix: "host", httpOnly: true, secure: true, sameSite: "Lax", path: "/", maxAge } as const;
}

// sessionCookie is the signed-in session, __Host-boop_session.
export function sessionCookie(keys: Uint8Array[]): SealedCookie<Session> {
  return new SealedCookie<Session>("boop_session", keys);
}

// newSessionExpiry is the absolute end of a session started now.
export function newSessionExpiry(ttlSeconds: number, now = Date.now()): number {
  return Math.floor(now / 1000) + ttlSeconds;
}
