// The controls on the right of every page header: the owner scope and the
// range. Both live in the URL, so a link carries them (DESIGN-0004).
import { Button } from "@donaldgifford/design-system/components/button";
import { Popover, PopoverContent, PopoverTrigger } from "@donaldgifford/design-system/components/popover";
import { IconChevronDown } from "@tabler/icons-react";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { useState } from "react";

import { OptionHeading, OptionItem, OptionSeparator } from "@donaldgifford/design-system/blocks/option-rows";
import { SegmentedControl } from "@donaldgifford/design-system/blocks/segmented-control";
import { useMe } from "../api/queries";

export const RANGES = ["1d", "7d", "30d", "90d"] as const;
export type Range = (typeof RANGES)[number];

export interface ScopeSearch {
  owner?: string | undefined;
  range?: Range | undefined;
}

export function parseScopeSearch(search: Record<string, unknown>): ScopeSearch {
  const out: ScopeSearch = {};
  if (typeof search.owner === "string" && search.owner !== "") out.owner = search.owner;
  if (typeof search.range === "string" && (RANGES as readonly string[]).includes(search.range)) out.range = search.range as Range;
  return out;
}

export function useScope(): { owner: string | undefined; range: Range } {
  const search = useSearch({ strict: false }) as ScopeSearch;
  return { owner: search.owner, range: search.range ?? "30d" };
}

export function OwnerScope() {
  const { owner } = useScope();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const me = useMe().data;
  const owners = me?.owners ?? [];
  const choose = (next: string | undefined) => {
    setOpen(false);
    void navigate({ to: ".", search: (prev: ScopeSearch) => ({ ...prev, owner: next }) });
  };
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger render={<Button variant="outline" size="sm" />}>
        <span className="text-muted-foreground">owner</span>
        <span className="font-mono">{owner ?? "all"}</span>
        <IconChevronDown aria-hidden="true" className="size-3.5 text-muted-foreground" />
      </PopoverTrigger>
      <PopoverContent align="end" className="w-64 gap-0 p-1">
        <OptionHeading>Owner scope</OptionHeading>
        <OptionItem current={owner === undefined} detail={me?.all_owners ? "every owner" : `${owners.length} visible`} onClick={() => choose(undefined)}>
          All visible owners
        </OptionItem>
        {owners.length > 0 ? <OptionSeparator /> : null}
        {owners.map((o) => (
          <OptionItem key={o} current={o === owner} onClick={() => choose(o)}>
            <span className="font-mono">{o}</span>
          </OptionItem>
        ))}
      </PopoverContent>
    </Popover>
  );
}

export function RangeControl() {
  const { range } = useScope();
  const navigate = useNavigate();
  return (
    <SegmentedControl
      label="Range"
      value={range}
      options={RANGES.map((r) => ({ value: r, label: r }))}
      onValueChange={(next) => void navigate({ to: ".", search: (prev: ScopeSearch) => ({ ...prev, range: next }) })}
    />
  );
}

export function PageControls({ range = true }: { range?: boolean }) {
  return (
    <>
      <OwnerScope />
      {range ? <RangeControl /> : null}
    </>
  );
}

export function Endpoint({ path }: { path: string }) {
  return <span className="font-mono text-xs">{path}</span>;
}
