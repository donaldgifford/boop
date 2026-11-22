import { dataTableColumns } from "@donaldgifford/design-system/blocks/data-table";
import { PageHeader } from "@donaldgifford/design-system/blocks/page-header";
import { SegmentedControl } from "@donaldgifford/design-system/blocks/segmented-control";
import { Time } from "@donaldgifford/design-system/blocks/time";
import { Input } from "@donaldgifford/design-system/components/input";
import { useNavigate, useSearch } from "@tanstack/react-router";

import type { Schemas } from "../api/client";
import { useRepositories } from "../api/queries";
import { StateBadge } from "../components/badges";
import { ListCard } from "../components/ListCard";
import { PageControls, parseScopeSearch, type ScopeSearch } from "../components/PageControls";
import { ALL, allOr, flag, oneOf, text, useSearchPatch } from "../components/search";

const STATES = ["idle", "running", "waiting", "rate-limited", "stalled", "errored"] as const;

export interface RepositoriesSearch extends ScopeSearch {
  q?: string | undefined;
  state?: string | undefined;
  has_open_prs?: boolean | undefined;
}

export function parseRepositoriesSearch(s: Record<string, unknown>): RepositoriesSearch {
  return { ...parseScopeSearch(s), q: text(s.q), state: oneOf(s.state, STATES), has_open_prs: flag(s.has_open_prs) };
}

const helper = dataTableColumns<Schemas["Repository"]>();
const columns = helper.columns([
  helper.accessor("slug", { header: "Repository", cell: ({ getValue }) => <span className="font-mono text-xs">{getValue()}</span> }),
  helper.accessor("state", { header: "State", cell: ({ getValue }) => <StateBadge state={getValue()} /> }),
  helper.accessor("profile", { header: "Profile", cell: ({ getValue }) => <span className="font-mono text-xs">{getValue()}</span>, meta: { hideBelow: "lg" } }),
  helper.accessor("last_run_at", {
    header: "Last run",
    cell: ({ row }) => (row.original.last_run_at ? <span className="flex items-center gap-2"><StateBadge state={row.original.last_outcome ?? "unknown"} /><Time iso={row.original.last_run_at} /></span> : "—"),
    meta: { hideBelow: "compact" },
  }),
  helper.accessor("open_prs", { header: "Open PRs", meta: { numeric: true } }),
  helper.accessor("waiting", { header: "Waiting", meta: { numeric: true } }),
]);

export function RepositoriesView() {
  const search = useSearch({ strict: false }) as RepositoriesSearch;
  const patch = useSearchPatch<RepositoriesSearch>();
  const navigate = useNavigate();
  const list = useRepositories({ owner: search.owner, q: search.q, state: search.state, has_open_prs: search.has_open_prs });
  return (
    <>
      <PageHeader title="Repositories" description="Onboarded repositories and their workflows" actions={<PageControls range={false} />} />
      <div className="flex flex-wrap items-center gap-3">
        <Input
          type="search"
          aria-label="Search repositories"
          placeholder="Search owner/name"
          className="w-64"
          defaultValue={search.q ?? ""}
          onChange={(e) => patch({ q: text(e.target.value) })}
        />
        <SegmentedControl
          label="State"
          value={search.state ?? ALL}
          options={[{ value: ALL, label: "All" }, ...STATES.map((s) => ({ value: s, label: s }))]}
          onValueChange={(v) => patch({ state: allOr(v) })}
        />
        <SegmentedControl
          label="Open pull requests"
          value={search.has_open_prs === undefined ? ALL : String(search.has_open_prs)}
          options={[
            { value: ALL, label: "Any" },
            { value: "true", label: "With PRs" },
            { value: "false", label: "None" },
          ]}
          onValueChange={(v) => patch({ has_open_prs: flag(v) })}
        />
      </div>
      <ListCard
        list={list}
        columns={columns}
        label="Repositories"
        noun="repositories"
        getRowId={(r) => String(r.id)}
        onRowClick={(r) => void navigate({ to: "/repositories/$id", params: { id: String(r.id) } })}
      />
    </>
  );
}
