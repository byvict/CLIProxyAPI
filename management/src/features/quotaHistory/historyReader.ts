import { apiClient } from '@/services/api/client';
import { captureQuotaCacheGeneration, commitIfQuotaCacheCurrent } from '@/stores';
import { normalizeAuthIndex } from '@/utils/authIndex';
import type { QuotaAdapter } from '@/features/quota/providers';
import type { AuthFileItem } from '@/types';
import { recordQuotaHistory } from './api';
import { quotaObservations } from './observations';

export function withQuotaHistory(adapter: QuotaAdapter): QuotaAdapter {
  const wrap =
    (read: QuotaAdapter['fetchQuota']): QuotaAdapter['fetchQuota'] =>
    async (file, t) => {
      const client = apiClient.captureConnection();
      const generation = captureQuotaCacheGeneration(file.name);
      const data = await read(file, t);
      if (commitIfQuotaCacheCurrent(generation, () => {})) {
        void persist(file, adapter.buildSuccessState(data), client);
      }
      return data;
    };
  const persist = async (
    file: AuthFileItem,
    quota: ReturnType<QuotaAdapter['buildSuccessState']>,
    client: ReturnType<typeof apiClient.captureConnection>
  ) => {
    const identity = normalizeAuthIndex(file.authIndex);
    if (!identity) return;
    const observations = quotaObservations(identity, adapter.type, quota, Date.now());
    if (!observations.length) return;
    try {
      await recordQuotaHistory(identity, observations, client);
      window.dispatchEvent(new Event('quota-history-updated'));
    } catch {
      window.dispatchEvent(new Event('quota-history-error'));
    }
  };
  return {
    ...adapter,
    fetchQuota: wrap(adapter.fetchQuota),
    ...(adapter.resetQuota ? { resetQuota: wrap(adapter.resetQuota) } : {}),
  };
}
