import { IconFileText, IconRefresh, IconShieldCheck } from "@tabler/icons-react";

import type { SwitcherApp } from "../candidates/AppSwitcher";

// In the real UI this comes from the BFF's /ui/config, which the chart
// fills from `ui.links` and, later, the app list (INV-0005 finding 9).
// The mockup hard-codes a deployment with all three apps. `?apps=off` on
// any URL shows the default, with no list configured.
const ALL_APPS: readonly SwitcherApp[] = [
  { id: "boop", name: "boop", subtitle: "renovate · fleet", icon: IconRefresh, url: "/" },
  { id: "repo-guardian", name: "Repo Guardian", subtitle: "compliance · read-only", icon: IconShieldCheck, url: "https://repo-guardian.example.test" },
  { id: "docz", name: "docz", subtitle: "design docs · review", icon: IconFileText, url: "https://docz.example.test" },
];

export function configuredApps(): readonly SwitcherApp[] {
  const off = new URLSearchParams(window.location.search).get("apps") === "off";
  return off ? ALL_APPS.filter((app) => app.id === "boop") : ALL_APPS;
}

export const ME = { name: "Donald Gifford", subject: "donald@fartlab.dev" };
