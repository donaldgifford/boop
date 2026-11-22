import { dataTableColumns } from "@donaldgifford/design-system/blocks/data-table";
import { InlineEmpty } from "@donaldgifford/design-system/blocks/inline-state";
import { PageHeader } from "@donaldgifford/design-system/blocks/page-header";
import { Card, CardHeader, CardTitle } from "@donaldgifford/design-system/components/card";
import { Link, useNavigate, useParams, useSearch } from "@tanstack/react-router";

import type { Schemas } from "../api/client";
import { useDependencies, usePackages, useUpdates } from "../api/queries";
import { UpdateTypeBadge } from "../components/badges";
import { ListCard } from "../components/ListCard";
import { PageControls, type ScopeSearch } from "../components/PageControls";

const versions = (v: readonly string[]) => (v.length > 3 ? `${v.slice(0, 3).join(", ")} +${v.length - 3}` : v.join(", "));

const helper = dataTableColumns<Schemas["Package"]>();
const columns = helper.columns([
  helper.accessor("purl", { header: "Package", cell: ({ getValue }) => <span className="font-mono text-xs">{getValue()}</span> }),
  helper.accessor("managers", { header: "Managers", cell: ({ getValue }) => <span className="font-mono text-xs">{getValue().join(", ")}</span>, meta: { hideBelow: "lg" } }),
  helper.accessor("from_versions", {
    header: "From → to",
    cell: ({ row }) => (
      <span className="font-mono text-xs">
        {versions(row.original.from_versions)} → {versions(row.original.to_versions)}
      </span>
    ),
    meta: { hideBelow: "compact" },
  }),
  helper.accessor("repositories", { header: "Repositories", meta: { numeric: true } }),
  helper.accessor("updates", { header: "Updates", meta: { numeric: true } }),
]);

// PackagesView is every package with a pending update in scope, most
// repositories first.
export function PackagesView() {
  const search = useSearch({ strict: false }) as ScopeSearch;
  const navigate = useNavigate();
  const list = usePackages({ owner: search.owner });
  return (
    <>
      <PageHeader title="Packages" description="Packages with pending updates, by how many repositories wait on them" actions={<PageControls range={false} />} />
      <ListCard
        list={list}
        columns={columns}
        label="Packages with pending updates"
        noun="packages"
        getRowId={(p) => p.purl}
        onRowClick={(p) => void navigate({ to: "/packages/$purl", params: { purl: p.purl } })}
      />
    </>
  );
}

const repoLink = (r: { repo_id: number; slug: string }) => (
  <Link to="/repositories/$id" params={{ id: String(r.repo_id) }} className="font-mono text-xs underline-offset-2 hover:underline">
    {r.slug}
  </Link>
);

const updateHelper = dataTableColumns<Schemas["Update"]>();
const updateColumns = updateHelper.columns([
  updateHelper.accessor("slug", { header: "Repository", cell: ({ row }) => repoLink(row.original) }),
  updateHelper.accessor("from_version", {
    header: "From → to",
    cell: ({ row }) => (
      <span className="font-mono text-xs">
        {row.original.from_version} → {row.original.to_version}
      </span>
    ),
  }),
  updateHelper.accessor("update_type", { header: "Type", cell: ({ getValue }) => <UpdateTypeBadge type={getValue()} /> }),
  updateHelper.accessor("pr_number", { header: "PR", cell: ({ getValue }) => (getValue() ? `#${getValue()}` : "—"), meta: { numeric: true } }),
]);

const depHelper = dataTableColumns<Schemas["Dependency"]>();
const depColumns = depHelper.columns([
  depHelper.accessor("slug", { header: "Repository", cell: ({ row }) => repoLink(row.original) }),
  depHelper.accessor("current_version", { header: "Version", cell: ({ row }) => <span className="font-mono text-xs">{row.original.current_version || row.original.current_value}</span> }),
  depHelper.accessor("package_file", { header: "File", cell: ({ getValue }) => <span className="font-mono text-xs">{getValue()}</span>, meta: { hideBelow: "compact" } }),
]);

// PackageView is one package URL: its pending updates and where it is
// used, both across the caller's visible owners.
export function PackageView() {
  const { purl } = useParams({ strict: false }) as { purl: string };
  const updates = useUpdates(purl);
  const deps = useDependencies(purl);
  return (
    <>
      <PageHeader title={<span className="font-mono">{purl}</span>} description="Pending updates and the repositories that use it" />
      <Card className="gap-0 py-0">
        <CardHeader className="py-3">
          <CardTitle>Pending updates</CardTitle>
        </CardHeader>
      </Card>
      <ListCard
        list={updates}
        columns={updateColumns}
        label={`Pending updates of ${purl}`}
        noun="updates"
        getRowId={(u) => `${u.repo_id}:${u.branch}:${u.package_file}`}
        empty={<InlineEmpty>No pending updates.</InlineEmpty>}
      />
      <Card className="gap-0 py-0">
        <CardHeader className="py-3">
          <CardTitle>Used by</CardTitle>
        </CardHeader>
      </Card>
      <ListCard
        list={deps}
        columns={depColumns}
        label={`Repositories using ${purl}`}
        noun="dependencies"
        getRowId={(d) => `${d.repo_id}:${d.package_file}:${d.package_name}`}
        empty={<InlineEmpty>No repository reports this package.</InlineEmpty>}
      />
    </>
  );
}
