import type { Schemas } from "../src/api/client";

// Sample API payloads for page tests: one of each shape, in the
// acme-web owner the test caller sees.

const at = "2026-11-01T10:00:00Z";

export const page = <T>(items: T[]) => ({ pages: [{ items, next_cursor: null }], pageParams: [undefined] });

export const update: Schemas["Update"] = {
  repo_id: 7,
  owner: "acme-web",
  slug: "acme-web/site",
  branch: "renovate/react-19.x",
  pr_number: 42,
  package_file: "package.json",
  package_name: "react",
  manager: "npm",
  datasource: "npm",
  update_type: "major",
  from_version: "18.3.1",
  to_version: "19.0.0",
  purl_from: "pkg:npm/react@18.3.1",
  purl_to: "pkg:npm/react@19.0.0",
};

export const event: Schemas["Event"] = {
  id: 101,
  key: "run.finished/7/1",
  occurred_at: at,
  recorded_at: at,
  kind: "run.finished",
  source: "worker",
  repo_id: 7,
  cause_key: "schedule/7",
  summary: "Run finished: Succeeded",
  data: { v: 1 },
};

export const pull: Schemas["PullRequest"] = {
  repo_id: 7,
  owner: "acme-web",
  slug: "acme-web/site",
  number: 42,
  branch: "renovate/react-19.x",
  state: "open",
  title: "Update react to v19",
  labels: ["dependencies"],
  automerge: false,
  url: "https://github.com/acme-web/site/pull/42",
  opened_at: at,
  updated_at: at,
  updates: [update],
};

export const pullDetail: Schemas["PullRequestDetail"] = { ...pull, timeline: [event] };

export const repository: Schemas["RepositoryDetail"] = {
  id: 7,
  installation_id: 1,
  owner: "acme-web",
  slug: "acme-web/site",
  state: "idle",
  profile: "default",
  onboarded_at: at,
  last_run_at: at,
  last_outcome: "Succeeded",
  open_prs: 1,
  waiting: 1,
  live: true,
  phase: "idle",
};

export const run: Schemas["Run"] = {
  id: "repo/github/7-run-1",
  repo_id: 7,
  owner: "acme-web",
  slug: "acme-web/site",
  cause: "schedule",
  outcome: "Succeeded",
  started_at: at,
  duration_ms: 247_000,
  spend: { core: { before: 5000, after: 4880 } },
  updates: 3,
};

export const item: Schemas["DashboardItem"] = {
  repo_id: 7,
  owner: "acme-web",
  slug: "acme-web/site",
  marker: "approve-branch",
  branch: "renovate/react-19.x",
  section: "pending-approval",
  title: "Update react to v19",
  deps: ["react"],
  first_seen_at: at,
  last_seen_at: at,
};

export const pkg: Schemas["Package"] = {
  purl: "pkg:npm/react",
  repositories: 4,
  updates: 4,
  from_versions: ["18.2.0", "18.3.1"],
  to_versions: ["19.0.0"],
  managers: ["npm"],
};

export const dependency: Schemas["Dependency"] = {
  repo_id: 7,
  owner: "acme-web",
  slug: "acme-web/site",
  manager: "npm",
  package_file: "package.json",
  package_name: "react",
  datasource: "npm",
  current_value: "^18.3.1",
  current_version: "18.3.1",
  purl: "pkg:npm/react@18.3.1",
};

export const delivery: Schemas["Delivery"] = {
  delivery_id: "0f6c2d1e-8b8f-4c55-9f0e-3a1d2b3c4d5e",
  attempt: 1,
  app: "boop-bot",
  event: "pull_request",
  action: "closed",
  outcome: "routed",
  github_status: 202,
  missed: false,
  received_at: at,
};

export const installation: Schemas["Installation"] = {
  id: 1,
  app: "boop-bot",
  owner: "acme-web",
  suspended: false,
  repositories: 12,
  core: { remaining: 4880, used_since_run_start: 120, observed_at: at },
};

export const estate: Schemas["Estate"] = {
  range: "30d",
  computed_at: at,
  repositories: 12,
  open_prs: 9,
  waiting: 3,
  oldest_open_pr_opened_at: at,
  runs_24h: 24,
  run_success_rate_24h: 95.8,
  lost_deliveries_24h: 0,
  merged_in_range: 17,
  age_buckets: [{ label: "< 7d", count: 4 }],
  top_packages: [pkg],
  ecosystems: [{ label: "npm", count: 9 }],
};

export const series = (metric: string): Schemas["EstateSeries"] => ({
  metric,
  range: "30d",
  bucket: "day",
  points: [{ at, value: 3, by: metric === "runs" ? { Succeeded: 3 } : undefined } as Schemas["SeriesPoint"]],
});

export const status: Schemas["Status"] = {
  state: "operational",
  updated_at: at,
  version: "v0.9.0",
  components: [{ name: "api", state: "operational", latency_ms: 12 }],
  fleet: { repositories: 12, open_prs: 9, runs_24h: 24 },
};
