import { useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { authFilesApi } from '@/services/api';
import { apiClient } from '@/services/api/client';
import {
  useAuthStore,
  useQuotaStore,
  captureQuotaCacheGeneration,
  commitIfQuotaCacheCurrent,
} from '@/stores';
import { normalizeAuthIndex } from '@/utils/authIndex';
import { getQuotaCacheKey } from '@/utils/quota/identity';
import { classifyQuotaFiles } from '@/features/quota/logic';
import { QUOTA_ADAPTERS, getQuotaSetter } from '@/features/quota/providers';
import { reserveQuotaPoll } from './api';

// Reuse the official readers and session guards; no inference or token renewal.
export function QuotaHistoryCollector() {
  const { t } = useTranslation();
  const connected = useAuthStore((state) => state.connectionStatus === 'connected');
  const generation = useQuotaStore((state) => state.cacheGeneration);
  useEffect(() => {
    if (!connected) return;
    let active = true;
    let running = false;
    const inFlight = new Set<string>();
    const revision = apiClient.getConnectionRevision();
    const historyClient = apiClient.captureConnection();
    const current = () => active && revision === apiClient.getConnectionRevision();
    const collect = async () => {
      if (running || !current()) return;
      running = true;
      try {
        const { files } = await authFilesApi.list();
        if (!current()) return;
        void Promise.all(
          classifyQuotaFiles(files || []).map(async ({ file, type }) => {
            const authIndex = normalizeAuthIndex(file.authIndex);
            if (!authIndex || file.disabled || !current() || inFlight.has(authIndex)) return;
            inFlight.add(authIndex);
            const cacheGeneration = captureQuotaCacheGeneration(file.name);
            const adapter = QUOTA_ADAPTERS[type];
            try {
              if (!(await reserveQuotaPoll(authIndex, historyClient)) || !current()) return;
              const data = await adapter.fetchQuota(file, t);
              if (!current()) return;
              const quota = adapter.buildSuccessState(data);
              if (
                !commitIfQuotaCacheCurrent(cacheGeneration, () => {
                  getQuotaSetter(adapter)((previous) => ({
                    ...previous,
                    [getQuotaCacheKey(file)]: quota,
                  }));
                })
              )
                return;
            } catch {
              // Failed reads never create a synthetic balance. Gaps suspend projections.
              if (current()) window.dispatchEvent(new Event('quota-history-error'));
            } finally {
              inFlight.delete(authIndex);
            }
          })
        );
      } catch {
        // Existing connection handling owns authentication and transport errors.
        if (current()) window.dispatchEvent(new Event('quota-history-error'));
      } finally {
        running = false;
      }
    };
    void collect();
    window.addEventListener('quota-history-refresh', collect);
    const timer = window.setInterval(() => void collect(), 5 * 60_000);
    return () => {
      active = false;
      window.clearInterval(timer);
      window.removeEventListener('quota-history-refresh', collect);
    };
  }, [connected, generation, t]);
  return null;
}
