import type { TimelineItem } from "@donaldgifford/design-system/blocks/timeline";

import type { Schemas } from "../api/client";
import { when } from "../lib/when";

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
// has one.
export function eventItem(e: Schemas["Event"]): TimelineItem {
  return {
    id: String(e.id),
    actor: e.actor_login ?? e.source,
    verb: e.kind,
    object: e.summary,
    detail: e.cause_key ? <span className="font-mono text-label text-muted-foreground">caused by {e.cause_key}</span> : undefined,
    when: when(e.occurred_at),
    tone: toneOf(e.kind),
    tags: [{ label: e.source, variant: "outline" }],
  };
}
