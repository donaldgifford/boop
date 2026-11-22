// Fails when the built SPA would break under the BFF's CSP
// (`default-src 'self'`, no data: source, no font host): an inlined asset
// or a Google Fonts URL in ui/dist means a page with missing fonts or
// icons. It builds first, then scans every CSS and JS file produced.
import { readdir, readFile } from "node:fs/promises";
import { join } from "node:path";

import { $ } from "bun";

export interface Offence {
  file: string;
  pattern: string;
}

// A data: URL with a media type. The bare prefix would flag prose and
// object keys.
const dataURL = /data:[a-z]+\/[a-z0-9.+-]+[;,]/i;
const googleFonts = /fonts\.googleapis\.com|fonts\.gstatic\.com/;
const scanned = /\.(css|js|mjs)$/;

export function offences(file: string, content: string): Offence[] {
  const found: Offence[] = [];
  if (dataURL.test(content)) found.push({ file, pattern: "data: URL" });
  if (googleFonts.test(content)) found.push({ file, pattern: "Google Fonts URL" });
  return found;
}

// scanDist is every offence in dir's CSS and JS files.
export async function scanDist(dir: string): Promise<Offence[]> {
  const entries = await readdir(dir, { recursive: true, withFileTypes: true });
  const files = entries
    .filter((e) => e.isFile() && scanned.test(e.name))
    .map((e) => join(e.parentPath, e.name))
    .sort();
  return (await Promise.all(files.map(async (f) => offences(f, await readFile(f, "utf8"))))).flat();
}

if (import.meta.main) {
  await $`bun run build`.quiet();
  const found = await scanDist("dist");
  for (const o of found) {
    console.error(`${o.file}: holds a ${o.pattern}, which the CSP blocks`);
  }
  if (found.length > 0) process.exit(1);
  console.log("dist/ is CSP-clean");
}
