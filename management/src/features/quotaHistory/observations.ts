import type { QuotaProviderType } from '@/features/quota/providers/types';
import { isRecord } from './api';
import type { QuotaObservation } from './projection';

const number = (value: unknown): number | null =>
  typeof value === 'number' && Number.isFinite(value) ? value : null;
const text = (value: unknown): string => (typeof value === 'string' ? value : '');
const records = (value: unknown): Record<string, unknown>[] =>
  Array.isArray(value) ? value.filter(isRecord) : [];
const remaining = (value: unknown): number | null => {
  const percent = number(value);
  return percent === null ? null : Math.min(100, Math.max(0, 100 - percent));
};

export function quotaObservations(
  credential: string,
  provider: QuotaProviderType,
  quota: unknown,
  observedAtMs: number
): QuotaObservation[] {
  if (!isRecord(quota) || quota.status !== 'success') return [];
  const base = { credential, provider, observedAtMs };
  const make = (window: Record<string, unknown>, index: number): QuotaObservation => ({
    ...base,
    windowId: text(window.id) || text(window.labelKey) || String(index),
    label: text(window.label) || text(window.id),
    remaining: remaining(window.usedPercent),
    resetAtMs: number(window.resetAtMs),
    periodHours: number(window.periodHours),
  });

  if (provider === 'claude' || provider === 'codex' || provider === 'devin') {
    return records(quota.windows).map((window, index) => ({
      ...make(window, index),
      remaining:
        provider === 'devin' ? number(window.remainingPercent) : remaining(window.usedPercent),
    }));
  }
  if (provider === 'xai') {
    if (!isRecord(quota.billing) || quota.billing.periodType !== 'weekly') return [];
    return [
      {
        ...make(quota.billing, 0),
        windowId: 'weekly',
        label: 'Weekly',
        remaining: remaining(quota.billing.usagePercent),
        periodHours: number(quota.billing.periodHours) ?? 168,
      },
    ];
  }
  if (provider === 'meta') {
    if (!isRecord(quota.data)) return [];
    return records(quota.data.windows).map((window, index) => ({
      ...make(window, index),
      label: text(window.id),
      resetAtMs: number(window.resetAt) === null ? null : Number(window.resetAt) * 1000,
      periodHours:
        window.id === 'weekly'
          ? 168
          : number(window.durationMinutes) === null
            ? null
            : Number(window.durationMinutes) / 60,
    }));
  }
  if (provider === 'kimi') {
    return records(quota.rows).map((row, index) => {
      const limit = number(row.limit);
      const used = number(row.used);
      return {
        ...make(row, index),
        remaining:
          limit !== null && limit > 0 && used !== null ? remaining((used / limit) * 100) : null,
      };
    });
  }
  return records(quota.groups).flatMap((group) =>
    records(group.buckets).map((bucket, index) => ({
      ...make(bucket, index),
      windowId: `${text(group.id)}/${text(bucket.id) || index}`,
      remaining:
        number(bucket.remainingFraction) === null ? null : Number(bucket.remainingFraction) * 100,
    }))
  );
}
