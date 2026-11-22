import { IconActivity, IconClock, IconGitPullRequest, IconHeartbeat, IconLayoutGrid, IconList, IconPackage, IconPlayerPlay, IconWebhook, type TablerIcon } from "@tabler/icons-react";

// The sidebar is one array (DESIGN-0004 § Visual design), so a page adds
// its entry by editing data, not markup.
export type NavGroup = "FLEET" | "ACTIVITY" | "SYSTEM";

export type AppPath = "/" | "/pulls" | "/waiting" | "/packages" | "/repositories" | "/runs" | "/events" | "/deliveries" | "/status";

export interface NavItem {
  group: NavGroup;
  label: string;
  to: AppPath;
  icon: TablerIcon;
}

export const NAV: readonly NavItem[] = [
  { group: "FLEET", label: "Estate", to: "/", icon: IconLayoutGrid },
  { group: "FLEET", label: "Pull requests", to: "/pulls", icon: IconGitPullRequest },
  { group: "FLEET", label: "Waiting", to: "/waiting", icon: IconClock },
  { group: "FLEET", label: "Packages", to: "/packages", icon: IconPackage },
  { group: "FLEET", label: "Repositories", to: "/repositories", icon: IconList },
  { group: "ACTIVITY", label: "Runs", to: "/runs", icon: IconPlayerPlay },
  { group: "ACTIVITY", label: "Events", to: "/events", icon: IconActivity },
  { group: "ACTIVITY", label: "Deliveries", to: "/deliveries", icon: IconWebhook },
  { group: "SYSTEM", label: "Status", to: "/status", icon: IconHeartbeat },
];

export const GROUP_LABELS: Record<NavGroup, string> = { FLEET: "Fleet", ACTIVITY: "Activity", SYSTEM: "System" };

const groupOrder: readonly NavGroup[] = ["FLEET", "ACTIVITY", "SYSTEM"];

export function navGroups(items: readonly NavItem[]): [NavGroup, NavItem[]][] {
  return groupOrder.map((g): [NavGroup, NavItem[]] => [g, items.filter((i) => i.group === g)]).filter(([, is]) => is.length > 0);
}

// sectionFor is the nav entry a path belongs to: its own, or its
// section's for a detail page (/repositories/42 is under Repositories).
export function sectionFor(pathname: string): NavItem | undefined {
  const exact = NAV.find((n) => n.to === pathname);
  if (exact) return exact;
  return NAV.filter((n) => n.to !== "/" && pathname.startsWith(`${n.to}/`)).sort((a, b) => b.to.length - a.to.length)[0];
}

export function labelFor(pathname: string): string {
  return sectionFor(pathname)?.label ?? "boop";
}

// Crumb is one breadcrumb: a link when `to` is set, else the current page.
export interface Crumb {
  label: string;
  to?: AppPath;
}

// crumbsFor is the trail for a path: the section, then the detail's last
// segment (decoded, as text) under it.
export function crumbsFor(pathname: string): Crumb[] {
  const section = sectionFor(pathname);
  if (!section) return [{ label: "boop" }];
  if (section.to === pathname) return [{ label: section.label }];
  const rest = pathname.slice(section.to.length + 1).split("/").filter(Boolean);
  const last = rest.at(-1) ?? "";
  let detail = last;
  try {
    detail = decodeURIComponent(last);
  } catch {
    // Keep the raw segment.
  }
  return [{ label: section.label, to: section.to }, { label: detail }];
}
