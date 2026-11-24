import { useTimeZone } from "@donaldgifford/design-system/blocks/time";
import { Timeline, type TimelineItem } from "@donaldgifford/design-system/blocks/timeline";
import type { Zone } from "@donaldgifford/design-system/lib/time";

import type { Schemas } from "../api/client";
import { localizeTimes, when } from "../lib/when";

type Tone = TimelineItem["tone"];

// toneOf colours an event by what happened to it.
function toneOf(kind: string): Tone {
  if (/failed|missed|errored/.test(kind)) return "destructive";
  if (/finished|merged|onboarded/.test(kind)) return "success";
  if (kind.startsWith("action.")) return "info";
  return "neutral";
}

// eventItem is an event as a Timeline row: who (the actor, else the
// source), what (the kind), the summary, and the cause when the event
// has one. Its times, the summary's included, are in zone.
export function eventItem(e: Schemas["Event"], zone: Zone): TimelineItem {
  return {
    id: String(e.id),
    actor: e.actor_login ?? e.source,
    verb: e.kind,
    object: localizeTimes(e.summary, zone),
    detail: e.cause_key ? <span className="font-mono text-label text-muted-foreground">caused by {e.cause_key}</span> : undefined,
    when: when(e.occurred_at, zone),
    tone: toneOf(e.kind),
    tags: [{ label: e.source, variant: "outline" }],
  };
}

// EventTimeline is events as a Timeline in the Local/UTC zone in force.
export function EventTimeline({ events }: { events: Schemas["Event"][] }) {
  const { zone } = useTimeZone();
  return <Timeline items={events.map((e) => eventItem(e, zone))} />;
}
