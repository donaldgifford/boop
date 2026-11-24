import { DetailsGrid } from "@donaldgifford/design-system/blocks/details-grid";
import { dataTableColumns } from "@donaldgifford/design-system/blocks/data-table";
import { ErrorState } from "@donaldgifford/design-system/blocks/error-state";
import { InlineEmpty, InlineLoading } from "@donaldgifford/design-system/blocks/inline-state";
import { LoadMore } from "@donaldgifford/design-system/blocks/load-more";
import { PageHeader } from "@donaldgifford/design-system/blocks/page-header";
import { StatStrip } from "@donaldgifford/design-system/blocks/stat-strip";
import { Time } from "@donaldgifford/design-system/blocks/time";
import { Button } from "@donaldgifford/design-system/components/button";
import { Card, CardContent } from "@donaldgifford/design-system/components/card";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@donaldgifford/design-system/components/tabs";
import { IconExternalLink, IconRefresh } from "@tabler/icons-react";
import { Link, useNavigate, useParams, useSearch } from "@tanstack/react-router";

import type { Schemas } from "../api/client";
import {
  inProgress,
  useDashboardItems,
  useMe,
  usePullRequests,
  useRecheck,
  useRepository,
  useRepositoryDependencies,
  useRepositoryEvents,
  useRepositoryRuns,
} from "../api/queries";
import { StateBadge } from "../components/badges";
import { EventTimeline } from "../components/events";
import { ListCard } from "../components/ListCard";
import { Endpoint, parseScopeSearch, type ScopeSearch } from "../components/PageControls";
import { repositoryRunColumns } from "../components/runs";
import { dashboardURL, WaitingSections } from "../components/waiting";
import { prId, repositoryPullColumns } from "./pulls";

const TABS = ["timeline", "runs", "pulls", "waiting", "dependencies", "config"] as const;
type Tab = (typeof TABS)[number];

export interface RepositorySearch extends ScopeSearch {
  tab?: Tab | undefined;
}

export function parseRepositorySearch(search: Record<string, unknown>): RepositorySearch {
  const out: RepositorySearch = parseScopeSearch(search);
  const tab = search.tab;
  if (typeof tab === "string" && (TABS as readonly string[]).includes(tab) && tab !== "timeline") out.tab = tab as Tab;
  return out;
}

export function RepositoryView() {
  const { id: raw } = useParams({ strict: false }) as { id: string };
  const id = Number(raw);
  const repo = useRepository(id);
  if (repo.isError) return <ErrorState error={repo.error} onRetry={() => void repo.refetch()} />;
  if (!repo.data) return <InlineLoading />;
  return <Repository repo={repo.data} />;
}

function Repository({ repo }: { repo: Schemas["RepositoryDetail"] }) {
  const search = useSearch({ strict: false }) as RepositorySearch;
  const navigate = useNavigate();
  const live = inProgress(repo);
  const tab = search.tab ?? "timeline";
  return (
    <>
      <PageHeader
        title={
          <span className="flex items-center gap-2">
            <span className="font-mono">{repo.slug}</span>
            <StateBadge state={repo.state} />
          </span>
        }
        description={<Endpoint path={`/api/v1/repositories/${repo.id}`} />}
        actions={
          <span className="flex items-center gap-2">
            <DashboardButton repo={repo} />
            <RecheckButton id={repo.id} />
          </span>
        }
      />
      <Card className="py-0">
        <StatStrip
          items={[
            { label: "Live phase", value: repo.live ? (repo.phase ?? "—") : "unknown", sub: phaseSub(repo) },
            { label: "Profile", value: repo.profile },
            { label: "Last run", value: repo.last_outcome ?? "—", sub: repo.last_run_at ? <Time iso={repo.last_run_at} /> : undefined },
            // Relative, with the time under it: a date and time is wider
            // than a stat's value has room for.
            { label: "Next due", value: repo.next_due ? <Time iso={repo.next_due} style="relative" /> : "—", sub: repo.next_due ? <Time iso={repo.next_due} /> : undefined },
            { label: "Open pull requests", value: repo.open_prs },
            { label: "Waiting", value: repo.waiting },
          ]}
        />
      </Card>
      <Tabs value={tab} onValueChange={(next) => void navigate({ to: ".", search: (prev: RepositorySearch) => ({ ...prev, tab: next === "timeline" ? undefined : (next as Tab) }) })}>
        <TabsList variant="line">
          <TabsTrigger value="timeline">Timeline</TabsTrigger>
          <TabsTrigger value="runs">Runs</TabsTrigger>
          <TabsTrigger value="pulls">Pull requests</TabsTrigger>
          <TabsTrigger value="waiting">Waiting</TabsTrigger>
          <TabsTrigger value="dependencies">Dependencies</TabsTrigger>
          <TabsTrigger value="config">Config</TabsTrigger>
        </TabsList>
        <TabsContent value="timeline">
          <TimelineTab id={repo.id} live={live} />
        </TabsContent>
        <TabsContent value="runs">
          <RunsTab id={repo.id} live={live} />
        </TabsContent>
        <TabsContent value="pulls">
          <PullsTab id={repo.id} />
        </TabsContent>
        <TabsContent value="waiting">
          <WaitingTab id={repo.id} />
        </TabsContent>
        <TabsContent value="dependencies">
          <DependenciesTab id={repo.id} />
        </TabsContent>
        <TabsContent value="config">
          <ConfigTab repo={repo} />
        </TabsContent>
      </Tabs>
    </>
  );
}

function phaseSub(repo: Schemas["RepositoryDetail"]) {
  if (!repo.live) return "Temporal did not answer";
  if (repo.running_since) return <>since <Time iso={repo.running_since} /></>;
  if (repo.budget_wait_until) return <>until <Time iso={repo.budget_wait_until} /></>;
  return undefined;
}

// DashboardButton opens the repository's Dependency Dashboard on GitHub,
// for everyone who can see the repository.
function DashboardButton({ repo }: { repo: Schemas["RepositoryDetail"] }) {
  return (
    <Button
      variant="outline"
      size="sm"
      nativeButton={false}
      render={<a href={dashboardURL(repo.slug, repo.dashboard_url)} target="_blank" rel="noopener noreferrer" />}
    >
      <IconExternalLink aria-hidden="true" className="size-3.5" />
      Dashboard
    </Button>
  );
}

// RecheckButton is for operators only; a viewer never sees it (the API
// would answer 403 anyway).
function RecheckButton({ id }: { id: number }) {
  const me = useMe();
  const recheck = useRecheck(id);
  if (!me.data?.operator) return null;
  return (
    <span className="flex items-center gap-2">
      <span role="status" className="text-xs text-muted-foreground">
        {recheck.isSuccess ? (recheck.data.deduplicated ? "Already requested in the last minute" : "Recheck requested") : null}
        {recheck.isError ? "Recheck failed" : null}
      </span>
      <Button variant="outline" size="sm" disabled={recheck.isPending} onClick={() => recheck.mutate()}>
        <IconRefresh aria-hidden="true" className="size-3.5" />
        Recheck
      </Button>
    </span>
  );
}

function TimelineTab({ id, live }: { id: number; live: boolean }) {
  const list = useRepositoryEvents(id, live);
  if (list.isError) return <ErrorState error={list.error} onRetry={() => void list.refetch()} />;
  return (
    <Card>
      <CardContent>
        {list.isPending ? (
          <InlineLoading />
        ) : list.items.length === 0 ? (
          <InlineEmpty>No events yet.</InlineEmpty>
        ) : (
          <>
            <EventTimeline events={list.items} />
            <LoadMore loaded={list.items.length} hasMore={list.hasNextPage} loading={list.isFetchingNextPage} onMore={() => void list.fetchNextPage()} noun="events" />
          </>
        )}
      </CardContent>
    </Card>
  );
}

function RunsTab({ id, live }: { id: number; live: boolean }) {
  const list = useRepositoryRuns(id, live);
  return <ListCard list={list} columns={repositoryRunColumns} label="Runs, newest first" noun="runs" getRowId={(r) => r.id} empty={<InlineEmpty>No runs yet.</InlineEmpty>} />;
}

function PullsTab({ id }: { id: number }) {
  const list = usePullRequests({ repo: id, state: "open" });
  const navigate = useNavigate();
  return (
    <ListCard
      list={list}
      columns={repositoryPullColumns}
      label="Open pull requests, oldest first"
      noun="pull requests"
      getRowId={prId}
      onRowClick={(pr) => void navigate({ to: "/pulls", search: { open: prId(pr) } })}
      empty={<InlineEmpty>No open pull requests.</InlineEmpty>}
    />
  );
}

function WaitingTab({ id }: { id: number }) {
  return <WaitingSections list={useDashboardItems({ repo: id })} showRepo={false} />;
}

const depHelper = dataTableColumns<Schemas["Dependency"]>();
const dependencyColumns = depHelper.columns([
  depHelper.accessor("package_name", { header: "Package", cell: ({ getValue }) => <span className="font-mono text-xs">{getValue()}</span> }),
  depHelper.accessor("current_version", {
    header: "Version",
    cell: ({ row }) => <span className="font-mono text-xs">{row.original.current_version || row.original.current_value}</span>,
  }),
  depHelper.accessor("manager", { header: "Manager", cell: ({ getValue }) => <span className="font-mono text-xs">{getValue()}</span> }),
  depHelper.accessor("package_file", { header: "File", cell: ({ getValue }) => <span className="font-mono text-xs">{getValue()}</span>, meta: { hideBelow: "lg" } }),
  depHelper.accessor("purl", {
    header: "Package URL",
    cell: ({ getValue }) => {
      const purl = getValue();
      return purl ? (
        <Link to="/packages/$purl" params={{ purl }} className="font-mono text-xs underline-offset-2 hover:underline">
          {purl}
        </Link>
      ) : (
        <span className="text-muted-foreground">—</span>
      );
    },
    meta: { hideBelow: "compact" },
  }),
]);

function DependenciesTab({ id }: { id: number }) {
  const list = useRepositoryDependencies(id);
  return (
    <ListCard
      list={list}
      columns={dependencyColumns}
      label="Dependencies from the last report"
      noun="dependencies"
      getRowId={(d) => `${d.package_file}:${d.manager}:${d.package_name}`}
      empty={<InlineEmpty>No report yet.</InlineEmpty>}
    />
  );
}

function ConfigTab({ repo }: { repo: Schemas["RepositoryDetail"] }) {
  const at = (iso: string | null | undefined) => (iso ? <Time iso={iso} /> : "—");
  return (
    <Card>
      <CardContent>
        <DetailsGrid
          columns={2}
          items={[
            { label: "Profile", value: repo.profile },
            { label: "State", value: <StateBadge state={repo.state} /> },
            { label: "Repository ID", value: <span className="font-mono text-xs">{repo.id}</span> },
            { label: "Installation", value: <span className="font-mono text-xs">{repo.installation_id}</span> },
            { label: "Onboarded", value: at(repo.onboarded_at) },
            { label: "Offboarded", value: at(repo.offboarded_at) },
            { label: "Last run", value: repo.last_run_id ? <span className="font-mono text-xs">{repo.last_run_id}</span> : "—" },
            { label: "Workflow", value: <span className="font-mono text-xs">repo/github/{repo.id}</span> },
          ]}
        />
      </CardContent>
    </Card>
  );
}
