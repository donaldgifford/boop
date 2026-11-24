import { formatTime, type Zone } from "@donaldgifford/design-system/lib/time";

// when formats an event time as text in the chosen zone. The design
// system's Timeline takes `when` as a string, so a timeline cannot use
// <Time>; its rows pass the zone from useTimeZone instead (INV-0005
// finding 8). UTC is labelled, so a shared screenshot is unambiguous.
export function when(iso: string, zone: Zone): string {
  const text = formatTime(iso, { zone });
  return zone === "utc" ? `${text} UTC` : text;
}

// rfc3339 matches the UTC times the worker writes into summaries
// ("next run 2026-11-25T10:39:55Z").
const rfc3339 = /\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z/g;

// localizeTimes formats every UTC time in text as when does.
export function localizeTimes(text: string, zone: Zone): string {
  return text.replace(rfc3339, (iso) => when(iso, zone));
}
