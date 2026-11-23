import { ErrorState } from "@donaldgifford/design-system/blocks/error-state";
import { InlineEmpty, InlineLoading } from "@donaldgifford/design-system/blocks/inline-state";
import { Dot, type DotTone, List, ListRow } from "@donaldgifford/design-system/blocks/list-row";
import { LoadMore } from "@donaldgifford/design-system/blocks/load-more";
import { Time } from "@donaldgifford/design-system/blocks/time";
import { Badge } from "@donaldgifford/design-system/components/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@donaldgifford/design-system/components/card";
import { Link } from "@tanstack/react-router";

import type { Schemas } from "../api/client";
import { daysSince } from "../lib/format";
import type { ListState } from "./ListCard";

type Item = Schemas["DashboardItem"];

// SECTIONS are the Dependency Dashboard's sections in the order the
// dashboard shows them, with the dot each gets. The API's enum is open:
// an unknown section lands after these under its own name.
const SECTIONS: readonly { key: string; label: string; tone: DotTone }[] = [
  { key: "pending-approval", label: "Pending approval", tone: "warn" },
  { key: "awaiting-schedule", label: "Awaiting schedule", tone: "info" },
  { key: "rate-limited", label: "Rate-limited", tone: "warn" },
  { key: "pending-status-checks", label: "Pending status checks", tone: "info" },
  { key: "errored", label: "Errored", tone: "bad" },
  { key: "open", label: "Open", tone: "ok" },
  { key: "ignored", label: "Ignored", tone: "neutral" },
  { key: "other", label: "Other", tone: "neutral" },
];

export const sectionKeys = SECTIONS.map((s) => s.key);

// groupBySection keeps the API's order inside a section and the
// dashboard's order across them.
export function groupBySection(items: readonly Item[]): { key: string; label: string; tone: DotTone; items: Item[] }[] {
  const by = new Map<string, Item[]>();
  for (const it of items) by.set(it.section, [...(by.get(it.section) ?? []), it]);
  const known = SECTIONS.filter((s) => by.has(s.key)).map((s) => ({ ...s, items: by.get(s.key) ?? [] }));
  const unknown = [...by.keys()]
    .filter((k) => !sectionKeys.includes(k))
    .sort()
    .map((k) => ({ key: k, label: k, tone: "neutral" as DotTone, items: by.get(k) ?? [] }));
  return [...known, ...unknown];
}

// dashboardURL is the repository's Dependency Dashboard issue: the
// issue itself when the API knows it (dashboard_url), else GitHub's
// issue search on Renovate's title.
export function dashboardURL(slug: string, url?: string | null): string {
  return url ?? `https://github.com/${slug}/issues?q=${encodeURIComponent('is:issue in:title "Dependency Dashboard"')}`;
}

// WaitingSections is a page of Dependency Dashboard items, one card per
// section. showRepo adds the repository to each row (the estate-wide
// page); a repository's own tab leaves it out.
export function WaitingSections({ list, showRepo }: { list: ListState<Item>; showRepo: boolean }) {
  if (list.isError) return <ErrorState error={list.error} onRetry={() => void list.refetch()} />;
  if (list.isPending) return <InlineLoading />;
  if (list.items.length === 0) return <InlineEmpty>Nothing is waiting on a Dependency Dashboard.</InlineEmpty>;
  return (
    <div className="grid gap-4">
      {groupBySection(list.items).map((s) => (
        <Card key={s.key} aria-labelledby={`section-${s.key}`}>
          <CardHeader>
            <CardTitle id={`section-${s.key}`} className="flex items-center gap-2">
              <Dot tone={s.tone} />
              {s.label}
              <Badge variant="muted">{s.items.length}</Badge>
            </CardTitle>
          </CardHeader>
          <CardContent>
            <List>
              {s.items.map((it) => (
                <WaitingRow key={`${it.repo_id}:${it.marker}:${it.branch}`} item={it} showRepo={showRepo} />
              ))}
            </List>
          </CardContent>
        </Card>
      ))}
      <LoadMore loaded={list.items.length} hasMore={list.hasNextPage} loading={list.isFetchingNextPage} onMore={() => void list.fetchNextPage()} noun="items" />
    </div>
  );
}

function WaitingRow({ item, showRepo }: { item: Item; showRepo: boolean }) {
  const days = daysSince(item.first_seen_at);
  return (
    <ListRow
      title={item.title}
      subtitle={
        <>
          {showRepo ? (
            <>
              <Link to="/repositories/$id" params={{ id: String(item.repo_id) }} className="relative z-10 font-mono underline-offset-2 hover:underline">
                {item.slug}
              </Link>
              {" · "}
            </>
          ) : null}
          waiting since <Time iso={item.first_seen_at} />
          {days === undefined ? null : ` (${days} d)`}
          {item.deps.length > 0 ? ` · ${item.deps.join(", ")}` : null}
        </>
      }
      trailing={
        <>
          {item.ticked_at ? (
            <Badge variant="success">
              ticked{item.ticked_by ? ` by ${item.ticked_by}` : ""}
            </Badge>
          ) : null}
          <a href={dashboardURL(item.slug)} target="_blank" rel="noopener noreferrer" className="text-xs text-muted-foreground underline-offset-2 hover:underline">
            Dashboard
          </a>
        </>
      }
    />
  );
}
