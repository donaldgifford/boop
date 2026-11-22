import { ErrorState } from "@donaldgifford/design-system/blocks/error-state";
import { InlineLoading } from "@donaldgifford/design-system/blocks/inline-state";
import { StatStrip } from "@donaldgifford/design-system/blocks/stat-strip";
import { type StatusComponent, StatusBanner, StatusComponents } from "@donaldgifford/design-system/blocks/status";
import { Time } from "@donaldgifford/design-system/blocks/time";
import { Button } from "@donaldgifford/design-system/components/button";
import { Card, CardHeader, CardTitle } from "@donaldgifford/design-system/components/card";
import { StatusPageLayout } from "@donaldgifford/design-system/shell/status-page";
import { IconRefresh } from "@tabler/icons-react";
import { Link } from "@tanstack/react-router";

import type { Schemas } from "../api/client";
import { useStatus } from "../api/queries";
import { Endpoint } from "../components/PageControls";
import { count, duration } from "../lib/format";

// componentDetail is the one line under a component: the numbers its
// kind reports.
export function componentDetail(c: Schemas["StatusComponent"]): string | undefined {
  const parts: string[] = [];
  if (c.latency_ms != null) parts.push(`${c.latency_ms} ms`);
  if (c.pollers != null) parts.push(`${c.pollers} pollers`);
  if (c.age_seconds != null) parts.push(`last ${duration(c.age_seconds * 1000)} ago`);
  if (c.total != null) parts.push(`${c.failed ?? 0} failed, ${c.missed ?? 0} missed of ${c.total}`);
  return parts.length > 0 ? parts.join(" · ") : undefined;
}

// StatusView is the public status page, outside the shell: the package's
// status parts over the aggregate-only /status payload, refetched every
// 30 s. Nothing here names an owner, a repository or a package.
export function StatusView() {
  const status = useStatus();
  const s = status.data;
  return (
    <StatusPageLayout
      title="boop status"
      mark={<IconRefresh aria-hidden="true" />}
      description={s ? <>updated <Time iso={s.updated_at} style="relative" /> · refreshes every 30s</> : "refreshes every 30s"}
      actions={
        <Button variant="outline" size="sm" nativeButton={false} render={<Link to="/" />}>
          Sign in
        </Button>
      }
      footer={<Endpoint path="/api/v1/status" />}
    >
      {status.isError ? (
        <ErrorState error={status.error} onRetry={() => void status.refetch()} />
      ) : !s ? (
        <InlineLoading />
      ) : (
        <>
          <StatusBanner state={s.state} note="Public and aggregate-only. No owner, repository or package names appear here." />
          <Card className="gap-0 py-0">
            <CardHeader className="px-3.5 py-2.5">
              <CardTitle>Components</CardTitle>
            </CardHeader>
            <StatusComponents
              items={s.components.map((c): StatusComponent => ({ name: c.name, state: c.state, detail: componentDetail(c) }))}
              note="github_budget is the share of installations below their reserve."
            />
          </Card>
          <Card className="gap-0 py-0">
            <StatStrip
              className="border-y-0"
              items={[
                { label: "Repositories", value: count(s.fleet.repositories) },
                { label: "Open pull requests", value: count(s.fleet.open_prs) },
                { label: "Runs in 24h", value: count(s.fleet.runs_24h) },
                { label: "Version", value: s.version },
              ]}
            />
          </Card>
        </>
      )}
    </StatusPageLayout>
  );
}
