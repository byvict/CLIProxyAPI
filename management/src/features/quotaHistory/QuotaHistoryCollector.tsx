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
import { getStatusFromError } from '@/utils/quota';
import { classifyQuotaFiles } from '@/features/quota/logic';
import { QUOTA_ADAPTERS, getQuotaMap, getQuotaSetter } from '@/features/quota/providers';
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
    let timer: number | undefined;
    const revision = apiClient.getConnectionRevision();
    const historyClient = apiClient.captureConnection();
    const current = () => active && revision === apiClient.getConnectionRevision();
    const collect = async () => {
      if (running || !current() || document.visibilityState === 'hidden') return;
      window.clearTimeout(timer);
      running = true;
      try {
        const { files } = await authFilesApi.list();
        if (!current()) return;
        await Promise.all(
          classifyQuotaFiles(files || []).map(async ({ file, type }) => {
            const authIndex = normalizeAuthIndex(file.authIndex);
            if (!authIndex || file.disabled || !current()) return;
            const cacheGeneration = captureQuotaCacheGeneration(file.name);
            const adapter = QUOTA_ADAPTERS[type];
            const key = getQuotaCacheKey(file);
            const commit = (quota: ReturnType<typeof adapter.buildSuccessState>) => {
              if (!current()) return;
              commitIfQuotaCacheCurrent(cacheGeneration, () => {
                getQuotaSetter(adapter)((previous) => ({ ...previous, [key]: quota }));
              });
            };
            try {
              if (getQuotaMap(adapter)[key]?.status === 'loading') return;
              const claimed = await reserveQuotaPoll(authIndex, historyClient);
              if (!current()) return;
              const cached = getQuotaMap(adapter)[key];
              if (cached?.status === 'loading') return;
              // An empty browser cache still needs its first reading after reopening.
              // Muse retains its persisted floor because its reader can mint a key.
              if (!claimed && (type === 'meta' || cached?.status === 'success')) return;
              const data = await adapter.fetchQuota(file, t);
              commit(adapter.buildSuccessState(data));
            } catch (error: unknown) {
              const message = error instanceof Error ? error.message : t('common.unknown_error');
              commit(adapter.buildErrorState(message, getStatusFromError(error)));
              // Failed reads never create a synthetic balance. Gaps suspend projections.
              if (current()) window.dispatchEvent(new Event('quota-history-error'));
            }
          })
        );
      } catch {
        // Existing connection handling owns authentication and transport errors.
        if (current()) window.dispatchEvent(new Event('quota-history-error'));
      } finally {
        running = false;
        if (current()) {
          timer = window.setTimeout(() => void collect(), 5 * 60_000);
        }
      }
    };
    void collect();
    window.addEventListener('quota-history-refresh', collect);
    document.addEventListener('visibilitychange', collect);
    return () => {
      active = false;
      window.clearTimeout(timer);
      window.removeEventListener('quota-history-refresh', collect);
      document.removeEventListener('visibilitychange', collect);
    };
  }, [connected, generation, t]);
  return null;
}
