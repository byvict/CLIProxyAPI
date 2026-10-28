import { useCallback, useEffect, useMemo, useSyncExternalStore } from 'react';
import { apiClient } from '@/services/api/client';
import { useAuthStore, useQuotaStore } from '@/stores';

export const providers = ['all', 'claude', 'codex', 'grok', 'meta'] as const;
export type Provider = (typeof providers)[number];
export const providerNames: Record<Provider, string> = {
  all: 'Todos',
  claude: 'Claude',
  codex: 'Codex',
  grok: 'Grok',
  meta: 'Meta',
};
export interface ActivityDay {
  date: string;
  agents: number;
  tokens: number;
  tokenRecords?: number;
  reportedAgents: number;
  partialAgents: number;
  longestDurationMs: number;
}
export interface Activity {
  year: number;
  timezone: string;
  today: string;
  generatedAt: string;
  availableFrom: string;
  historyImported?: boolean;
  providers: { id: Exclude<Provider, 'all'>; days: ActivityDay[] }[];
}
export type PerformanceResponse =
  { status: 'unavailable' } | { status: 'ready'; activity: Activity };
export interface GitHubActivity {
  login: string;
  year: number;
  contributions: number;
  pullRequests: number;
  reviews: number;
  commits: number;
  days: { date: string; count: number }[];
}
export type GitHubResponse =
  | { status: 'disconnected' }
  | {
      status: 'connected';
      source: 'configuration' | 'environment';
      activity: GitHubActivity;
    };

const record = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);
const count = (value: unknown): value is number =>
  typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
const date = (value: unknown): value is string =>
  typeof value === 'string' &&
  /^\d{4}-\d{2}-\d{2}$/.test(value) &&
  Number.isFinite(Date.parse(value));
function isActivityDay(value: unknown): value is ActivityDay {
  return (
    record(value) &&
    date(value.date) &&
    count(value.agents) &&
    count(value.tokens) &&
    (value.tokenRecords === undefined || count(value.tokenRecords)) &&
    count(value.reportedAgents) &&
    count(value.partialAgents) &&
    count(value.longestDurationMs)
  );
}
function isActivity(value: unknown): value is Activity {
  return (
    record(value) &&
    count(value.year) &&
    (value.historyImported === undefined || typeof value.historyImported === 'boolean') &&
    typeof value.timezone === 'string' &&
    date(value.today) &&
    typeof value.generatedAt === 'string' &&
    typeof value.availableFrom === 'string' &&
    Array.isArray(value.providers) &&
    value.providers.every(
      (p: unknown) =>
        record(p) &&
        (p.id === 'claude' || p.id === 'codex' || p.id === 'grok' || p.id === 'meta') &&
        Array.isArray(p.days) &&
        p.days.every(isActivityDay)
    )
  );
}
export function parsePerformance(value: unknown): PerformanceResponse {
  if (record(value) && value.status === 'unavailable') return { status: 'unavailable' };
  if (record(value) && value.status === 'ready' && isActivity(value.activity)) {
    return { status: 'ready', activity: value.activity };
  }
  throw new Error('O servidor retornou dados de Performance inválidos.');
}
export function parseGitHub(value: unknown): GitHubResponse {
  if (record(value) && value.status === 'disconnected') return { status: 'disconnected' };
  if (
    record(value) &&
    value.status === 'connected' &&
    (value.source === 'configuration' || value.source === 'environment')
  ) {
    const a = value.activity;
    if (
      record(a) &&
      typeof a.login === 'string' &&
      count(a.year) &&
      count(a.contributions) &&
      count(a.pullRequests) &&
      count(a.reviews) &&
      count(a.commits) &&
      Array.isArray(a.days) &&
      a.days.every((day: unknown) => record(day) && date(day.date) && count(day.count))
    ) {
      const days: GitHubActivity['days'] = [];
      for (const day of a.days) {
        if (record(day) && date(day.date) && count(day.count))
          days.push({ date: day.date, count: day.count });
      }
      return {
        status: 'connected',
        source: value.source,
        activity: {
          login: a.login,
          year: a.year,
          contributions: a.contributions,
          pullRequests: a.pullRequests,
          reviews: a.reviews,
          commits: a.commits,
          days,
        },
      };
    }
  }
  throw new Error('O servidor retornou uma conexão GitHub inválida.');
}

type Remote<T> =
  | { status: 'loading' }
  | { status: 'error'; message: string }
  | { status: 'ready'; data: T; refreshing: boolean; refreshError: string };

const loading = { status: 'loading' } as const;
type ActivityEntry = {
  state: Remote<unknown>;
  receivedAt: number;
  controller: AbortController;
  pending?: Promise<void>;
};
const responses = new Map<string, ActivityEntry>();
const listeners = new Set<() => void>();
let scope = '';
const emit = () => listeners.forEach((listener) => listener());
function currentScope() {
  return [apiClient.getConnectionRevision(), useQuotaStore.getState().cacheGeneration,
    new Intl.DateTimeFormat('en', { year: 'numeric', timeZone: 'America/Sao_Paulo' }).format(Date.now())].join(':');
}
function entries() {
  const next = currentScope();
  if (scope !== next) {
    responses.forEach((entry) => entry.controller.abort());
    responses.clear();
    scope = next;
  }
  return responses;
}

export function invalidateActivity(path: string) {
  const cache = entries();
  cache.get(path)?.controller.abort();
  cache.delete(path);
  emit();
}

async function readActivity(path: string, parse: (value: unknown) => unknown, force: boolean) {
  const cache = entries();
  const previous = cache.get(path);
  if (previous?.pending) return previous.pending;
  if (!force && previous?.state.status === 'ready' && Date.now() - previous.receivedAt < 60_000) return;
  const entry: ActivityEntry = {
    state: previous?.state.status === 'ready'
      ? { ...previous.state, refreshing: true, refreshError: '' } : loading,
    receivedAt: previous?.receivedAt ?? 0,
    controller: new AbortController(),
  };
  cache.set(path, entry);
  const capturedScope = scope;
  const current = () => capturedScope === currentScope() && responses.get(path) === entry;
  const client = apiClient.captureConnection();
  entry.pending = (async () => {
    try {
      const data = await client.get<unknown>(path, { signal: entry.controller.signal, timeout: 0 });
      parse(data);
      if (current()) {
        entry.state = { status: 'ready', data, refreshing: false, refreshError: '' };
        entry.receivedAt = Date.now();
      }
    } catch (error: unknown) {
      if (current()) {
        const message = error instanceof Error ? error.message : 'Não foi possível carregar os dados.';
        entry.state = entry.state.status === 'ready'
          ? { ...entry.state, refreshing: false, refreshError: message }
          : { status: 'error', message };
      }
    } finally {
      entry.pending = undefined;
      if (current()) emit();
    }
  })();
  emit();
  return entry.pending;
}

export function useActivity<T>(path: string, parse: (value: unknown) => T, enabled = true) {
  const apiBase = useAuthStore((state) => state.apiBase);
  const managementKey = useAuthStore((state) => state.managementKey);
  const generation = useQuotaStore((state) => state.cacheGeneration);
  const readScope = currentScope();
  const subscribe = useCallback((listener: () => void) => {
    listeners.add(listener);
    return () => { listeners.delete(listener); };
  }, []);
  const snapshot = useCallback(() => entries().get(path)?.state ?? loading, [path]);
  const raw = useSyncExternalStore(subscribe, snapshot);
  const state: Remote<T> = useMemo(() => raw.status === 'ready'
    ? { ...raw, data: parse(raw.data) } : raw, [raw, parse]);
  const refresh = useCallback(() => { void readActivity(path, parse, true); }, [path, parse]);
  useEffect(() => {
    if (enabled) void readActivity(path, parse, false);
  }, [apiBase, managementKey, generation, readScope, path, parse, enabled]);
  return { state, refresh, refreshing: state.status === 'ready' && state.refreshing };
}
