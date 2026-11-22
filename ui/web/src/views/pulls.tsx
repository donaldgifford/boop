import { dataTableColumns } from "@donaldgifford/design-system/blocks/data-table";
import { DetailsGrid } from "@donaldgifford/design-system/blocks/details-grid";
import { ErrorState } from "@donaldgifford/design-system/blocks/error-state";
import { FilterBar } from "@donaldgifford/design-system/blocks/filter-bar";
import { FilterChip } from "@donaldgifford/design-system/blocks/filter-chip";
import { InlineEmpty, InlineLoading } from "@donaldgifford/design-system/blocks/inline-state";
import { OptionHeading, OptionItem } from "@donaldgifford/design-system/blocks/option-rows";
import { PageHeader } from "@donaldgifford/design-system/blocks/page-header";
import { SegmentedControl } from "@donaldgifford/design-system/blocks/segmented-control";
import { Time } from "@donaldgifford/design-system/blocks/time";
import { Timeline } from "@donaldgifford/design-system/blocks/timeline";
import { Badge } from "@donaldgifford/design-system/components/badge";
import { Button } from "@donaldgifford/design-system/components/button";
import { Popover, PopoverContent, PopoverTrigger } from "@donaldgifford/design-system/components/popover";
import { useMediaQuery } from "@donaldgifford/design-system/hooks/use-media-query";
import { cn } from "@donaldgifford/design-system/lib/utils";
import { DETAIL_INLINE_QUERY, DetailPanel, DetailSection } from "@donaldgifford/design-system/shell/detail-panel";
import { IconExternalLink } from "@tabler/icons-react";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { useState } from "react";

import type { Schemas } from "../api/client";
import { type PullRequestFilters, usePullRequest, usePullRequests } from "../api/queries";
import { StateBadge } from "../components/badges";
import { eventItem } from "../components/events";
import { ListCard } from "../components/ListCard";
import { PageControls, parseScopeSearch, type ScopeSearch } from "../components/PageControls";
import { UpdateRow, UpdateSummary } from "../components/updates";
import { daysSince } from "../lib/format";

const STATES = ["open", "merged", "closed"] as const;

// PullsSearch is the page's URL: the scope, the filters, and the open
// pull request as "<repo id>-<number>".
export interface PullsSearch extends ScopeSearch {
  state?: string | undefined;
  manager?: string | undefined;
  update_type?: string | undefined;
  older_than?: number | undefined;
  automerge?: boolean | undefined;
  label?: string | undefined;
  purl?: string | undefined;
  open?: string | undefined;
}

// The filters a chip shows and removes. state has its own control.
const CHIP_FILTERS = ["manager", "update_type", "older_than", "automerge", "label", "purl"] as const;
type ChipFilter = (typeof CHIP_FILTERS)[number];

const str = (v: unknown) => (typeof v === "string" && v !== "" ? v : undefined);

export function parsePullsSearch(search: Record<string, unknown>): PullsSearch {
  const out: PullsSearch = parseScopeSearch(search);
  const state = str(search.state);
  if (state && (STATES as readonly string[]).includes(state)) out.state = state;
  for (const k of ["manager", "update_type", "label", "purl", "open"] as const) {
    const v = str(search[k]);
    if (v) out[k] = v;
  }
  const older = Number(search.older_than);
  if (Number.isInteger(older) && older > 0) out.older_than = older;
  if (search.automerge === true || search.automerge === "true") out.automerge = true;
  if (search.automerge === false || search.automerge === "false") out.automerge = false;
  return out;
}

// PRESETS are the filters "Add filter" offers.
const PRESETS: readonly { label: string; patch: Partial<PullsSearch> }[] = [
  { label: "major updates", patch: { update_type: "major" } },
  { label: "not automerged", patch: { automerge: false } },
  { label: "open over 30 days", patch: { older_than: 30 } },
  { label: "open over 90 days", patch: { older_than: 90 } },
];

export function filtersOf(s: PullsSearch): PullRequestFilters {
  return {
    state: s.state ?? "open",
    owner: s.owner,
    manager: s.manager,
    update_type: s.update_type,
    older_than: s.older_than,
    automerge: s.automerge,
    label: s.label,
    purl: s.purl,
  };
}

export const prId = (pr: Pick<Schemas["PullRequest"], "repo_id" | "number">) => `${pr.repo_id}-${pr.number}`;

function parsePrId(id: string | undefined): { repoId: number; number: number } | undefined {
  const m = id?.match(/^(\d+)-(\d+)$/);
  return m ? { repoId: Number(m[1]), number: Number(m[2]) } : undefined;
}

const helper = dataTableColumns<Schemas["PullRequest"]>();
const columns = helper.columns([
  helper.accessor("slug", {
    header: "Repository",
    cell: ({ row }) => (
      <span className="font-mono text-xs">
        {row.original.slug} <span className="text-muted-foreground">#{row.original.number}</span>
      </span>
    ),
  }),
  helper.accessor("title", { header: "Update", cell: ({ row }) => <UpdateSummary updates={row.original.updates} fallback={row.original.title} /> }),
  helper.accessor("state", { header: "State", cell: ({ getValue }) => <StateBadge state={getValue()} />, meta: { hideBelow: "compact" } }),
  helper.accessor((pr) => pr.updates[0]?.manager ?? "", { id: "manager", header: "Manager", cell: ({ getValue }) => <span className="font-mono text-xs">{getValue()}</span>, meta: { hideBelow: "lg" } }),
  helper.accessor("opened_at", { header: "Age", cell: ({ getValue }) => <AgeCell iso={getValue()} />, meta: { numeric: true } }),
]);

function AgeCell({ iso }: { iso: string | null | undefined }) {
  const d = daysSince(iso);
  return d === undefined ? <span className="text-muted-foreground">—</span> : <>{d} d</>;
}

export function PullsView() {
  const search = useSearch({ strict: false }) as PullsSearch;
  const navigate = useNavigate();
  const inline = useMediaQuery(DETAIL_INLINE_QUERY);
  const list = usePullRequests(filtersOf(search));
  const selected = parsePrId(search.open);
  const patch = (next: Partial<PullsSearch>) => void navigate({ to: ".", search: (prev: PullsSearch) => ({ ...prev, ...next }) });
  const active = CHIP_FILTERS.filter((k) => search[k] !== undefined);
  const clearAll = Object.fromEntries(CHIP_FILTERS.map((k) => [k, undefined])) as Partial<PullsSearch>;

  return (
    <>
      <PageHeader title="Pull requests" description="Renovate's pull requests, oldest first" actions={<PageControls range={false} />} />
      <div className="flex flex-wrap items-center gap-3">
        <SegmentedControl
          label="State"
          value={search.state ?? "open"}
          options={STATES.map((s) => ({ value: s, label: s[0]?.toUpperCase() + s.slice(1) }))}
          onValueChange={(next) => patch({ state: next, open: undefined })}
        />
        <FilterBar className="flex-1" count={list.isPending ? undefined : `${list.items.length}${list.hasNextPage ? "+" : ""} shown`} {...(active.length > 0 ? { onClear: () => patch(clearAll) } : {})}>
          {active.map((k) => (
            <RemovableChip key={k} name={k} value={String(search[k])} onRemove={() => patch({ [k]: undefined } as Partial<PullsSearch>)} />
          ))}
          <AddFilter onAdd={patch} />
        </FilterBar>
      </div>

      <div className={cn("grid items-start gap-4", selected && inline && "grid-cols-[minmax(0,1fr)_minmax(0,28rem)]")}>
        <ListCard
          list={list}
          columns={columns}
          label="Pull requests, oldest first"
          noun="pull requests"
          getRowId={prId}
          onRowClick={(row) => patch({ open: prId(row) })}
          selectedId={search.open}
          compact={Boolean(selected && inline)}
        />
        <PullDrawer selected={selected} inline={inline} onClose={() => patch({ open: undefined })} />
      </div>
    </>
  );
}

function RemovableChip({ name, value, onRemove }: { name: ChipFilter; value: string; onRemove: () => void }) {
  return (
    <FilterChip
      name={name}
      value={
        <>
          {value}{" "}
          <span aria-hidden="true" className="ml-0.5 opacity-70">
            ×
          </span>
        </>
      }
      aria-label={`Remove filter ${name} = ${value}`}
      onClick={onRemove}
    />
  );
}

function AddFilter({ onAdd }: { onAdd: (patch: Partial<PullsSearch>) => void }) {
  const [open, setOpen] = useState(false);
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger render={<FilterChip />} />
      <PopoverContent align="start" className="w-56 gap-0 p-1">
        <OptionHeading>Add filter</OptionHeading>
        {PRESETS.map((p) => (
          <OptionItem
            key={p.label}
            onClick={() => {
              setOpen(false);
              onAdd(p.patch);
            }}
          >
            {p.label}
          </OptionItem>
        ))}
      </PopoverContent>
    </Popover>
  );
}

function PullDrawer({ selected, inline, onClose }: { selected: { repoId: number; number: number } | undefined; inline: boolean; onClose: () => void }) {
  const detail = usePullRequest(selected?.repoId, selected?.number);
  const pr = detail.data;
  return (
    <DetailPanel
      open={selected !== undefined}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
      inline={inline}
      subject={selected ? `${selected.repoId}-${selected.number}` : undefined}
      title={pr ? `${pr.slug} #${pr.number}` : selected ? `#${selected.number}` : ""}
      description={pr?.title}
      footer={
        pr ? (
          <Button variant="outline" size="sm" nativeButton={false} render={<a href={pr.url} target="_blank" rel="noopener noreferrer" />}>
            Open on GitHub
            <IconExternalLink aria-hidden="true" className="size-3.5" />
          </Button>
        ) : null
      }
    >
      {detail.isError ? <ErrorState error={detail.error} /> : pr ? <PullDetail pr={pr} /> : selected ? <InlineLoading /> : null}
    </DetailPanel>
  );
}

function PullDetail({ pr }: { pr: Schemas["PullRequestDetail"] }) {
  const age = daysSince(pr.opened_at);
  return (
    <>
      <DetailsGrid
        columns={2}
        items={[
          { label: "State", value: <StateBadge state={pr.state} /> },
          { label: "Automerge", value: pr.automerge ? <Badge variant="success">on</Badge> : <Badge variant="muted">off</Badge> },
          { label: "Opened", value: pr.opened_at ? <Time iso={pr.opened_at} /> : "—" },
          { label: "Age", value: age === undefined ? "—" : `${age} days` },
          { label: "Branch", value: <span className="font-mono text-xs">{pr.branch}</span> },
          { label: "Labels", value: pr.labels.length > 0 ? pr.labels.join(", ") : "—" },
        ]}
      />
      <DetailSection title="Updates">
        {pr.updates.length > 0 ? (
          <ul className="grid gap-2">
            {pr.updates.map((u) => (
              <UpdateRow key={`${u.package_file}:${u.package_name}`} update={u} />
            ))}
          </ul>
        ) : (
          <InlineEmpty>No updates recorded for this branch yet.</InlineEmpty>
        )}
      </DetailSection>
      <DetailSection title="Timeline">{pr.timeline.length > 0 ? <Timeline items={pr.timeline.map(eventItem)} /> : <InlineEmpty>No events.</InlineEmpty>}</DetailSection>
    </>
  );
}
