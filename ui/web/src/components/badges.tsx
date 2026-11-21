// boop's states on the design system's Badge variants (DESIGN-0004 § Visual
// design). Enums are open, so an unknown value gets `muted`.
import { Badge, type BadgeProps } from "@donaldgifford/design-system/components/badge";

type Variant = NonNullable<BadgeProps["variant"]>;

const VARIANTS: Readonly<Record<string, Variant>> = {
  merged: "success",
  succeeded: "success",
  operational: "success",
  passing: "success",
  open: "info",
  running: "running",
  waiting: "warning",
  warned: "warning",
  "rate-limited": "warning",
  stalled: "warning",
  degraded: "warning",
  pending: "pending",
  "pending-approval": "pending",
  errored: "destructive",
  failed: "destructive",
  failing: "destructive",
  missed: "destructive",
  down: "destructive",
  closed: "ignored",
  ignored: "ignored",
  idle: "muted",
};

export function stateVariant(state: string): Variant {
  return VARIANTS[state] ?? "muted";
}

export function StateBadge({ state }: { state: string }) {
  return <Badge variant={stateVariant(state)}>{state}</Badge>;
}

const UPDATE_VARIANTS: Readonly<Record<string, Variant>> = { major: "warning", minor: "info", patch: "muted", digest: "muted", pin: "muted" };

export function UpdateTypeBadge({ type }: { type: string }) {
  return <Badge variant={UPDATE_VARIANTS[type] ?? "muted"}>{type}</Badge>;
}
