import { PageHeader } from "@donaldgifford/design-system/blocks/page-header";
import { SegmentedControl } from "@donaldgifford/design-system/blocks/segmented-control";
import { useNavigate, useSearch } from "@tanstack/react-router";

import { useRuns } from "../api/queries";
import { ListCard } from "../components/ListCard";
import { PageControls, parseScopeSearch, type ScopeSearch } from "../components/PageControls";
import { runColumns } from "../components/runs";
import { ALL, allOr, oneOf, useSearchPatch } from "../components/search";

const OUTCOMES = ["Succeeded", "Skipped", "Failed", "TimedOut"] as const;
const CAUSES = ["schedule", "webhook", "recheck", "unknown"] as const;

export interface RunsSearch extends ScopeSearch {
  outcome?: string | undefined;
  cause?: string | undefined;
}

export function parseRunsSearch(s: Record<string, unknown>): RunsSearch {
  return { ...parseScopeSearch(s), outcome: oneOf(s.outcome, OUTCOMES), cause: oneOf(s.cause, CAUSES) };
}

export function RunsView() {
  const search = useSearch({ strict: false }) as RunsSearch;
  const patch = useSearchPatch<RunsSearch>();
  const navigate = useNavigate();
  const list = useRuns({ owner: search.owner, outcome: search.outcome, cause: search.cause });
  return (
    <>
      <PageHeader title="Runs" description="Renovate runs, newest first" actions={<PageControls range={false} />} />
      <div className="flex flex-wrap items-center gap-3">
        <SegmentedControl
          label="Outcome"
          value={search.outcome ?? ALL}
          options={[{ value: ALL, label: "All" }, ...OUTCOMES.map((o) => ({ value: o, label: o }))]}
          onValueChange={(v) => patch({ outcome: allOr(v) })}
        />
        <SegmentedControl
          label="Cause"
          value={search.cause ?? ALL}
          options={[{ value: ALL, label: "All" }, ...CAUSES.map((c) => ({ value: c, label: c }))]}
          onValueChange={(v) => patch({ cause: allOr(v) })}
        />
      </div>
      <ListCard
        list={list}
        columns={runColumns}
        label="Runs, newest first"
        noun="runs"
        getRowId={(r) => r.id}
        onRowClick={(r) => void navigate({ to: "/repositories/$id", params: { id: String(r.repo_id) }, search: { tab: "runs" } })}
      />
    </>
  );
}
