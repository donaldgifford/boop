const dayMs = 86_400_000;

// daysSince is whole days from iso to now; undefined without a time.
export function daysSince(iso: string | null | undefined, now = Date.now()): number | undefined {
  if (!iso) return undefined;
  return Math.max(0, Math.floor((now - Date.parse(iso)) / dayMs));
}

// duration renders milliseconds as "4m 07s", or "1h 02m" past an hour.
export function duration(ms: number | null | undefined): string {
  if (ms === null || ms === undefined) return "—";
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${String(s % 60).padStart(2, "0")}s`;
  return `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, "0")}m`;
}

// rangeDays is a range's length in days ("7d" is 7).
export function rangeDays(range: string): number {
  return Number.parseInt(range, 10) || 30;
}

// count formats a number with grouping.
export function count(n: number | null | undefined): string {
  return n === null || n === undefined ? "—" : n.toLocaleString("en-US");
}
