import { useTranslation } from 'react-i18next';
import type { QuotaProjection } from './projection';
import styles from './QuotaHistory.module.scss';

export function ExhaustionMarker({
  projection,
  startMs,
  endMs,
}: {
  projection: QuotaProjection;
  startMs: number;
  endMs: number;
}) {
  const { t, i18n } = useTranslation();
  if (projection.status !== 'projected') return null;
  const left = ((projection.exhaustsAtMs - startMs) / (endMs - startMs)) * 100;
  const title =
    t('quota_history.marker_detail', {
      at: new Date(projection.exhaustsAtMs).toLocaleString(i18n.resolvedLanguage, {
        timeZone: 'America/Sao_Paulo',
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
      }),
      offset: ((projection.exhaustsAtMs - projection.resetAtMs) / 3_600_000).toFixed(1),
      hours: projection.lookbackHours.toFixed(1),
      rate: projection.ratePpH.toFixed(2),
    }) +
    ` · ${t('quota_history.marker_coverage', { observed: projection.observedHours.toFixed(1), coverage: (projection.coverage * 100).toFixed(0) })}`;
  return (
    <span
      className={`${styles.timeMarker} ${styles.exhaustionMarker}`}
      data-band={projection.band}
      data-coverage={projection.coverage.toFixed(3)}
      data-outside={left < 0 ? 'before' : left > 100 ? 'after' : undefined}
      style={{ left: `${Math.min(99.8, Math.max(0.2, left))}%` }}
      role="img"
      tabIndex={0}
      aria-label={title}
      title={title}
    />
  );
}
