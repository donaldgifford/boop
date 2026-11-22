import { expect, test } from "bun:test";

import { ESLint } from "eslint";

const cwd = new URL("..", import.meta.url).pathname;

// The HTML-sink ban is only worth something if it fires: a probe that
// uses every sink must get one error per sink.
test("ESLint bans every HTML sink in the SPA", async () => {
  const probe = `
export function Probe({ html }: { html: string }) {
  const el = document.createElement("div");
  el.innerHTML = html;
  el.outerHTML = html;
  el.insertAdjacentHTML("beforeend", html);
  document.write(html);
  const props = { dangerouslySetInnerHTML: { __html: html } };
  return <div dangerouslySetInnerHTML={{ __html: html }} {...props} />;
}
`;
  const [result] = await new ESLint({ cwd }).lintText(probe, { filePath: "web/src/probe.tsx" });
  expect((result?.messages ?? []).filter((m) => m.ruleId === "no-restricted-syntax")).toHaveLength(6);
});

test("ESLint bans radix and zod under web/, not in the server", async () => {
  const probe = `
import { Dialog } from "radix-ui";
import * as Popover from "@radix-ui/react-popover";
import { z } from "zod";
import { problemSchema } from "@donaldgifford/design-system/lib/problem";
import { ApiError } from "@donaldgifford/design-system/lib/errors";
export const used = [Dialog, Popover, z, problemSchema, ApiError];
`;
  const eslint = new ESLint({ cwd });
  const [web] = await eslint.lintText(probe, { filePath: "web/src/probe.tsx" });
  expect((web?.messages ?? []).filter((m) => m.ruleId === "no-restricted-imports").map((m) => m.line)).toEqual([2, 3, 4, 5]);
  const [server] = await eslint.lintText(probe, { filePath: "server/probe.ts" });
  expect((server?.messages ?? []).filter((m) => m.ruleId === "no-restricted-imports")).toHaveLength(0);
});
