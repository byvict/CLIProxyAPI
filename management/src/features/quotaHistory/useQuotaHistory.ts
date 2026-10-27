import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useAuthStore, useQuotaStore } from '@/stores';
import { readQuotaHistory } from './api';
import type { QuotaObservation } from './projection';

export function useQuotaHistory() {
  const { t } = useTranslation();
  const connected = useAuthStore((state) => state.connectionStatus === 'connected');
  const generation = useQuotaStore((state) => state.cacheGeneration);
  const [observations, setObservations] = useState<QuotaObservation[]>([]);
  const [error, setError] = useState('');
  useEffect(() => {
    let active = true;
    let loading = false;
    setObservations([]);
    setError('');
    const load = async () => {
      if (!connected || loading) return;
      loading = true;
      try {
        const data = await readQuotaHistory();
        if (active) {
          setObservations(data);
          setError('');
        }
      } catch (reason: unknown) {
        if (active) setError(reason instanceof Error ? reason.message : String(reason));
      } finally {
        loading = false;
      }
    };
    void load();
    const timer = window.setInterval(() => void load(), 60_000);
    window.addEventListener('quota-history-updated', load);
    const failed = () => {
      if (active) setError(t('quota_history.collection_failed'));
    };
    window.addEventListener('quota-history-error', failed);
    return () => {
      active = false;
      window.clearInterval(timer);
      window.removeEventListener('quota-history-updated', load);
      window.removeEventListener('quota-history-error', failed);
    };
  }, [connected, generation, t]);
  return { observations, error };
}
