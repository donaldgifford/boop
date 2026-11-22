import { dataTableColumns } from "@donaldgifford/design-system/blocks/data-table";
import { Time } from "@donaldgifford/design-system/blocks/time";
import { Link } from "@tanstack/react-router";

import type { Schemas } from "../api/client";
import { duration } from "../lib/format";
import { StateBadge } from "./badges";

type Run = Schemas["Run"];

// coreSpent is the REST budget a run used, when it reported one.
export function coreSpent(r: Run): number | undefined {
  const s = r.spend?.core;
  return s ? s.before - s.after : undefined;
}

const helper = dataTableColumns<Run>();

const common = [
  helper.accessor("started_at", { header: "Started", cell: ({ getValue }) => <Time iso={getValue()} /> }),
  helper.accessor("outcome", { header: "Outcome", cell: ({ getValue }) => <StateBadge state={getValue()} /> }),
  helper.accessor("cause", {
    header: "Cause",
    cell: ({ row }) => (
      <span className="font-mono text-xs" title={row.original.cause_key ?? undefined}>
        {row.original.cause}
      </span>
    ),
  }),
  helper.accessor("duration_ms", { header: "Duration", cell: ({ getValue }) => duration(getValue()), meta: { numeric: true } }),
  helper.accessor("updates", { header: "Updates", meta: { numeric: true } }),
  helper.accessor(coreSpent, { id: "core", header: "REST spent", cell: ({ getValue }) => getValue() ?? "—", meta: { numeric: true, hideBelow: "lg" } }),
];

// repositoryRunColumns are a repository's runs; runColumns add the
// repository for the estate-wide list.
export const repositoryRunColumns = helper.columns([
  helper.accessor("id", { header: "Run", cell: ({ getValue }) => <span className="font-mono text-xs">{getValue()}</span>, meta: { hideBelow: "lg" } }),
  ...common,
]);

export const runColumns = helper.columns([
  helper.accessor("slug", {
    header: "Repository",
    cell: ({ row }) => (
      <Link to="/repositories/$id" params={{ id: String(row.original.repo_id) }} className="font-mono text-xs underline-offset-2 hover:underline">
        {row.original.slug}
      </Link>
    ),
  }),
  ...common,
]);
