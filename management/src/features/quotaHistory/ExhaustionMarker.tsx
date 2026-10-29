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
  if (projection.status !== 'projected' && projection.status !== 'survives-reset') return null;
  const left = ((projection.exhaustsAtMs - startMs) / (endMs - startMs)) * 100;
  const title =
    t('quota_history.forecast_marker', {
      defaultValue:
        'Approximate exhaustion: {{at}} (São Paulo), if the observed pace continues · {{rate}} p.p./h.',
      at: new Date(projection.exhaustsAtMs).toLocaleString(i18n.resolvedLanguage, {
        timeZone: 'America/Sao_Paulo',
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
      }),
      offset: ((projection.exhaustsAtMs - projection.resetAtMs) / 3_600_000).toFixed(1),
      hours: projection.lookbackHours.toFixed(1),
      rate: projection.ratePpH.toFixed(2),
    }) +
    ` · ${t('quota_history.forecast_observed', { defaultValue: 'Current cycle: {{observed}}h observed of {{hours}}h · {{coverage}}% coverage', hours: projection.lookbackHours.toFixed(1), observed: projection.observedHours.toFixed(1), coverage: (projection.coverage * 100).toFixed(0) })}` +
    (projection.status === 'survives-reset'
      ? ` · ${t('quota_history.forecast_after_reset', { defaultValue: 'Projection ignores the scheduled quota reset.' })}`
      : '') +
    (projection.provisional
      ? ` · ${t('quota_history.forecast_provisional', { defaultValue: 'Provisional' })}`
      : '');
  return (
    <span
      className={`${styles.timeMarker} ${styles.exhaustionMarker}`}
      data-band={projection.band}
      data-provisional={projection.provisional || undefined}
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
