import { dataTableColumns } from "@donaldgifford/design-system/blocks/data-table";
import { EmptyState } from "@donaldgifford/design-system/blocks/empty-state";
import { InlineLoading } from "@donaldgifford/design-system/blocks/inline-state";
import { PageHeader } from "@donaldgifford/design-system/blocks/page-header";
import { SegmentedControl } from "@donaldgifford/design-system/blocks/segmented-control";
import { Time } from "@donaldgifford/design-system/blocks/time";
import { Badge } from "@donaldgifford/design-system/components/badge";
import { IconLock } from "@tabler/icons-react";
import { useSearch } from "@tanstack/react-router";

import type { Schemas } from "../api/client";
import { useDeliveries, useMe } from "../api/queries";
import { StateBadge } from "../components/badges";
import { ListCard } from "../components/ListCard";
import { ALL, allOr, flag, oneOf, useSearchPatch } from "../components/search";

const OUTCOMES = ["routed", "ignored", "own", "rejected", "failed"] as const;

export interface DeliveriesSearch {
  outcome?: string | undefined;
  missed?: boolean | undefined;
}

export function parseDeliveriesSearch(s: Record<string, unknown>): DeliveriesSearch {
  return { outcome: oneOf(s.outcome, OUTCOMES), missed: flag(s.missed) };
}

const helper = dataTableColumns<Schemas["Delivery"]>();
const columns = helper.columns([
  helper.accessor("received_at", { header: "Received", cell: ({ getValue }) => (getValue() ? <Time iso={getValue() ?? ""} /> : "—") }),
  helper.accessor("event", {
    header: "Event",
    cell: ({ row }) => (
      <span className="font-mono text-xs">
        {row.original.event}
        {row.original.action ? `.${row.original.action}` : ""}
      </span>
    ),
  }),
  helper.accessor("outcome", { header: "Outcome", cell: ({ getValue }) => (getValue() ? <StateBadge state={getValue()} /> : <span className="text-muted-foreground">no record</span>) }),
  helper.accessor("github_status", { header: "GitHub status", cell: ({ getValue }) => getValue() ?? "—", meta: { numeric: true, hideBelow: "compact" } }),
  helper.accessor("missed", { header: "Missed", cell: ({ getValue }) => (getValue() ? <Badge variant="destructive">missed</Badge> : null) }),
  helper.accessor("delivery_id", { header: "Delivery", cell: ({ row }) => <span className="font-mono text-xs">{row.original.delivery_id.slice(0, 8)} #{row.original.attempt}</span>, meta: { hideBelow: "lg" } }),
]);

// DeliveriesView is the webhook delivery audit. Deliveries are not
// owner-scoped, so the API answers 403 unless the caller sees every
// owner; the page says so rather than asking.
export function DeliveriesView() {
  const me = useMe();
  const search = useSearch({ strict: false }) as DeliveriesSearch;
  const patch = useSearchPatch<DeliveriesSearch>();
  const allowed = me.data?.all_owners === true;
  const list = useDeliveries({ outcome: search.outcome, missed: search.missed }, allowed);
  return (
    <>
      <PageHeader title="Deliveries" description="Webhook deliveries: what ingest did and what GitHub recorded" />
      {me.isPending ? (
        <InlineLoading />
      ) : !allowed ? (
        <EmptyState icon={IconLock} title="Deliveries need every owner" description="Webhook deliveries span installations, so only a group that sees all owners can read them." />
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-3">
            <SegmentedControl
              label="Outcome"
              value={search.outcome ?? ALL}
              options={[{ value: ALL, label: "All" }, ...OUTCOMES.map((o) => ({ value: o, label: o }))]}
              onValueChange={(v) => patch({ outcome: allOr(v) })}
            />
            <SegmentedControl
              label="Missed"
              value={search.missed ? "true" : ALL}
              options={[
                { value: ALL, label: "All" },
                { value: "true", label: "Missed only" },
              ]}
              onValueChange={(v) => patch({ missed: v === "true" ? true : undefined })}
            />
          </div>
          <ListCard list={list} columns={columns} label="Deliveries, newest first" noun="deliveries" getRowId={(d) => `${d.delivery_id}:${d.attempt}`} />
        </>
      )}
    </>
  );
}
