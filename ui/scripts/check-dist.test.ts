import { expect, test } from "bun:test";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { offences, scanDist } from "./check-dist";

test.each([
  ["a CSS data: font", "a.css", '@font-face{src:url(data:font/woff2;base64,AAAA) format("woff2")}', "data: URL"],
  ["a JS data: image", "b.js", 'const i = "data:image/svg+xml,%3Csvg%3E";', "data: URL"],
  ["a Google Fonts import", "c.css", '@import url("https://fonts.googleapis.com/css2?family=Inter");', "Google Fonts URL"],
  ["a gstatic font", "d.css", "src:url(https://fonts.gstatic.com/s/x.woff2)", "Google Fonts URL"],
])("fails on %s", (_name, file, content, pattern) => {
  expect(offences(file, content)).toEqual([{ file, pattern }]);
});

test("passes hashed asset references and prose", () => {
  expect(offences("a.css", '@font-face{src:url(/assets/inter-latin-400-CDDApCn2.woff2) format("woff2")}')).toEqual([]);
  expect(offences("b.js", 'const label = "metadata: none"; const key = { data: 1 };')).toEqual([]);
});

test("scanDist walks nested CSS and JS only", async () => {
  const dir = await mkdtemp(join(tmpdir(), "boop-dist-"));
  try {
    await mkdir(join(dir, "assets"));
    await writeFile(join(dir, "index.html"), '<link href="https://fonts.googleapis.com">');
    await writeFile(join(dir, "assets", "ok.css"), "body{margin:0}");
    await writeFile(join(dir, "assets", "bad.js"), 'u="data:image/png;base64,AA"');
    await writeFile(join(dir, "assets", "font.woff2"), "data:font/woff2;base64,");
    expect(await scanDist(dir)).toEqual([{ file: join(dir, "assets", "bad.js"), pattern: "data: URL" }]);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});
