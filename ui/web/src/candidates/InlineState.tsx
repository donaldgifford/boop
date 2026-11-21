// Candidate for the design system (INV-0005 finding 8): one line of loading
// or empty text inside a panel or under a table head. The package's
// EmptyState is a page-level block, and its skeletons stand in for layout.
import { cn } from "@donaldgifford/design-system/lib/utils";
import type { ReactNode } from "react";

export function InlineLoading({ children = "Loading…", className }: { children?: ReactNode; className?: string }) {
  return (
    <p role="status" className={cn("flex items-center gap-2 px-3.5 py-2.5 text-muted-foreground text-xs", className)}>
      <span aria-hidden="true" className="size-1.5 animate-pulse rounded-pill bg-muted-foreground" />
      {children}
    </p>
  );
}

export function InlineEmpty({ children, className }: { children: ReactNode; className?: string }) {
  return <p className={cn("px-3.5 py-2.5 text-muted-foreground text-xs", className)}>{children}</p>;
}
