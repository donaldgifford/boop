import { DataTable, dataTableColumns } from "@donaldgifford/design-system/blocks/data-table";
import { DetailsGrid } from "@donaldgifford/design-system/blocks/details-grid";
import { LoadMore } from "@donaldgifford/design-system/blocks/load-more";
import { PageHeader } from "@donaldgifford/design-system/blocks/page-header";
import { Timeline } from "@donaldgifford/design-system/blocks/timeline";
import { Badge } from "@donaldgifford/design-system/components/badge";
import { Button } from "@donaldgifford/design-system/components/button";
import { Card } from "@donaldgifford/design-system/components/card";
import { useMediaQuery } from "@donaldgifford/design-system/hooks/use-media-query";
import { cn } from "@donaldgifford/design-system/lib/utils";
import { DETAIL_INLINE_QUERY, DetailPanel, DetailSection } from "@donaldgifford/design-system/shell/detail-panel";
import { IconExternalLink } from "@tabler/icons-react";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { useState } from "react";

import { type ActiveFilter, FilterBar } from "../candidates/FilterBar";
import { InlineEmpty } from "../candidates/InlineState";
import { SegmentedControl } from "../candidates/SegmentedControl";
import { StateBadge, UpdateTypeBadge } from "../components/badges";
import { Endpoint, PageControls, parseScopeSearch, type ScopeSearch } from "../components/PageControls";
import { type PullRequest, type PullState, pulls, pullTimeline } from "../fixtures";
import { when } from "../lib/when";

export interface PullsSearch extends ScopeSearch {
  state?: PullState | undefined;
  open?: string | undefined;
}

const STATES: readonly PullState[] = ["open", "merged", "closed"];

export function parsePullsSearch(search: Record<string, unknown>): PullsSearch {
  const out: PullsSearch = parseScopeSearch(search);
  if (typeof search.state === "string" && (STATES as readonly string[]).includes(search.state)) out.state = search.state as PullState;
  if (typeof search.open === "string" && search.open !== "") out.open = search.open;
  return out;
}

const helper = dataTableColumns<PullRequest>();
const columns = helper.columns([
  helper.accessor("repo", {
    header: "Repository",
    cell: ({ row }) => (
      <span className="font-mono text-xs">
        {row.original.repo} <span className="text-muted-foreground">#{row.original.number}</span>
      </span>
    ),
  }),
  helper.accessor("title", { header: "Update", cell: ({ row }) => <UpdateCell pr={row.original} />, enableSorting: false }),
  helper.accessor("updateType", { header: "Type", cell: ({ getValue }) => <UpdateTypeBadge type={getValue()} />, enableSorting: false }),
  helper.accessor("manager", { header: "Manager", cell: ({ getValue }) => <span className="font-mono text-xs">{getValue()}</span>, meta: { hideBelow: "lg" } }),
  helper.accessor("checks", { header: "Checks", cell: ({ getValue }) => <StateBadge state={getValue()} />, enableSorting: false, meta: { hideBelow: "compact" } }),
  helper.accessor("ageDays", { header: "Age", cell: ({ getValue }) => `${getValue()} d`, meta: { numeric: true } }),
]);

function UpdateCell({ pr }: { pr: PullRequest }) {
  const update = pr.updates[0];
  if (!update) return <>{pr.title}</>;
  return (
    <span className="font-mono text-xs">
      {update.package} <span className="text-muted-foreground">{update.from} →</span> {update.to}
    </span>
  );
}

const PAGE = 12;

export function PullsView() {
  const search = useSearch({ strict: false }) as PullsSearch;
  const navigate = useNavigate();
  const inline = useMediaQuery(DETAIL_INLINE_QUERY);
  const [extra, setExtra] = useState<readonly ActiveFilter[]>([{ name: "older_than", value: "0d" }]);
  const [shown, setShown] = useState(PAGE);

  const state = search.state ?? "open";
  const inState = pulls.filter((p) => p.state === state && (search.owner === undefined || p.repo.startsWith(`${search.owner}/`)));
  const rows = inState.slice(0, shown);
  const selected = pulls.find((p) => p.id === search.open);
  const patch = (next: Partial<PullsSearch>) => void navigate({ to: ".", search: (prev: PullsSearch) => ({ ...prev, ...next }) });

  return (
    <>
      <PageHeader title="Pull requests" description={<Endpoint path="/api/v1/pull-requests" />} actions={<PageControls range={false} />} />
      <div className="flex flex-wrap items-center gap-3">
        <SegmentedControl
          label="State"
          value={state}
          options={STATES.map((s) => ({ value: s, label: s[0]?.toUpperCase() + s.slice(1) }))}
          onValueChange={(next) => {
            setShown(PAGE);
            patch({ state: next, open: undefined });
          }}
        />
        <FilterBar
          className="flex-1"
          filters={[{ name: "state", value: state }, ...extra]}
          onRemove={(name) => setExtra((prev) => prev.filter((f) => f.name !== name))}
          onAdd={() => setExtra((prev) => (prev.some((f) => f.name === "automerge") ? prev : [...prev, { name: "automerge", value: "false" }]))}
          onClear={() => setExtra([])}
          count={`${inState.length} of ${pulls.length}`}
        />
      </div>

      <div className={cn("grid items-start gap-4", selected && inline && "grid-cols-[minmax(0,1fr)_minmax(0,28rem)]")}>
        <Card className="gap-0 py-0">
          <DataTable
            columns={columns}
            data={rows}
            label="Pull requests, oldest first"
            bare
            getRowId={(row) => row.id}
            onRowClick={(row) => patch({ open: row.id })}
            selectedId={selected?.id}
            compact={Boolean(selected && inline)}
            empty={<InlineEmpty>No pull requests match these filters.</InlineEmpty>}
          />
          <LoadMore loaded={rows.length} total={inState.length} hasMore={rows.length < inState.length} onMore={() => setShown((n) => n + PAGE)} noun="pull requests" />
        </Card>

        <DetailPanel
          open={selected !== undefined}
          onOpenChange={(open) => {
            if (!open) patch({ open: undefined });
          }}
          inline={inline}
          subject={selected?.id}
          title={selected ? `${selected.repo} #${selected.number}` : ""}
          description={selected?.title}
          footer={
            <Button variant="outline" size="sm" nativeButton={false} render={<a href="https://github.com" target="_blank" rel="noopener noreferrer" />}>
              Open on GitHub
              <IconExternalLink aria-hidden="true" className="size-3.5" />
            </Button>
          }
        >
          {selected ? <PullDetail pr={selected} /> : null}
        </DetailPanel>
      </div>
    </>
  );
}

function PullDetail({ pr }: { pr: PullRequest }) {
  return (
    <>
      <DetailsGrid
        columns={2}
        items={[
          { label: "State", value: <StateBadge state={pr.state} /> },
          { label: "Checks", value: <StateBadge state={pr.checks} /> },
          { label: "Opened", value: when(pr.openedAt) },
          { label: "Age", value: `${pr.ageDays} days` },
          { label: "Manager", value: <span className="font-mono text-xs">{pr.manager}</span> },
          { label: "Automerge", value: pr.automerge ? <Badge variant="success">on</Badge> : <Badge variant="muted">off</Badge> },
        ]}
      />
      <DetailSection title="Updates">
        <ul className="grid gap-2">
          {pr.updates.map((u) => (
            <li key={u.purl} className="rounded-lg border px-3 py-2">
              <div className="flex items-center justify-between gap-2">
                <span className="font-mono text-xs">{u.package}</span>
                <UpdateTypeBadge type={u.updateType} />
              </div>
              <div className="mt-1 font-mono text-xs">
                <span className="text-muted-foreground">{u.from} →</span> {u.to}
              </div>
              <div className="mt-1 truncate font-mono text-label text-muted-foreground">{u.purl}</div>
            </li>
          ))}
        </ul>
      </DetailSection>
      <DetailSection title="Timeline">
        <Timeline
          items={pullTimeline(pr).map((e) => ({ id: e.id, actor: e.actor, verb: e.verb, object: e.object, detail: e.detail, when: when(e.at), tone: e.tone, tags: [{ label: e.kind, variant: "outline" as const }] }))}
        />
      </DetailSection>
    </>
  );
}
