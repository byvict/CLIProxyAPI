import { apiClient } from '@/services/api/client';
import type { QuotaObservation } from './projection';

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

const nullableNumber = (value: unknown): value is number | null =>
  value === null || (typeof value === 'number' && Number.isFinite(value));

function isObservation(value: unknown): value is QuotaObservation {
  return (
    isRecord(value) &&
    typeof value.credential === 'string' &&
    typeof value.provider === 'string' &&
    typeof value.windowId === 'string' &&
    typeof value.label === 'string' &&
    typeof value.observedAtMs === 'number' &&
    Number.isFinite(value.observedAtMs) &&
    nullableNumber(value.remaining) &&
    nullableNumber(value.resetAtMs) &&
    nullableNumber(value.periodHours)
  );
}

export async function readQuotaHistory(client = apiClient, signal?: AbortSignal): Promise<QuotaObservation[]> {
  const response: unknown = await client.get('/quota-history?period=30d', { signal, timeout: 0 });
  if (
    !isRecord(response) ||
    !Array.isArray(response.observations) ||
    !response.observations.every(isObservation)
  ) {
    throw new Error('Invalid quota history response');
  }
  return response.observations;
}

export const recordQuotaHistory = (
  authIndex: string,
  observations: QuotaObservation[],
  client = apiClient
) => client.post('/quota-history', { authIndex, observations });

export async function reserveQuotaPoll(authIndex: string, client = apiClient): Promise<boolean> {
  const response: unknown = await client.post('/quota-history/poll', { authIndex });
  if (!isRecord(response) || typeof response.claimed !== 'boolean')
    throw new Error('Invalid quota poll response');
  return response.claimed;
}
