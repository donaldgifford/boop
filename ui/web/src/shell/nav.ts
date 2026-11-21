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
  /** A count beside the label. */
  count?: number;
}

export const NAV: readonly NavItem[] = [
  { group: "FLEET", label: "Estate", to: "/", icon: IconLayoutGrid },
  { group: "FLEET", label: "Pull requests", to: "/pulls", icon: IconGitPullRequest, count: 318 },
  { group: "FLEET", label: "Waiting", to: "/waiting", icon: IconClock, count: 57 },
  { group: "FLEET", label: "Packages", to: "/packages", icon: IconPackage },
  { group: "FLEET", label: "Repositories", to: "/repositories", icon: IconList },
  { group: "ACTIVITY", label: "Runs", to: "/runs", icon: IconPlayerPlay },
  { group: "ACTIVITY", label: "Events", to: "/events", icon: IconActivity },
  { group: "ACTIVITY", label: "Deliveries", to: "/deliveries", icon: IconWebhook },
  { group: "SYSTEM", label: "Status", to: "/status", icon: IconHeartbeat },
];

export const GROUP_LABELS: Record<NavGroup, string> = { FLEET: "Fleet", ACTIVITY: "Activity", SYSTEM: "System" };

export function navGroups(items: readonly NavItem[]): [NavGroup, NavItem[]][] {
  const order: NavGroup[] = ["FLEET", "ACTIVITY", "SYSTEM"];
  return order.map((g) => [g, items.filter((i) => i.group === g)] as [NavGroup, NavItem[]]).filter(([, is]) => is.length > 0);
}

export function labelFor(pathname: string): string {
  if (pathname.startsWith("/repositories/")) return "Repositories";
  return NAV.find((n) => n.to === pathname)?.label ?? "boop";
}
