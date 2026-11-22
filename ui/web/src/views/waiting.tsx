import { PageHeader } from "@donaldgifford/design-system/blocks/page-header";
import { SegmentedControl } from "@donaldgifford/design-system/blocks/segmented-control";
import { useNavigate, useSearch } from "@tanstack/react-router";

import { useDashboardItems } from "../api/queries";
import { PageControls, parseScopeSearch, type ScopeSearch } from "../components/PageControls";
import { WaitingSections } from "../components/waiting";

// WaitingSearch is the page's URL: the scope and how long an item has
// waited at least, in days.
export interface WaitingSearch extends ScopeSearch {
  waiting?: number | undefined;
}

const WAITING = [
  { value: "0", label: "Any" },
  { value: "7", label: "> 7 d" },
  { value: "30", label: "> 30 d" },
  { value: "90", label: "> 90 d" },
] as const;

export function parseWaitingSearch(search: Record<string, unknown>): WaitingSearch {
  const out: WaitingSearch = parseScopeSearch(search);
  const days = Number(search.waiting);
  if (Number.isInteger(days) && days > 0) out.waiting = days;
  return out;
}

// WaitingView is every unticked Dependency Dashboard item in scope,
// grouped by the dashboard's sections.
export function WaitingView() {
  const search = useSearch({ strict: false }) as WaitingSearch;
  const navigate = useNavigate();
  const list = useDashboardItems({ owner: search.owner, waiting_longer_than: search.waiting });
  return (
    <>
      <PageHeader title="Waiting" description="Dependency Dashboard items waiting on a person or a schedule" actions={<PageControls range={false} />} />
      <SegmentedControl
        label="Waiting longer than"
        value={String(search.waiting ?? 0)}
        options={WAITING.map((w) => ({ value: w.value, label: w.label }))}
        onValueChange={(next) => void navigate({ to: ".", search: (prev: WaitingSearch) => ({ ...prev, waiting: next === "0" ? undefined : Number(next) }) })}
      />
      <WaitingSections list={list} showRepo />
    </>
  );
}
