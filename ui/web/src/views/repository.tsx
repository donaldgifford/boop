import { DataTable, dataTableColumns } from "@donaldgifford/design-system/blocks/data-table";
import { PageHeader } from "@donaldgifford/design-system/blocks/page-header";
import { StatStrip } from "@donaldgifford/design-system/blocks/stat-strip";
import { Timeline } from "@donaldgifford/design-system/blocks/timeline";
import { Button } from "@donaldgifford/design-system/components/button";
import { Card, CardContent } from "@donaldgifford/design-system/components/card";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@donaldgifford/design-system/components/tabs";
import { IconRefresh } from "@tabler/icons-react";

import { InlineEmpty, InlineLoading } from "@donaldgifford/design-system/blocks/inline-state";
import { StateBadge } from "../components/badges";
import { Endpoint } from "../components/PageControls";
import { type Run, repository, repositoryRuns, repositoryTimeline } from "../fixtures";
import { when } from "../lib/when";

const helper = dataTableColumns<Run>();
const runColumns = helper.columns([
  helper.accessor("id", { header: "Run", cell: ({ getValue }) => <span className="font-mono text-xs">{getValue()}</span> }),
  helper.accessor("startedAt", { header: "Started", cell: ({ getValue }) => <span className="font-mono text-xs">{when(getValue())}</span> }),
  helper.accessor("outcome", { header: "Outcome", cell: ({ getValue }) => <StateBadge state={getValue()} />, enableSorting: false }),
  helper.accessor("cause", { header: "Cause", cell: ({ getValue }) => <span className="font-mono text-xs">{getValue()}</span> }),
  helper.accessor("durationSeconds", { header: "Duration", cell: ({ getValue }) => `${Math.floor(getValue() / 60)}m ${String(getValue() % 60).padStart(2, "0")}s`, meta: { numeric: true } }),
  helper.accessor("updates", { header: "Updates", meta: { numeric: true } }),
  helper.accessor("coreSpent", { header: "REST spent", meta: { numeric: true, hideBelow: "lg" } }),
]);

export function RepositoryView() {
  return (
    <>
      <PageHeader
        title={
          <span className="flex items-center gap-2">
            <span className="font-mono">{repository.slug}</span>
            <StateBadge state={repository.state} />
          </span>
        }
        description={<Endpoint path={`/api/v1/repositories/${repository.id}`} />}
        actions={
          <Button variant="outline" size="sm">
            <IconRefresh aria-hidden="true" className="size-3.5" />
            Recheck
          </Button>
        }
      />
      <Card className="py-0">
        <StatStrip
          items={[
            { label: "Profile", value: repository.profile },
            { label: "Cadence", value: repository.cadence },
            { label: "Live phase", value: repository.phase },
            { label: "Next due", value: "in 11h", sub: when(repository.nextDue) },
            { label: "Open pull requests", value: repository.openPulls },
            { label: "Waiting", value: repository.waiting },
          ]}
        />
      </Card>
      <Tabs defaultValue="timeline">
        <TabsList variant="line">
          <TabsTrigger value="timeline">Timeline</TabsTrigger>
          <TabsTrigger value="runs">Runs</TabsTrigger>
          <TabsTrigger value="pulls">Pull requests</TabsTrigger>
          <TabsTrigger value="waiting">Waiting</TabsTrigger>
          <TabsTrigger value="dependencies">Dependencies</TabsTrigger>
        </TabsList>
        <TabsContent value="timeline">
          <Card>
            <CardContent>
              <Timeline
                items={repositoryTimeline.map((e) => ({ id: e.id, actor: e.actor, verb: e.verb, object: e.object, detail: e.detail, when: when(e.at), tone: e.tone, tags: [{ label: e.kind, variant: "outline" as const }] }))}
              />
            </CardContent>
          </Card>
        </TabsContent>
        <TabsContent value="runs">
          <Card className="py-0">
            <DataTable columns={runColumns} data={repositoryRuns} label="Runs" bare getRowId={(row) => row.id} />
          </Card>
        </TabsContent>
        <TabsContent value="pulls">
          <Card className="py-0">
            <InlineLoading />
          </Card>
        </TabsContent>
        <TabsContent value="waiting">
          <Card className="py-0">
            <InlineEmpty>Nothing is waiting on this dashboard.</InlineEmpty>
          </Card>
        </TabsContent>
        <TabsContent value="dependencies">
          <Card className="py-0">
            <InlineEmpty>Not in the mockup yet.</InlineEmpty>
          </Card>
        </TabsContent>
      </Tabs>
    </>
  );
}
