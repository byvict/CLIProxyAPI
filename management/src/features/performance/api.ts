import { useCallback, useEffect, useState } from 'react';
import { apiClient } from '@/services/api/client';
import { useAuthStore } from '@/stores';

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
  { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; data: T };
export function useActivity<T>(path: string, parse: (value: unknown) => T) {
  const apiBase = useAuthStore((state) => state.apiBase);
  const managementKey = useAuthStore((state) => state.managementKey);
  const [version, setVersion] = useState(0);
  const [state, setState] = useState<Remote<T>>({ status: 'loading' });
  const refresh = useCallback(() => setVersion((value) => value + 1), []);
  useEffect(() => {
    const controller = new AbortController();
    const client = apiClient.captureConnection();
    setState({ status: 'loading' });
    client
      .get<unknown>(path, { signal: controller.signal, timeout: 0 })
      .then((value) => {
        const data = parse(value);
        if (!controller.signal.aborted) setState({ status: 'ready', data });
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setState({
            status: 'error',
            message: error instanceof Error ? error.message : 'Não foi possível carregar os dados.',
          });
      });
    return () => controller.abort();
  }, [apiBase, managementKey, version, path, parse]);
  return { state, refresh };
}
