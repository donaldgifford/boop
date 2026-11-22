// Fails when web/src paints with a colour that is not one of the design
// system's tokens (DESIGN-0004 § Visual design: the app defines no colour
// token). Tailwind compiles a palette or arbitrary colour without
// complaint, so a stray one would quietly drift from the package's
// themes; this catches it before review.
//
// Flagged in .ts, .tsx and .css under web/src:
//   - a Tailwind palette colour (bg-red-500, text-white, ring-slate-200/50);
//   - an arbitrary colour value (bg-[#0af], text-[rgb(0,0,0)]);
//   - a colour literal: a hex in CSS, or rgb()/hsl()/oklch()/lab()/lch()/
//     color-mix() anywhere;
//   - a colour token defined here (--color-x: ...).
import { readdir, readFile } from "node:fs/promises";
import { join } from "node:path";

export interface Offence {
  file: string;
  line: number;
  found: string;
}

const utility = "(?:bg|text|border(?:-[trblxy])?|fill|stroke|ring|ring-offset|divide|outline|decoration|from|via|to|shadow|caret|accent|placeholder)";
const palette = "(?:slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose)-(?:50|[1-9]00|950)";

const rules: RegExp[] = [
  new RegExp(`(?<![\\w-])${utility}-(?:${palette}|white|black)(?![\\w-])`, "g"),
  new RegExp(`(?<![\\w-])${utility}-\\[(?:#|rgb|hsl|oklch|oklab|lab|lch|color)[^\\]]*\\]`, "g"),
  /(?<!\[)\b(?:rgba?|hsla?|oklch|oklab|lab|lch|color-mix)\(/g,
  /--color-[\w-]+\s*:/g,
];
// Hex colours only in CSS, where "#" cannot be a pull request number.
const cssHex = /(?<![\w&])#(?:[0-9a-f]{8}|[0-9a-f]{6}|[0-9a-f]{3,4})(?![\w-])/gi;

const scanned = /\.(ts|tsx|css)$/;

export function offences(file: string, content: string): Offence[] {
  const found: Offence[] = [];
  const checks = file.endsWith(".css") ? [...rules, cssHex] : rules;
  content.split("\n").forEach((text, i) => {
    for (const re of checks) {
      for (const m of text.matchAll(re)) found.push({ file, line: i + 1, found: m[0] });
    }
  });
  return found;
}

// scanTree is every offence in dir's TS, TSX and CSS files. Generated
// API types are skipped.
export async function scanTree(dir: string): Promise<Offence[]> {
  const entries = await readdir(dir, { recursive: true, withFileTypes: true });
  const files = entries
    .filter((e) => e.isFile() && scanned.test(e.name) && !e.name.endsWith(".gen.ts"))
    .map((e) => join(e.parentPath, e.name))
    .sort();
  return (await Promise.all(files.map(async (f) => offences(f, await readFile(f, "utf8"))))).flat();
}

if (import.meta.main) {
  const found = await scanTree("web/src");
  for (const o of found) {
    console.error(`${o.file}:${o.line}: ${o.found} is not a design-system colour token`);
  }
  if (found.length > 0) process.exit(1);
  console.log("web/src paints only with design-system tokens");
}
