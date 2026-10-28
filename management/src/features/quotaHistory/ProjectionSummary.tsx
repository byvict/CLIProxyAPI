import { useTranslation } from 'react-i18next';
import type { QuotaProjection } from './projection';
import styles from './QuotaHistory.module.scss';

export function ProjectionSummary({
  projection,
  now,
}: {
  projection: QuotaProjection;
  now: number;
}) {
  const { t } = useTranslation();
  let label: string;
  switch (projection.status) {
    case 'projected':
      label = t('quota_history.forecast_before', {
        defaultValue: 'May run out in about {{hours}}h',
        hours: Math.max(0, Math.round((projection.exhaustsAtMs - now) / 3_600_000)),
      });
      break;
    case 'survives-reset':
      label = t('quota_history.forecast_survives', {
        defaultValue: 'Expected to last until reset',
      });
      break;
    case 'unstable':
      label = t('quota_history.forecast_unstable', {
        defaultValue: 'Changing pace · no reliable date',
      });
      break;
    case 'no-consumption':
      label = t('quota_history.forecast_no_drop', {
        defaultValue: 'No detectable drop in observed intervals',
      });
      break;
    case 'exhausted':
      label = t('quota_history.forecast_empty', {
        defaultValue: 'Quota exhausted',
      });
      break;
    case 'stale':
      label = t('quota_history.forecast_stale', {
        defaultValue: 'Estimate paused · stale reading',
      });
      break;
    case 'reset-passed':
      label = t('quota_history.forecast_reset', {
        defaultValue: 'Awaiting the new cycle',
      });
      break;
    case 'insufficient':
      label = t('quota_history.forecast_wait', {
        defaultValue: 'Collecting history for an estimate',
      });
      break;
    default: {
      const exhaustive: never = projection;
      return exhaustive;
    }
  }
  if (!('coverage' in projection)) return <p className={styles.projectionStatus}>{label}</p>;
  return (
    <details className={styles.projectionSummary} data-projection-status={projection.status}>
      <summary>
        {projection.provisional &&
          t('quota_history.forecast_provisional', {
            defaultValue: 'Provisional',
          })}
        {projection.provisional && ' · '}
        {label}
      </summary>
      <p>
        {t('quota_history.forecast_basis', {
          defaultValue:
            'Current cycle, up to 24h. {{observed}}h observed of {{requested}}h · {{coverage}}% coverage.',
          observed: projection.observedHours.toFixed(1),
          requested: projection.lookbackHours.toFixed(1),
          coverage: Math.round(projection.coverage * 100),
        })}
      </p>
      <p>
        {t('quota_history.forecast_rate', {
          defaultValue:
            '{{consumed}} p.p. consumed · {{rate}} p.p./h. Last reading {{age}} min ago.',
          consumed: projection.consumedPp.toFixed(1),
          rate: projection.ratePpH.toFixed(2),
          age: Math.max(0, Math.round((now - projection.observedAtMs) / 60_000)),
        })}
      </p>
      <p>
        {t('quota_history.forecast_assumption', {
          defaultValue:
            'Assumes the observed pace continues. Missing readings are unknown, not idle time. Coverage is not confidence.',
        })}
      </p>
    </details>
  );
}
