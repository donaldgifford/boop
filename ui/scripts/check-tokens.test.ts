import { expect, test } from "bun:test";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { offences, scanTree } from "./check-tokens";

test.each([
  ["a palette background", 'className="bg-red-500 p-2"', "bg-red-500"],
  ["a palette text with a variant and opacity", 'className="hover:text-slate-200/50"', "text-slate-200"],
  ["white", 'className="text-white"', "text-white"],
  ["a side border", 'className="border-l-emerald-950"', "border-l-emerald-950"],
  ["an arbitrary hex", 'className="bg-[#0af]"', "bg-[#0af]"],
  ["an arbitrary rgb", 'className="text-[rgb(0,0,0)]"', "text-[rgb(0,0,0)]"],
  ["an inline rgb", 'style={{ color: "rgb(1, 2, 3)" }}', "rgb("],
  ["an oklch", 'fill: "oklch(0.7 0.1 200)"', "oklch("],
  ["a color-mix", "background: color-mix(in oklab, red, blue)", "color-mix("],
])("fails on %s", (_name, line, found) => {
  expect(offences("a.tsx", line)).toEqual([{ file: "a.tsx", line: 1, found }]);
});

test("a colour token defined in CSS, and a hex there, fail", () => {
  expect(offences("a.css", ":root { --color-brand: var(--primary); }").map((o) => o.found)).toEqual(["--color-brand:"]);
  expect(offences("a.css", ".x { color: #ff00aa; }").map((o) => o.found)).toEqual(["#ff00aa"]);
});

test("passes the package's tokens, references to them and look-alikes", () => {
  const ok = [
    'className="bg-card bg-muted text-success border-warning fill-destructive text-muted-foreground ring-ring"',
    '"var(--color-card)" "var(--color-success)" stroke="var(--chart-1)"',
    'const title = `#${pr.number}`; const label = "PR #123"; className="bg-redacted text-whitespace"',
    '@import "@donaldgifford/design-system/globals.css";',
  ].join("\n");
  expect(offences("a.tsx", ok)).toEqual([]);
  expect(offences("a.css", '@import "@donaldgifford/design-system/globals.css";')).toEqual([]);
});

test("scanTree reports file and line, and skips other and generated files", async () => {
  const dir = await mkdtemp(join(tmpdir(), "boop-tokens-"));
  try {
    await mkdir(join(dir, "views"));
    await writeFile(join(dir, "README.md"), "bg-red-500");
    await writeFile(join(dir, "schema.gen.ts"), "bg-red-500");
    await writeFile(join(dir, "views", "a.tsx"), 'const a = 1;\nconst c = "text-blue-600";');
    expect(await scanTree(dir)).toEqual([{ file: join(dir, "views", "a.tsx"), line: 2, found: "text-blue-600" }]);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});
