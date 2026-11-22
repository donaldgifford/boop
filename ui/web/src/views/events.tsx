import { dataTableColumns } from "@donaldgifford/design-system/blocks/data-table";
import { PageHeader } from "@donaldgifford/design-system/blocks/page-header";
import { SegmentedControl } from "@donaldgifford/design-system/blocks/segmented-control";
import { Time } from "@donaldgifford/design-system/blocks/time";
import { Badge } from "@donaldgifford/design-system/components/badge";
import { Input } from "@donaldgifford/design-system/components/input";
import { Link, useSearch } from "@tanstack/react-router";

import type { Schemas } from "../api/client";
import { useEvents } from "../api/queries";
import { ListCard } from "../components/ListCard";
import { PageControls, parseScopeSearch, type ScopeSearch } from "../components/PageControls";
import { ALL, allOr, oneOf, text, useSearchPatch } from "../components/search";

const SOURCES = ["webhook", "worker", "audit", "api"] as const;

export interface EventsSearch extends ScopeSearch {
  kind?: string | undefined;
  source?: string | undefined;
  actor?: string | undefined;
}

export function parseEventsSearch(s: Record<string, unknown>): EventsSearch {
  return { ...parseScopeSearch(s), kind: text(s.kind), source: oneOf(s.source, SOURCES), actor: text(s.actor) };
}

const helper = dataTableColumns<Schemas["Event"]>();
const columns = helper.columns([
  helper.accessor("occurred_at", { header: "When", cell: ({ getValue }) => <Time iso={getValue()} /> }),
  helper.accessor("kind", { header: "Kind", cell: ({ getValue }) => <span className="font-mono text-xs">{getValue()}</span> }),
  helper.accessor("source", { header: "Source", cell: ({ getValue }) => <Badge variant="outline">{getValue()}</Badge>, meta: { hideBelow: "lg" } }),
  helper.accessor("actor_login", { header: "Actor", cell: ({ getValue }) => getValue() ?? "—", meta: { hideBelow: "compact" } }),
  helper.accessor("summary", {
    header: "Summary",
    cell: ({ row }) =>
      row.original.repo_id ? (
        <Link to="/repositories/$id" params={{ id: String(row.original.repo_id) }} className="underline-offset-2 hover:underline">
          {row.original.summary}
        </Link>
      ) : (
        row.original.summary
      ),
  }),
]);

// EventsView is the event stream, newest first, refetched every 15 s.
export function EventsView() {
  const search = useSearch({ strict: false }) as EventsSearch;
  const patch = useSearchPatch<EventsSearch>();
  const list = useEvents({ owner: search.owner, kind: search.kind, source: search.source, actor: search.actor });
  return (
    <>
      <PageHeader title="Events" description="Everything boop recorded, newest first; refreshes every 15 s" actions={<PageControls range={false} />} />
      <div className="flex flex-wrap items-center gap-3">
        <SegmentedControl
          label="Source"
          value={search.source ?? ALL}
          options={[{ value: ALL, label: "All" }, ...SOURCES.map((s) => ({ value: s, label: s }))]}
          onValueChange={(v) => patch({ source: allOr(v) })}
        />
        <Input aria-label="Kind" placeholder="kind, e.g. run.finished" className="w-56" defaultValue={search.kind ?? ""} onChange={(e) => patch({ kind: text(e.target.value) })} />
        <Input aria-label="Actor" placeholder="actor login" className="w-40" defaultValue={search.actor ?? ""} onChange={(e) => patch({ actor: text(e.target.value) })} />
      </div>
      <ListCard list={list} columns={columns} label="Events, newest first" noun="events" getRowId={(e) => String(e.id)} />
    </>
  );
}
