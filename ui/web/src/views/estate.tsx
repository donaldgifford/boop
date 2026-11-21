import { BarBuckets, bucketsTable } from "@donaldgifford/design-system/blocks/bar-buckets";
import { BarList, barListTable } from "@donaldgifford/design-system/blocks/bar-list";
import { ChartCard } from "@donaldgifford/design-system/blocks/chart-card";
import { DataTable, dataTableColumns } from "@donaldgifford/design-system/blocks/data-table";
import { Kpi } from "@donaldgifford/design-system/blocks/kpi";
import { Meter, type MeterTone } from "@donaldgifford/design-system/blocks/meter";
import { PageHeader } from "@donaldgifford/design-system/blocks/page-header";
import type { StackSegment } from "@donaldgifford/design-system/blocks/stacked-bars";
import { TrendChart, trendTable } from "@donaldgifford/design-system/blocks/trend-chart";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@donaldgifford/design-system/components/card";

import { StackedBarsOverTime, stackTimeTable } from "../candidates/StackedBarsOverTime";
import { Endpoint, PageControls, useScope } from "../components/PageControls";
import { ageBuckets, ecosystems, estate, installations, openPullsSeries, runsByDay, topPackages } from "../fixtures";

const OUTCOMES: readonly StackSegment[] = [
  { key: "succeeded", label: "Succeeded", color: "var(--success)" },
  { key: "warned", label: "Warnings", color: "var(--warning)" },
  { key: "failed", label: "Failed", color: "var(--destructive)" },
];

// spendTone reads a budget the other way round from the package's Meter,
// whose default thresholds treat a high percentage as good (compliance).
// For spend, high is bad: a prop gap to raise upstream (INV-0005 finding 8).
function spendTone(percent: number): MeterTone {
  return percent >= 90 ? "destructive" : percent >= 70 ? "warning" : "muted";
}

type Installation = (typeof installations)[number];
const helper = dataTableColumns<Installation>();
const installationColumns = helper.columns([
  helper.accessor("owner", { header: "Owner", cell: ({ getValue }) => <span className="font-mono text-xs">{getValue()}</span> }),
  helper.accessor("repositories", { header: "Repositories", meta: { numeric: true } }),
  helper.accessor("corePercent", { header: "REST budget used", cell: ({ getValue, row }) => <Meter percent={getValue()} tone={spendTone(getValue())} label={`REST budget used by ${row.original.owner}`} /> }),
  helper.accessor("graphqlPercent", { header: "GraphQL budget used", cell: ({ getValue, row }) => <Meter percent={getValue()} tone={spendTone(getValue())} label={`GraphQL budget used by ${row.original.owner}`} /> }),
  helper.accessor("concurrent", { header: "Runs now / cap", meta: { numeric: true } }),
]);

export function EstateView() {
  const { range } = useScope();
  const days = Number.parseInt(range, 10);
  return (
    <>
      <PageHeader title="Estate" description={<Endpoint path="/api/v1/estate" />} actions={<PageControls />} />

      <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">
        <Kpi label="Repositories" value={estate.repositories} hint="onboarded" />
        <Kpi label="Open pull requests" value={estate.openPulls} hint="+12 in 7 days" />
        <Kpi label="Waiting updates" value={estate.waiting} hint="on 31 dashboards" tone="warning" />
        <Kpi label="Oldest open" value={`${estate.oldestOpenDays} d`} hint="proxmox-go-sdk #88" tone="warning" />
        <Kpi label="Runs, 24h" value={`${estate.successRate24h} %`} hint={`${estate.runs24h} runs`} tone="success" />
        <Kpi label="Lost deliveries" value={estate.lostDeliveries} hint="in 24 hours" />
      </div>

      <div className="grid gap-3 lg:grid-cols-2">
        <ChartCard title="Open pull requests" description={`daily, last ${days} days`} table={trendTable(openPullsSeries, "Open", { unit: "count", head: "Day", noun: "days" })}>
          <TrendChart points={openPullsSeries} unit="count" rangeDays={30} series="Open" label="Open pull requests per day" />
        </ChartCard>
        <ChartCard title="Pull request age" description="open pull requests by age" table={bucketsTable(ageBuckets, "Open")}>
          <BarBuckets buckets={ageBuckets} series="Open" label="Open pull requests by age" />
        </ChartCard>
        <ChartCard title="Runs by outcome" description="per day · candidate block" table={stackTimeTable(runsByDay, OUTCOMES)}>
          <StackedBarsOverTime points={runsByDay} segments={OUTCOMES} label="Runs per day by outcome" />
        </ChartCard>
        <ChartCard title="Top pending packages" description="repositories behind, by package" table={barListTable(topPackages, "Package")}>
          <BarList items={topPackages} label="Repositories behind, by package" />
        </ChartCard>
      </div>

      <div className="grid gap-3 lg:grid-cols-[1fr_2fr]">
        <ChartCard title="Pull requests by ecosystem" description="open, by manager" table={barListTable(ecosystems, "Manager")}>
          <BarList items={ecosystems} share label="Open pull requests by manager" />
        </ChartCard>
        <Card>
          <CardHeader>
            <CardTitle render={<h3 />}>Installations</CardTitle>
            <CardDescription>budget read from /rate_limit at the latest run</CardDescription>
          </CardHeader>
          <CardContent className="px-0">
            <DataTable columns={installationColumns} data={installations} label="Installations" bare sortable={false} getRowId={(row) => row.owner} />
          </CardContent>
        </Card>
      </div>
    </>
  );
}
