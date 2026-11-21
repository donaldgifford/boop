// safeReturnPath is the open-redirect guard for the post-login return:
// only a same-origin path survives. Absolute URLs, protocol-relative
// "//host", backslashes and paths under /auth/ all become "/".
export function safeReturnPath(raw: string | undefined, origin: URL): string {
  if (!raw?.startsWith("/") || raw.startsWith("//") || raw.includes("\\")) {
    return "/";
  }
  let u: URL;
  try {
    u = new URL(raw, origin);
  } catch {
    return "/";
  }
  if (u.origin !== origin.origin || u.pathname.startsWith("/auth/")) {
    return "/";
  }
  return `${u.pathname}${u.search}${u.hash}`;
}
