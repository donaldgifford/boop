// Candidate for the design system (INV-0005 finding 8): the package's Tabs
// list used as a control with no panels. The router binding stays in the
// app; this takes a value and reports a change.
import { Tabs, TabsList, TabsTrigger } from "@donaldgifford/design-system/components/tabs";
import type { ReactNode } from "react";

export interface SegmentOption<V extends string> {
  value: V;
  label: ReactNode;
}

interface SegmentedControlProps<V extends string> {
  options: readonly SegmentOption<V>[];
  value: V;
  onValueChange: (value: V) => void;
  /** The accessible name: what the control chooses. */
  label: string;
  className?: string | undefined;
}

export function SegmentedControl<V extends string>({ options, value, onValueChange, label, className }: SegmentedControlProps<V>) {
  return (
    <Tabs value={value} onValueChange={(next) => onValueChange(next as V)} className={className}>
      <TabsList aria-label={label}>
        {options.map((option) => (
          <TabsTrigger key={option.value} value={option.value}>
            {option.label}
          </TabsTrigger>
        ))}
      </TabsList>
    </Tabs>
  );
}
