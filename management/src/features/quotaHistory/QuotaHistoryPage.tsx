import { useEffect, useRef } from 'react';
import { useQuotaStore } from '@/stores';
import { apiClient } from '@/services/api/client';
import { mountV1History } from './v1Runtime.js';
import styles from './v1Style.css?inline';

export function QuotaHistoryPage() {
  const host = useRef<HTMLDivElement>(null);
  const generation = useQuotaStore((state) => state.cacheGeneration);
  useEffect(() => {
    if (!host.current) return;
    const shadow = host.current.shadowRoot ?? host.current.attachShadow({ mode: 'open' });
    shadow.innerHTML = `<style>${styles}</style><div class="cp-root" id="cp-monitor">
      <main id="screen" aria-live="polite"></main>
      <div class="tooltip" id="chart-tooltip" role="tooltip"></div>
      <div class="refresh-status" id="refresh-status" role="status" hidden><span></span><button class="btn btn-small" data-action="apply-pending">Aplicar dados</button></div>
      <div class="toast" id="toast" role="status"></div><div id="modal-backdrop" hidden></div>
    </div>`;
    const client = apiClient.captureConnection();
    return mountV1History(
      shadow,
      (path, options) => {
        if (path === '/api/v1/snapshot') return client.get('/quota-history/snapshot', { signal: options?.signal, timeout: 0 });
        if (path.startsWith('/api/v1/view?'))
          return client.get('/quota-history/view?' + path.split('?')[1], { signal: options?.signal, timeout: 0 });
        return Promise.reject(new Error('Unsupported history request'));
      },
      () => window.dispatchEvent(new Event('quota-history-refresh'))
    );
  }, [generation]);
  return <div ref={host} data-quota-history="v1" />;
}
