# Candidates for the design system

Drafts of generic components that boop needs and
`@donaldgifford/design-system` does not have yet (INV-0005 findings 8
and 9). They are built here so the mockup can show them in real pages.

None of them stays in boop. Each one moves to the design system, where it
is designed and released, and this directory is deleted (IMPL-0005
12.7). Nothing here names boop's domain.

| File | Candidate |
| --- | --- |
| `StackedBarsOverTime.tsx` | a stacked bar per time bucket, with a table helper for `ChartCard` |
| `SegmentedControl.tsx` | the package's `Tabs` list as a control with no panels |
| `OptionRows.tsx` | rows for a picker popover: heading, item with a detail column, separator |
| `FilterBar.tsx` | the row around `FilterChip`s: remove, add, clear, count |
| `InlineState.tsx` | one-line loading and empty text inside a panel |
| `AppSwitcher.tsx` | the sidebar header as a menu of configured apps |
