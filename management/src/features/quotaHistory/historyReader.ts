import { apiClient } from '@/services/api/client';
import { captureQuotaCacheGeneration, commitIfQuotaCacheCurrent, useQuotaStore } from '@/stores';
import { normalizeAuthIndex } from '@/utils/authIndex';
import type { QuotaAdapter } from '@/features/quota/providers';
import type { AuthFileItem } from '@/types';
import { recordQuotaHistory } from './api';
import { quotaObservations } from './observations';

export function withQuotaHistory(adapter: QuotaAdapter): QuotaAdapter {
  const inflight = new Map<string, Promise<unknown>>();
  const resets = new Map<string, Promise<unknown>>();
  const enrichments = new Map<string, { data: unknown; promise: Promise<unknown> }>();
  const identity = (file: AuthFileItem) => JSON.stringify([
    adapter.type, file.name, normalizeAuthIndex(file.auth_index ?? file.authIndex),
  ]);
  const keyFor = (file: AuthFileItem) => {
    const generation = captureQuotaCacheGeneration(file.name);
    return JSON.stringify([apiClient.getConnectionRevision(), generation.cacheGeneration,
      generation.fileGenerations[file.name] ?? 0, identity(file)]);
  };
  const wrap = (read: QuotaAdapter['fetchQuota'], reset = false): QuotaAdapter['fetchQuota'] =>
    (file, t) => {
      if (reset) useQuotaStore.setState((state) => ({
        fileGenerations: { ...state.fileGenerations, [file.name]: (state.fileGenerations[file.name] ?? 0) + 1 },
      }));
      const key = keyFor(file);
      const resetting = !reset && resets.get(key);
      if (resetting) return resetting.catch((error: unknown) => {
        if (keyFor(file) !== key) throw error;
        return wrap(read)(file, t);
      });
      const existing = !reset && inflight.get(key);
      if (existing) return existing;
      const client = apiClient.captureConnection();
      const current = () => keyFor(file) === key;
      const promise = (async () => {
        const data = await read(file, t, client);
        if (!current()) throw new DOMException('Quota read superseded', 'AbortError');
        const authIndex = normalizeAuthIndex(file.auth_index ?? file.authIndex);
        if (authIndex) {
          const observations = quotaObservations(authIndex, adapter.type, adapter.buildSuccessState(data), Date.now());
          if (observations.length) {
            void recordQuotaHistory(authIndex, observations, client).then(() => {
              if (current()) window.dispatchEvent(new Event('quota-history-updated'));
            }).catch(() => {
              if (current()) window.dispatchEvent(new Event('quota-history-error'));
            });
          }
        }
        if (!current()) throw new DOMException('Quota read superseded', 'AbortError');
        return data;
      })();
      const pending = reset ? resets : inflight;
      pending.set(key, promise);
      void promise.finally(() => { if (pending.get(key) === promise) pending.delete(key); }).catch(() => {});
      return promise;
    };
  return {
    ...adapter,
    fetchQuota: wrap(adapter.fetchQuota),
    ...(adapter.enrichQuota ? {
      enrichQuota: (file, data, t) => {
        const key = keyFor(file);
        const existing = enrichments.get(key);
        if (existing && existing.data === data) return existing.promise;
        const generation = captureQuotaCacheGeneration(file.name);
        const client = apiClient.captureConnection();
        const promise = Promise.resolve(adapter.enrichQuota?.(file, data, t, client)).then((enriched) => {
          if (keyFor(file) !== key || !commitIfQuotaCacheCurrent(generation, () => {})) return data;
          return enriched;
        });
        enrichments.set(key, { data, promise });
        void promise.finally(() => { if (enrichments.get(key)?.promise === promise) enrichments.delete(key); }).catch(() => {});
        return promise;
      },
    } : {}),
    ...(adapter.resetQuota ? { resetQuota: wrap(adapter.resetQuota, true) } : {}),
  };
}
