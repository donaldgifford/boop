import { type QueryKey, useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo } from "react";

import { api, type Schemas, unwrap } from "./client";

// Polling (DESIGN-0004 OQ10): lists every 30 s, the event stream every
// 15 s, and a repository with a run in progress every 5 s.
export const listPollMs = 30_000;
export const eventPollMs = 15_000;
export const livePollMs = 5_000;

// UIConfig is the BFF's /ui/config: display values only.
export interface UIConfig {
  issuer: string;
  public_url: string;
  session_ttl_seconds: number;
  // links are the optional external links (DESIGN-0004 § The UI).
  links: { label: string; url: string }[];
}

export function useUIConfig() {
  return useQuery({
    queryKey: ["ui-config"],
    queryFn: async (): Promise<UIConfig> => (await fetch("/ui/config")).json() as Promise<UIConfig>,
    staleTime: Infinity,
  });
}

// useMe is the signed-in caller: name, groups, visible owners and
// whether they may recheck.
export function useMe() {
  return useQuery({ queryKey: ["me"], queryFn: () => unwrap(api.GET("/me")), staleTime: 5 * 60_000 });
}

// --- filters, one per list, as the URL and the query key carry them ---

export interface RepositoryFilters {
  owner?: string | undefined;
  q?: string | undefined;
  state?: string | undefined;
  profile?: string | undefined;
  has_open_prs?: boolean | undefined;
}

export interface PullRequestFilters {
  state?: string | undefined;
  owner?: string | undefined;
  repo?: number | undefined;
  manager?: string | undefined;
  update_type?: string | undefined;
  older_than?: number | undefined;
  automerge?: boolean | undefined;
  label?: string | undefined;
  purl?: string | undefined;
}

export interface DashboardItemFilters {
  section?: string | undefined;
  owner?: string | undefined;
  repo?: number | undefined;
  waiting_longer_than?: number | undefined;
}

export interface PackageFilters {
  owner?: string | undefined;
  manager?: string | undefined;
}

export interface RunFilters {
  outcome?: string | undefined;
  owner?: string | undefined;
  cause?: string | undefined;
}

export interface EventFilters {
  kind?: string | undefined;
  source?: string | undefined;
  actor?: string | undefined;
  owner?: string | undefined;
  repo?: number | undefined;
  cause_key?: string | undefined;
}

export interface DeliveryFilters {
  event?: string | undefined;
  outcome?: string | undefined;
  missed?: boolean | undefined;
}

// keys are the query keys, exported so tests seed exactly what a page
// reads. Filters drop undefined values, so equal filters are equal keys.
export const keys = {
  estate: (owner: string | undefined, range: string) => ["estate", { owner, range }] as const,
  series: (metric: string, owner: string | undefined, range: string) => ["estate-series", { metric, owner, range }] as const,
  installations: () => ["installations"] as const,
  repositories: (f: RepositoryFilters) => ["repositories", clean(f)] as const,
  repository: (id: number) => ["repository", id] as const,
  repositoryEvents: (id: number) => ["repository-events", id] as const,
  repositoryRuns: (id: number) => ["repository-runs", id] as const,
  repositoryDependencies: (id: number) => ["repository-dependencies", id] as const,
  pullRequests: (f: PullRequestFilters) => ["pull-requests", clean(f)] as const,
  pullRequest: (repoId: number, number: number) => ["pull-request", repoId, number] as const,
  dashboardItems: (f: DashboardItemFilters) => ["dashboard-items", clean(f)] as const,
  packages: (f: PackageFilters) => ["packages", clean(f)] as const,
  updates: (purl: string) => ["updates", purl] as const,
  dependencies: (purl: string) => ["dependencies", purl] as const,
  runs: (f: RunFilters) => ["runs", clean(f)] as const,
  events: (f: EventFilters) => ["events", clean(f)] as const,
  deliveries: (f: DeliveryFilters) => ["deliveries", clean(f)] as const,
  status: () => ["status"] as const,
};

function clean<T extends object>(f: T): Partial<T> {
  return Object.fromEntries(Object.entries(f).filter(([, v]) => v !== undefined && v !== "")) as Partial<T>;
}

// q drops undefined values from a query, which exactOptionalPropertyTypes
// requires of openapi-fetch's optional parameters.
function q<T extends object>(o: T): { [K in keyof T]?: Exclude<T[K], undefined> } {
  return Object.fromEntries(Object.entries(o).filter(([, v]) => v !== undefined)) as { [K in keyof T]?: Exclude<T[K], undefined> };
}

// Page is every list's envelope.
export interface Page<T> {
  items: T[];
  next_cursor: string | null;
}

interface ListOptions {
  refetchInterval?: number | false | undefined;
  enabled?: boolean | undefined;
}

// useList pages a list by cursor ("Load more") and flattens it.
export function useList<T>(queryKey: QueryKey, fetchPage: (cursor: string | undefined) => Promise<Page<T>>, opts: ListOptions = {}) {
  const query = useInfiniteQuery({
    queryKey,
    queryFn: ({ pageParam }) => fetchPage(pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    refetchInterval: opts.refetchInterval ?? listPollMs,
    enabled: opts.enabled ?? true,
  });
  const items = useMemo(() => query.data?.pages.flatMap((p) => p.items) ?? [], [query.data]);
  return { ...query, items };
}

// --- estate ---

export function useEstate(owner: string | undefined, range: string) {
  return useQuery({
    queryKey: keys.estate(owner, range),
    queryFn: () => unwrap(api.GET("/estate", { params: { query: q({ owner, range }) } })),
    refetchInterval: listPollMs,
  });
}

export function useEstateSeries(metric: string, owner: string | undefined, range: string) {
  return useQuery({
    queryKey: keys.series(metric, owner, range),
    queryFn: () => unwrap(api.GET("/estate/series", { params: { query: { metric, ...q({ owner, range }) } } })),
    refetchInterval: listPollMs,
  });
}

export function useInstallations() {
  return useList(keys.installations(), (cursor) => unwrap(api.GET("/installations", { params: { query: q({ cursor }) } })));
}

// --- repositories ---

export function useRepositories(f: RepositoryFilters) {
  return useList(keys.repositories(f), (cursor) => unwrap(api.GET("/repositories", { params: { query: q({ ...f, cursor }) } })));
}

// inProgress reports whether a repository has a run going, which turns
// its page's polling up to livePollMs.
export function inProgress(r: Pick<Schemas["RepositoryDetail"], "state" | "phase"> | undefined): boolean {
  return r?.state === "running" || r?.phase === "running";
}

export function useRepository(id: number) {
  return useQuery({
    queryKey: keys.repository(id),
    queryFn: () => unwrap(api.GET("/repositories/{id}", { params: { path: { id } } })),
    refetchInterval: (q) => (inProgress(q.state.data) ? livePollMs : listPollMs),
  });
}

export function useRepositoryEvents(id: number, live: boolean) {
  return useList(keys.repositoryEvents(id), (cursor) => unwrap(api.GET("/repositories/{id}/events", { params: { path: { id }, query: q({ cursor }) } })), {
    refetchInterval: live ? livePollMs : listPollMs,
  });
}

export function useRepositoryRuns(id: number, live: boolean) {
  return useList(keys.repositoryRuns(id), (cursor) => unwrap(api.GET("/repositories/{id}/runs", { params: { path: { id }, query: q({ cursor }) } })), {
    refetchInterval: live ? livePollMs : listPollMs,
  });
}

export function useRepositoryDependencies(id: number) {
  return useList(keys.repositoryDependencies(id), (cursor) =>
    unwrap(api.GET("/repositories/{id}/dependencies", { params: { path: { id }, query: q({ cursor }) } })),
  );
}

// useRecheck signals the repository's workflow (operators only; the
// BFF checks Origin, the API checks the operator group). The repository
// and its timeline are refetched once it is accepted.
export function useRecheck(id: number) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: () => unwrap(api.POST("/repositories/{id}/recheck", { params: { path: { id } } })),
    onSuccess: async () => {
      await Promise.all([
        client.invalidateQueries({ queryKey: keys.repository(id) }),
        client.invalidateQueries({ queryKey: keys.repositoryEvents(id) }),
      ]);
    },
  });
}

// --- pull requests, waiting, packages ---

export function usePullRequests(f: PullRequestFilters) {
  return useList(keys.pullRequests(f), (cursor) => unwrap(api.GET("/pull-requests", { params: { query: q({ ...f, cursor }) } })));
}

export function usePullRequest(repoId: number | undefined, number: number | undefined) {
  return useQuery({
    queryKey: keys.pullRequest(repoId ?? 0, number ?? 0),
    queryFn: () => unwrap(api.GET("/pull-requests/{repo_id}/{number}", { params: { path: { repo_id: repoId ?? 0, number: number ?? 0 } } })),
    enabled: repoId !== undefined && number !== undefined,
    refetchInterval: listPollMs,
  });
}

export function useDashboardItems(f: DashboardItemFilters) {
  return useList(keys.dashboardItems(f), (cursor) => unwrap(api.GET("/dashboard-items", { params: { query: q({ ...f, cursor }) } })));
}

export function usePackages(f: PackageFilters) {
  return useList(keys.packages(f), (cursor) => unwrap(api.GET("/packages", { params: { query: q({ ...f, cursor }) } })));
}

export function useUpdates(purl: string) {
  return useList(keys.updates(purl), (cursor) => unwrap(api.GET("/updates", { params: { query: q({ purl, cursor }) } })));
}

export function useDependencies(purl: string) {
  return useList(keys.dependencies(purl), (cursor) => unwrap(api.GET("/dependencies", { params: { query: q({ purl, cursor }) } })));
}

// --- activity ---

export function useRuns(f: RunFilters) {
  return useList(keys.runs(f), (cursor) => unwrap(api.GET("/runs", { params: { query: q({ ...f, cursor }) } })));
}

export function useEvents(f: EventFilters) {
  return useList(keys.events(f), (cursor) => unwrap(api.GET("/events", { params: { query: q({ ...f, cursor }) } })), { refetchInterval: eventPollMs });
}

export function useDeliveries(f: DeliveryFilters, enabled = true) {
  return useList(keys.deliveries(f), (cursor) => unwrap(api.GET("/deliveries", { params: { query: q({ ...f, cursor }) } })), { enabled });
}

// useStatus is the public status page's: 30 s, no session needed.
export function useStatus() {
  return useQuery({ queryKey: keys.status(), queryFn: () => unwrap(api.GET("/status")), refetchInterval: listPollMs });
}
