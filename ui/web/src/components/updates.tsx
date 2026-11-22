import type { Schemas } from "../api/client";
import { UpdateTypeBadge } from "./badges";

// UpdateRow is one branch update: package, from → to, type and purl.
// boop's own (DESIGN-0004 § Independence): it is about Renovate updates.
export function UpdateRow({ update }: { update: Schemas["Update"] }) {
  return (
    <li className="rounded-lg border px-3 py-2">
      <div className="flex items-center justify-between gap-2">
        <span className="font-mono text-xs">{update.package_name}</span>
        <UpdateTypeBadge type={update.update_type} />
      </div>
      <div className="mt-1 font-mono text-xs">
        <span className="text-muted-foreground">{update.from_version} →</span> {update.to_version}
      </div>
      {update.purl_to ? <div className="mt-1 truncate font-mono text-label text-muted-foreground">{update.purl_to}</div> : null}
    </li>
  );
}

// UpdateSummary is a pull request's first update on one line, with a
// count of the rest.
export function UpdateSummary({ updates, fallback }: { updates: readonly Schemas["Update"][]; fallback: string }) {
  const [first, ...rest] = updates;
  if (!first) return <>{fallback}</>;
  return (
    <span className="font-mono text-xs">
      {first.package_name} <span className="text-muted-foreground">{first.from_version} →</span> {first.to_version}
      {rest.length > 0 ? <span className="text-muted-foreground"> +{rest.length}</span> : null}
    </span>
  );
}
