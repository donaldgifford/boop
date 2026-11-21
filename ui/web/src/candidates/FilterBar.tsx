// Candidate for the design system (INV-0005 finding 8): the row around the
// package's FilterChip. The page owns the filters and each chip's editor;
// the bar lays them out and reports remove, add and clear.
import { FilterChip } from "@donaldgifford/design-system/blocks/filter-chip";
import { cn } from "@donaldgifford/design-system/lib/utils";
import type { ReactNode } from "react";

export interface ActiveFilter {
  name: string;
  value: string;
}

interface FilterBarProps {
  filters: readonly ActiveFilter[];
  onRemove: (name: string) => void;
  onAdd?: (() => void) | undefined;
  onClear?: (() => void) | undefined;
  /** The result count, at the row's end: "23 of 318". */
  count?: ReactNode | undefined;
  className?: string | undefined;
}

export function FilterBar({ filters, onRemove, onAdd, onClear, count, className }: FilterBarProps) {
  return (
    <div data-slot="filter-bar" className={cn("flex flex-wrap items-center gap-1.5", className)}>
      {filters.map((f) => (
        <FilterChip
          key={f.name}
          name={f.name}
          value={
            <>
              {f.value}{" "}
              <span aria-hidden="true" className="ml-0.5 opacity-70">
                ×
              </span>
            </>
          }
          onClick={() => onRemove(f.name)}
          aria-label={`Remove filter ${f.name} = ${f.value}`}
        />
      ))}
      {onAdd ? <FilterChip onClick={onAdd} /> : null}
      {onClear && filters.length > 0 ? (
        <button type="button" onClick={onClear} className="ml-1 text-muted-foreground text-xs hover:text-foreground">
          Clear
        </button>
      ) : null}
      {count === undefined ? null : <span className="ml-auto text-muted-foreground text-xs tabular-nums">{count}</span>}
    </div>
  );
}
