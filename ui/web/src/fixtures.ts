// Fixture data for the mockup (INV-0005). The shapes follow DESIGN-0004's
// endpoints, so a page built on these moves to the real API by swapping
// the import for a query. Everything is generated from fixed seeds: the
// mockup draws the same numbers on every load.

export type PullState = "open" | "merged" | "closed";
export type UpdateType = "major" | "minor" | "patch" | "digest" | "pin";
export type RunOutcome = "succeeded" | "warned" | "failed";
export type RepoState = "idle" | "running" | "waiting" | "stalled" | "errored";

export interface Update {
  package: string;
  purl: string;
  from: string;
  to: string;
  updateType: UpdateType;
}

export interface PullRequest {
  id: string;
  repo: string;
  number: number;
  title: string;
  state: PullState;
  manager: string;
  updateType: UpdateType;
  automerge: boolean;
  openedAt: string;
  ageDays: number;
  checks: "passing" | "failing" | "pending";
  updates: Update[];
}

export interface TimelineEvent {
  id: string;
  actor: string;
  verb: string;
  object: string;
  detail?: string;
  at: string;
  kind: string;
  tone: "neutral" | "success" | "destructive" | "info";
}

export interface Run {
  id: string;
  startedAt: string;
  outcome: RunOutcome;
  durationSeconds: number;
  cause: "schedule" | "webhook" | "recheck";
  updates: number;
  coreSpent: number;
}

export interface DayRuns {
  at: string;
  counts: Record<RunOutcome, number>;
}

// NOW is the mockup's clock, so ages and series do not drift.
export const NOW = new Date("2026-11-21T15:00:00Z");

function rng(seed: number): () => number {
  let s = seed;
  return () => {
    s = (s * 1103515245 + 12345) & 0x7fffffff;
    return s / 0x7fffffff;
  };
}

function daysAgo(n: number, hour = 9): string {
  const d = new Date(NOW);
  d.setUTCDate(d.getUTCDate() - n);
  d.setUTCHours(hour, 12, 0, 0);
  return d.toISOString();
}

function pick<T>(items: readonly T[], r: number): T {
  const item = items[Math.min(items.length - 1, Math.floor(r * items.length))];
  if (item === undefined) {
    throw new Error("pick from an empty list");
  }
  return item;
}

const REPOS = [
  "donaldgifford/proxmox-go-sdk",
  "donaldgifford/boop",
  "donaldgifford/x",
  "donaldgifford/docz",
  "donaldgifford/design-system",
  "donaldgifford/hclkit",
  "donaldgifford/libtftest",
  "fartlab/homelab",
  "fartlab/helm-charts",
  "acme-web/storefront",
] as const;

const PACKAGES: readonly { name: string; purl: string; manager: string; versions: readonly [string, string, UpdateType][] }[] = [
  { name: "go.temporal.io/sdk", purl: "pkg:golang/go.temporal.io/sdk", manager: "gomod", versions: [["1.38.0", "1.41.2", "minor"]] },
  { name: "github.com/jackc/pgx/v5", purl: "pkg:golang/github.com/jackc/pgx/v5", manager: "gomod", versions: [["5.7.2", "5.7.6", "patch"]] },
  { name: "react", purl: "pkg:npm/react", manager: "npm", versions: [["19.2.7", "19.3.1", "minor"]] },
  { name: "vite", purl: "pkg:npm/vite", manager: "npm", versions: [["7.3.1", "8.3.4", "major"]] },
  { name: "actions/checkout", purl: "pkg:githubactions/actions/checkout", manager: "github-actions", versions: [["v5", "v6", "major"]] },
  { name: "jdx/mise-action", purl: "pkg:githubactions/jdx/mise-action", manager: "github-actions", versions: [["v3", "v4", "major"]] },
  { name: "golang", purl: "pkg:docker/golang", manager: "dockerfile", versions: [["1.27.3", "1.27.4", "patch"]] },
  { name: "oven/bun", purl: "pkg:docker/oven/bun", manager: "dockerfile", versions: [["1.3.14", "1.4.4", "minor"]] },
  { name: "temporalio/helm-charts", purl: "pkg:helm/temporal", manager: "helm-values", versions: [["0.68.1", "0.71.0", "minor"]] },
  { name: "renovatebot/renovate", purl: "pkg:docker/renovate/renovate", manager: "helm-values", versions: [["44.179.2", "44.202.0", "minor"]] },
];

function buildPulls(): PullRequest[] {
  const next = rng(11);
  const pulls: PullRequest[] = [];
  for (let i = 0; i < 42; i++) {
    const repo = pick(REPOS, next());
    const pkg = pick(PACKAGES, next());
    const [from, to, updateType] = pick(pkg.versions, next());
    const age = Math.floor(next() * next() * 60);
    const stateRoll = next();
    const state: PullState = stateRoll < 0.68 ? "open" : stateRoll < 0.92 ? "merged" : "closed";
    const number = 20 + Math.floor(next() * 400);
    pulls.push({
      id: `${repo}#${number}`,
      repo,
      number,
      title: `chore(deps): update ${pkg.name} to ${to}`,
      state,
      manager: pkg.manager,
      updateType,
      automerge: updateType === "patch" || updateType === "digest",
      openedAt: daysAgo(age, 3 + Math.floor(next() * 18)),
      ageDays: age,
      checks: state !== "open" ? "passing" : pick(["passing", "passing", "failing", "pending"] as const, next()),
      updates: [{ package: pkg.name, purl: `${pkg.purl}@${to}`, from, to, updateType }],
    });
  }
  return pulls.sort((a, b) => b.ageDays - a.ageDays);
}

export const pulls = buildPulls();
export const openPulls = pulls.filter((p) => p.state === "open");

export function pullTimeline(pr: PullRequest): TimelineEvent[] {
  const events: TimelineEvent[] = [
    { id: "1", actor: "boop-bot", verb: "opened", object: `#${pr.number}`, detail: `from run ${pr.repo.split("/")[1]}-${pr.number}`, at: pr.openedAt, kind: "pr.opened", tone: "info" },
    { id: "2", actor: "github-actions", verb: pr.checks === "failing" ? "failed checks on" : "passed checks on", object: `#${pr.number}`, at: pr.openedAt, kind: "pr.checks", tone: pr.checks === "failing" ? "destructive" : "success" },
  ];
  if (pr.ageDays > 7) {
    events.push({ id: "3", actor: "boop-bot", verb: "rebased", object: `#${pr.number}`, detail: "base branch moved", at: daysAgo(Math.max(1, pr.ageDays - 6)), kind: "pr.synchronize", tone: "neutral" });
  }
  if (pr.state === "merged") {
    events.push({ id: "4", actor: "donaldgifford", verb: "merged", object: `#${pr.number}`, at: daysAgo(0, 8), kind: "pr.merged", tone: "success" });
  }
  if (pr.state === "closed") {
    events.push({ id: "4", actor: "donaldgifford", verb: "closed", object: `#${pr.number}`, detail: "superseded by a newer update", at: daysAgo(0, 8), kind: "pr.closed", tone: "neutral" });
  }
  return events.reverse();
}

function buildRunsByDay(days: number, base: number, seed: number): DayRuns[] {
  const next = rng(seed);
  return Array.from({ length: days }, (_, i) => {
    const total = Math.round(base * (0.55 + next() * 0.6));
    const failed = next() < 0.3 ? Math.round(total * next() * 0.12) : 0;
    const warned = Math.round(total * next() * 0.1);
    return { at: daysAgo(days - 1 - i, 0), counts: { succeeded: Math.max(0, total - failed - warned), warned, failed } };
  });
}

export const runsByDay = buildRunsByDay(30, 150, 7);

function buildOpenSeries(): { at: string; value: number }[] {
  const next = rng(5);
  let value = 262;
  return Array.from({ length: 30 }, (_, i) => {
    value = Math.max(200, Math.round(value + (next() - 0.42) * 14));
    return { at: daysAgo(29 - i, 0), value };
  });
}

export const openPullsSeries = buildOpenSeries();

export const estate = {
  repositories: 142,
  openPulls: 318,
  waiting: 57,
  oldestOpenDays: 41,
  runs24h: 146,
  successRate24h: 98.6,
  lostDeliveries: 0,
};

export const ageBuckets = [
  { label: "<1d", value: 34 },
  { label: "1-7d", value: 121 },
  { label: "7-14d", value: 78 },
  { label: "14-30d", value: 62 },
  { label: ">30d", value: 23 },
];

export const topPackages = [
  { label: "pkg:golang/go.temporal.io/sdk", value: 23, sub: "1.38.0 → 1.41.2" },
  { label: "pkg:githubactions/actions/checkout", value: 21, sub: "v5 → v6" },
  { label: "pkg:npm/vite", value: 14, sub: "7.x → 8.3.4" },
  { label: "pkg:docker/golang", value: 12, sub: "1.27.3 → 1.27.4" },
  { label: "pkg:npm/react", value: 9, sub: "19.2.7 → 19.3.1" },
  { label: "pkg:helm/temporal", value: 6, sub: "0.68.1 → 0.71.0" },
];

export const ecosystems = [
  { label: "gomod", value: 88 },
  { label: "github-actions", value: 74 },
  { label: "dockerfile", value: 52 },
  { label: "npm", value: 49 },
  { label: "helm-values", value: 29 },
  { label: "terraform", value: 17 },
  { label: "pip_requirements", value: 9 },
];

export const installations = [
  { owner: "donaldgifford", repositories: 96, corePercent: 38, graphqlPercent: 12, concurrent: "3 / 8" },
  { owner: "fartlab", repositories: 31, corePercent: 74, graphqlPercent: 21, concurrent: "2 / 4" },
  { owner: "acme-web", repositories: 15, corePercent: 93, graphqlPercent: 40, concurrent: "1 / 2" },
];

export const owners = [
  { owner: undefined, label: "All owners", detail: "142 repos" },
  { owner: "donaldgifford", label: "donaldgifford", detail: "96" },
  { owner: "fartlab", label: "fartlab", detail: "31" },
  { owner: "acme-web", label: "acme-web", detail: "15" },
] as const;

export const repository = {
  id: "812345678",
  slug: "donaldgifford/proxmox-go-sdk",
  profile: "go",
  cadence: "every 6h",
  state: "waiting" as RepoState,
  phase: "waiting for budget",
  nextDue: daysAgo(-1, 2),
  lastRun: "succeeded" as RunOutcome,
  openPulls: 7,
  waiting: 4,
};

export const repositoryTimeline: TimelineEvent[] = [
  { id: "e1", actor: "donaldgifford", verb: "requested a recheck of", object: "proxmox-go-sdk", detail: "cause: action.recheck · request 01JD3…", at: daysAgo(0, 14), kind: "action.recheck", tone: "info" },
  { id: "e2", actor: "boop-bot", verb: "opened", object: "#412", detail: "go.temporal.io/sdk 1.38.0 → 1.41.2 · cause: run proxmox-go-sdk-0612", at: daysAgo(0, 9), kind: "pr.opened", tone: "info" },
  { id: "e3", actor: "schedule", verb: "ran", object: "Renovate 44.179.2", detail: "succeeded in 1m 42s · 3 updates · 41 core requests", at: daysAgo(0, 9), kind: "run.finished", tone: "success" },
  { id: "e4", actor: "donaldgifford", verb: "ticked", object: "Dependency Dashboard", detail: "approved: vite 7.x → 8.3.4 (pending approval since 6d)", at: daysAgo(1, 16), kind: "dashboard.ticked", tone: "neutral" },
  { id: "e5", actor: "github", verb: "delivered", object: "push to main", detail: "webhook 7c1e… · run scheduled", at: daysAgo(1, 11), kind: "delivery.received", tone: "neutral" },
  { id: "e6", actor: "schedule", verb: "ran", object: "Renovate 44.179.2", detail: "failed after 4m 03s · lookup timeout for docker.io/library/golang", at: daysAgo(2, 9), kind: "run.finished", tone: "destructive" },
  { id: "e7", actor: "donaldgifford", verb: "merged", object: "#398", detail: "actions/checkout v5 → v6", at: daysAgo(3, 13), kind: "pr.merged", tone: "success" },
  { id: "e8", actor: "boop-bot", verb: "onboarded", object: "proxmox-go-sdk", detail: "renovate.json5 found · profile go", at: daysAgo(34, 10), kind: "repo.onboarded", tone: "neutral" },
];

function buildRuns(): Run[] {
  const next = rng(23);
  return Array.from({ length: 14 }, (_, i) => {
    const roll = next();
    const outcome: RunOutcome = i === 5 ? "failed" : roll < 0.12 ? "warned" : "succeeded";
    return {
      id: `proxmox-go-sdk-${String(612 - i).padStart(4, "0")}`,
      startedAt: daysAgo(Math.floor(i / 2), i % 2 === 0 ? 15 : 9),
      outcome,
      durationSeconds: 60 + Math.floor(next() * 220),
      cause: pick(["schedule", "schedule", "schedule", "webhook", "recheck"] as const, next()),
      updates: outcome === "failed" ? 0 : Math.floor(next() * 5),
      coreSpent: 20 + Math.floor(next() * 60),
    };
  });
}

export const repositoryRuns = buildRuns();

// The public status page (DESIGN-0004 § Estate view and status page):
// aggregate-only, so no owner, repository or package name appears.
export const status = {
  state: "degraded",
  // Against the real clock, not NOW: the page says how long ago it was computed.
  updatedAt: new Date(Date.now() - 12_000).toISOString(),
  version: "v0.9.2",
  components: [
    { name: "api", state: "operational", detail: "p95 84 ms" },
    { name: "ingest", state: "operational", detail: "last delivery 40s ago · 0.2% failed" },
    { name: "workers", state: "operational", detail: "2 pollers on the task queue" },
    { name: "temporal", state: "operational", detail: "reachable · 3 ms" },
    { name: "postgres", state: "operational", detail: "reachable · 2 ms" },
    { name: "discovery", state: "operational", detail: "last pass 4m ago · 0 failures" },
    { name: "deliveries", state: "operational", detail: "0 missed, 3 failed in 24h" },
    { name: "github_budget", state: "degraded", detail: "1 of 3 installations below reserve" },
  ],
  releases: [
    { id: "v0.9.2", date: "Nov 19", title: "v0.9.2", detail: "Sandboxed execution profiles.", running: true },
    { id: "v0.9.1", date: "Nov 12", title: "v0.9.1", detail: "Webhook ingest and delivery records.", running: false },
    { id: "v0.9.0", date: "Nov 3", title: "v0.9.0", detail: "Capacity leases across installations.", running: false },
  ],
};
