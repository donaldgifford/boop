// Candidate for the design system (INV-0005 finding 8): one stacked bar per
// time bucket. TrendChart draws one series and StackedBars stacks by
// category with no time axis, so "runs by outcome over the range" has no
// block today. Segments reuse StackedBars' shape.
import { type ChartTable, SeriesLegend } from "@donaldgifford/design-system/blocks/chart-card";
import type { StackSegment } from "@donaldgifford/design-system/blocks/stacked-bars";
import { cn } from "@donaldgifford/design-system/lib/utils";

export interface StackPoint {
  /** The start of the bucket, as an ISO time. */
  at: string;
  /** The count for each segment key; a missing key is zero. */
  counts: Readonly<Record<string, number>>;
}

interface StackedBarsOverTimeProps {
  points: readonly StackPoint[];
  segments: readonly StackSegment[];
  height?: number | undefined;
  /** The accessible name: what the chart shows. */
  label: string;
  className?: string | undefined;
}

function total(point: StackPoint, segments: readonly StackSegment[]): number {
  return segments.reduce((sum, s) => sum + (point.counts[s.key] ?? 0), 0);
}

function dayLabel(iso: string): string {
  return new Date(iso).toLocaleDateString("en-US", { month: "short", day: "numeric", timeZone: "UTC" });
}

export function StackedBarsOverTime({ points, segments, height = 180, label, className }: StackedBarsOverTimeProps) {
  const top = Math.max(4, Math.ceil(Math.max(0, ...points.map((p) => total(p, segments))) / 4) * 4);
  const yTicks = [top, (top * 3) / 4, top / 2, top / 4, 0];
  const step = Math.max(1, Math.ceil(points.length / 5));
  const xTicks = points.filter((_, i) => i % step === 0 || i === points.length - 1);
  return (
    <div data-slot="stacked-bars-over-time" className={className}>
      <div className="grid grid-cols-[auto_1fr] gap-x-2">
        <div aria-hidden="true" className="flex flex-col items-end justify-between font-mono text-label text-muted-foreground tabular-nums" style={{ height }}>
          {yTicks.map((tick) => (
            <span key={tick}>{tick}</span>
          ))}
        </div>
        <ul
          aria-label={label}
          className="flex items-end gap-0.5 border-b bg-[linear-gradient(to_bottom,var(--border)_1px,transparent_1px)] bg-size-[100%_25%]"
          style={{ height }}
        >
          {points.map((point) => {
            const sum = total(point, segments);
            const title = `${dayLabel(point.at)}: ${segments.map((s) => `${s.label} ${point.counts[s.key] ?? 0}`).join(", ")}`;
            return (
              <li key={point.at} title={title} className={cn("flex h-full min-w-0 flex-1 flex-col-reverse gap-px hover:opacity-75", sum === 0 && "border-muted border-b-2")}>
                <span className="sr-only">{title}</span>
                {segments.map((s) => {
                  const value = point.counts[s.key] ?? 0;
                  return value === 0 ? null : <span key={s.key} aria-hidden="true" className="block w-full" style={{ height: `${(100 * value) / top}%`, background: s.color }} />;
                })}
              </li>
            );
          })}
        </ul>
        <div aria-hidden="true" className="col-start-2 mt-1 flex justify-between font-mono text-label text-muted-foreground">
          {xTicks.map((point) => (
            <span key={point.at}>{dayLabel(point.at)}</span>
          ))}
        </div>
      </div>
      <SeriesLegend items={segments} />
    </div>
  );
}

/** The stacks' data as a ChartCard table, newest first. */
export function stackTimeTable(points: readonly StackPoint[], segments: readonly StackSegment[], first = "Day"): ChartTable {
  return {
    head: [first, ...segments.map((s) => s.label)],
    numeric: segments.map((_, i) => i + 1),
    rows: [...points].reverse().map((p) => [p.at.slice(0, 10), ...segments.map((s) => (p.counts[s.key] ?? 0).toLocaleString("en-US"))]),
  };
}
