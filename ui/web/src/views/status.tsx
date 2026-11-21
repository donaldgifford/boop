import { StatStrip } from "@donaldgifford/design-system/blocks/stat-strip";
import { StatusBanner, StatusComponents } from "@donaldgifford/design-system/blocks/status";
import { Time } from "@donaldgifford/design-system/blocks/time";
import { Changelog } from "@donaldgifford/design-system/blocks/timeline";
import { Button } from "@donaldgifford/design-system/components/button";
import { Card, CardContent, CardHeader, CardTitle } from "@donaldgifford/design-system/components/card";
import { StatusPageLayout } from "@donaldgifford/design-system/shell/status-page";
import { IconRefresh } from "@tabler/icons-react";
import { Link } from "@tanstack/react-router";

import { Endpoint } from "../components/PageControls";
import { estate, status } from "../fixtures";

// StatusView is the public status page, outside the shell: the package's
// status parts over the aggregate-only /status payload. Nothing here names
// an owner, a repository or a package.
export function StatusView() {
  return (
    <StatusPageLayout
      title="boop status"
      mark={<IconRefresh aria-hidden="true" />}
      description={
        <>
          updated <Time iso={status.updatedAt} style="relative" /> · refreshes every 30s
        </>
      }
      actions={
        <Button variant="outline" size="sm" nativeButton={false} render={<Link to="/" />}>
          Sign in
        </Button>
      }
      footer={<Endpoint path="/api/v1/status" />}
    >
      <StatusBanner state={status.state} note="Public and aggregate-only. No owner, repository or package names appear here." />
      <Card className="gap-0 py-0">
        <CardHeader className="px-3.5 py-2.5">
          <CardTitle>Components</CardTitle>
        </CardHeader>
        <StatusComponents items={status.components} note="github_budget is the share of installations below their reserve." />
      </Card>
      <Card className="gap-0 py-0">
        <StatStrip
          className="border-y-0"
          items={[
            { label: "Repositories", value: estate.repositories.toLocaleString("en-US") },
            { label: "Open pull requests", value: estate.openPulls.toLocaleString("en-US") },
            { label: "Runs in 24h", value: estate.runs24h.toLocaleString("en-US") },
            { label: "Version", value: status.version },
          ]}
        />
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Changelog</CardTitle>
        </CardHeader>
        <CardContent>
          <Changelog
            items={status.releases.map((r) => ({
              id: r.id,
              date: r.date,
              title: r.title,
              detail: r.detail,
              ...(r.running ? { tags: [{ label: "running", variant: "success" as const }] } : {}),
            }))}
          />
        </CardContent>
      </Card>
    </StatusPageLayout>
  );
}
