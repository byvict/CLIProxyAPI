import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useAuthStore, useQuotaStore } from '@/stores';
import { apiClient } from '@/services/api/client';
import { readQuotaHistory } from './api';
import type { QuotaObservation } from './projection';

type HistorySnapshot = { scope: string; observations: QuotaObservation[]; error: string };
let historySnapshot: HistorySnapshot | undefined;

export function useQuotaHistory() {
  const { t } = useTranslation();
  const connected = useAuthStore((state) => state.connectionStatus === 'connected');
  const generation = useQuotaStore((state) => state.cacheGeneration);
  const revision = apiClient.getConnectionRevision();
  const scope = connected ? `${revision}/${generation}` : '';
  const [snapshot, setSnapshot] = useState<HistorySnapshot | undefined>(() =>
    connected && historySnapshot?.scope === scope ? historySnapshot : undefined
  );
  const visible =
    connected && snapshot?.scope === scope
      ? snapshot
      : connected && historySnapshot?.scope === scope
        ? historySnapshot
        : undefined;
  useEffect(() => {
    let active = true;
    let loading = false;
    let dirty = false;
    const client = apiClient.captureConnection();
    const controller = new AbortController();
    const current = () =>
      active &&
      connected &&
      revision === apiClient.getConnectionRevision() &&
      generation === useQuotaStore.getState().cacheGeneration &&
      useAuthStore.getState().connectionStatus === 'connected';
    setSnapshot(historySnapshot?.scope === scope ? historySnapshot : undefined);
    const load = async () => {
      if (!current()) return;
      if (loading) {
        dirty = true;
        return;
      }
      loading = true;
      try {
        const data = await readQuotaHistory(client, controller.signal);
        if (current()) {
          historySnapshot = { scope, observations: data, error: '' };
          setSnapshot(historySnapshot);
        }
      } catch (reason: unknown) {
        if (current()) setError(reason instanceof Error ? reason.message : String(reason));
      } finally {
        loading = false;
        if (dirty && active) {
          dirty = false;
          void load();
        }
      }
    };
    const setError = (error: string) => {
      const last = historySnapshot?.scope === scope ? historySnapshot.observations : [];
      setSnapshot({ scope, observations: last, error });
    };
    void load();
    const timer = window.setInterval(() => void load(), 60_000);
    window.addEventListener('quota-history-updated', load);
    const failed = () => {
      if (current()) setError(t('quota_history.collection_failed'));
    };
    window.addEventListener('quota-history-error', failed);
    return () => {
      active = false;
      controller.abort();
      window.clearInterval(timer);
      window.removeEventListener('quota-history-updated', load);
      window.removeEventListener('quota-history-error', failed);
    };
  }, [connected, generation, revision, scope, t]);
  return { observations: visible?.observations ?? [], error: visible?.error ?? '' };
}
