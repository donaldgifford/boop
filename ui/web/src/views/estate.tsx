import { BarBuckets, bucketsTable } from "@donaldgifford/design-system/blocks/bar-buckets";
import { BarList, barListTable } from "@donaldgifford/design-system/blocks/bar-list";
import { ChartCard } from "@donaldgifford/design-system/blocks/chart-card";
import { DataTable, dataTableColumns } from "@donaldgifford/design-system/blocks/data-table";
import { ErrorState } from "@donaldgifford/design-system/blocks/error-state";
import { InlineEmpty } from "@donaldgifford/design-system/blocks/inline-state";
import { Kpi } from "@donaldgifford/design-system/blocks/kpi";
import { PageHeader } from "@donaldgifford/design-system/blocks/page-header";
import type { StackSegment } from "@donaldgifford/design-system/blocks/stacked-bars";
import { StackedBarsOverTime, type StackTimePoint, stackTimeTable } from "@donaldgifford/design-system/blocks/stacked-bars-over-time";
import { Time } from "@donaldgifford/design-system/blocks/time";
import { TrendChart, type TrendPoint, trendTable } from "@donaldgifford/design-system/blocks/trend-chart";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@donaldgifford/design-system/components/card";
import { PageSkeleton } from "@donaldgifford/design-system/shell/page-skeleton";

import type { Schemas } from "../api/client";
import { useEstate, useEstateSeries, useInstallations } from "../api/queries";
import { PageControls, useScope } from "../components/PageControls";
import { count, daysSince, rangeDays } from "../lib/format";

// OUTCOMES are the run outcomes the runs chart stacks; any other outcome
// the API reports is counted under "Other".
const OUTCOMES: readonly StackSegment[] = [
  { key: "Succeeded", label: "Succeeded", color: "var(--success)" },
  { key: "Skipped", label: "Skipped", color: "var(--muted-foreground)" },
  { key: "Failed", label: "Failed", color: "var(--destructive)" },
  { key: "Other", label: "Other", color: "var(--warning)" },
];

const known = new Set(OUTCOMES.map((o) => o.key));

// stackPoints folds the series' per-outcome counts onto OUTCOMES.
export function stackPoints(points: readonly Schemas["SeriesPoint"][]): StackTimePoint[] {
  return points.map((p) => {
    const counts: Record<string, number> = {};
    for (const [k, v] of Object.entries(p.by ?? {})) {
      const key = known.has(k) ? k : "Other";
      counts[key] = (counts[key] ?? 0) + v;
    }
    return { at: p.at, counts };
  });
}

export function trendPoints(points: readonly Schemas["SeriesPoint"][]): TrendPoint[] {
  return points.map((p) => ({ at: p.at, value: p.value }));
}

const helper = dataTableColumns<Schemas["Installation"]>();
const installationColumns = helper.columns([
  helper.accessor("owner", { header: "Owner", cell: ({ getValue }) => <span className="font-mono text-xs">{getValue()}</span> }),
  helper.accessor("repositories", { header: "Repositories", meta: { numeric: true } }),
  helper.accessor("core", { header: "REST remaining", cell: ({ getValue }) => <BudgetCell budget={getValue()} />, meta: { numeric: true } }),
  helper.accessor("graphql", { header: "GraphQL remaining", cell: ({ getValue }) => <BudgetCell budget={getValue()} />, meta: { numeric: true } }),
]);

function BudgetCell({ budget }: { budget: Schemas["Budget"] | undefined }) {
  if (!budget) return <span className="text-muted-foreground">—</span>;
  return (
    <span className="font-mono text-xs">
      {count(budget.remaining)}
      {budget.used_since_run_start !== undefined ? <span className="text-muted-foreground"> (−{count(budget.used_since_run_start)})</span> : null}
    </span>
  );
}

export function EstateView() {
  const { owner, range } = useScope();
  const estate = useEstate(owner, range);
  if (estate.isPending) return <PageSkeleton />;
  if (estate.isError) return <ErrorState error={estate.error} onRetry={() => void estate.refetch()} />;
  return (
    <>
      <PageHeader title="Estate" description={<>computed <Time iso={estate.data.computed_at} style="relative" /></>} actions={<PageControls />} />
      <Kpis estate={estate.data} />
      <div className="grid gap-3 lg:grid-cols-2">
        <OpenTrend owner={owner} range={range} />
        <ChartCard title="Pull request age" description="open pull requests by age" table={bucketsTable(buckets(estate.data.age_buckets), "Open")}>
          <BarBuckets buckets={buckets(estate.data.age_buckets)} series="Open" label="Open pull requests by age" />
        </ChartCard>
        <RunsByOutcome owner={owner} range={range} />
        <ChartCard title="Top pending packages" description="repositories behind, by package" table={barListTable(topPackages(estate.data), "Package")}>
          {estate.data.top_packages.length > 0 ? <BarList items={topPackages(estate.data)} label="Repositories behind, by package" /> : <InlineEmpty>Nothing pending.</InlineEmpty>}
        </ChartCard>
      </div>
      <div className="grid gap-3 lg:grid-cols-[1fr_2fr]">
        <ChartCard title="Pull requests by ecosystem" description="open, by manager" table={barListTable(barItems(estate.data.ecosystems), "Manager")}>
          {estate.data.ecosystems.length > 0 ? <BarList items={barItems(estate.data.ecosystems)} share label="Open pull requests by manager" /> : <InlineEmpty>No open pull requests.</InlineEmpty>}
        </ChartCard>
        <Installations />
      </div>
    </>
  );
}

function Kpis({ estate }: { estate: Schemas["Estate"] }) {
  const oldest = daysSince(estate.oldest_open_pr_opened_at);
  const rate = estate.run_success_rate_24h;
  return (
    <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">
      <Kpi label="Repositories" value={count(estate.repositories)} hint="onboarded" />
      <Kpi label="Open pull requests" value={count(estate.open_prs)} hint={estate.merged_in_range === undefined ? undefined : `${count(estate.merged_in_range)} merged in ${estate.range}`} />
      <Kpi label="Waiting updates" value={count(estate.waiting)} hint="on dashboards" tone={estate.waiting > 0 ? "warning" : "default"} />
      <Kpi label="Oldest open" value={oldest === undefined ? "—" : `${oldest} d`} tone={oldest !== undefined && oldest > 30 ? "warning" : "default"} />
      <Kpi label="Runs, 24h" value={rate === null ? "—" : `${rate.toFixed(1)} %`} hint={`${count(estate.runs_24h)} runs`} tone={rate === null ? "default" : rate >= 90 ? "success" : "warning"} />
      <Kpi label="Lost deliveries" value={count(estate.lost_deliveries_24h)} hint="in 24 hours" tone={estate.lost_deliveries_24h > 0 ? "destructive" : "default"} />
    </div>
  );
}

function buckets(bs: readonly Schemas["Bucket"][]) {
  return bs.map((b) => ({ label: b.label, value: b.count }));
}

function barItems(bs: readonly Schemas["Bucket"][]) {
  return bs.map((b) => ({ label: b.label, value: b.count }));
}

function topPackages(e: Schemas["Estate"]) {
  return e.top_packages.map((p) => ({ label: p.purl, value: p.repositories, sub: `${count(p.updates)} updates` }));
}

function OpenTrend({ owner, range }: { owner: string | undefined; range: string }) {
  const series = useEstateSeries("open_prs", owner, range);
  const points = trendPoints(series.data?.points ?? []);
  return (
    <ChartCard title="Open pull requests" description={`per ${series.data?.bucket ?? "day"}, last ${range}`} table={trendTable(points, "Open", { unit: "count", head: "Day", noun: "days" })}>
      {series.isError ? <ErrorState error={series.error} /> : <TrendChart points={points} unit="count" rangeDays={rangeDays(range)} series="Open" label="Open pull requests over time" />}
    </ChartCard>
  );
}

function RunsByOutcome({ owner, range }: { owner: string | undefined; range: string }) {
  const series = useEstateSeries("runs", owner, range);
  const points = stackPoints(series.data?.points ?? []);
  return (
    <ChartCard title="Runs by outcome" description={`per ${series.data?.bucket ?? "day"}`} table={stackTimeTable(points, OUTCOMES)}>
      {series.isError ? <ErrorState error={series.error} /> : <StackedBarsOverTime points={points} segments={OUTCOMES} rangeDays={rangeDays(range)} label="Runs by outcome over time" />}
    </ChartCard>
  );
}

function Installations() {
  const list = useInstallations();
  return (
    <Card>
      <CardHeader>
        <CardTitle render={<h3 />}>Installations</CardTitle>
        <CardDescription>budget read from /rate_limit at each installation's latest run</CardDescription>
      </CardHeader>
      <CardContent className="px-0">
        {list.isError ? (
          <ErrorState error={list.error} />
        ) : (
          <DataTable columns={installationColumns} data={list.items} isLoading={list.isPending} label="Installations" bare sortable={false} getRowId={(row) => String(row.id)} empty={<InlineEmpty>No installations.</InlineEmpty>} />
        )}
      </CardContent>
    </Card>
  );
}
