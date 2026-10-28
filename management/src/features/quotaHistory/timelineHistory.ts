import { buildTimelineLane, pickLaneWindow } from '@/features/quota/quotaTimelineModel';
import type { TimelineLane, TimelineLaneInput } from '@/features/quota/quotaTimelineModel';
import { normalizeAuthIndex } from '@/utils/authIndex';
import type { QuotaObservation } from './projection';

export function historyTimelineLane(
  input: TimelineLaneInput,
  credential: string | null,
  observations: readonly QuotaObservation[],
  now: number
): TimelineLane {
  const live = buildTimelineLane(input);
  if ((input.quota?.retained ?? input.quota)?.status === 'success' || !credential) return live;

  const latest = new Map<string, QuotaObservation>();
  for (const sample of observations) {
    if (
      normalizeAuthIndex(sample.credential) !== credential ||
      sample.provider !== input.provider ||
      sample.observedAtMs > now
    )
      continue;
    const previous = latest.get(sample.windowId);
    if (!previous || sample.observedAtMs >= previous.observedAtMs)
      latest.set(sample.windowId, sample);
  }
  const windows = [...latest.values()].filter(
    (window) =>
      window.remaining !== null &&
      window.resetAtMs !== null &&
      window.periodHours !== null &&
      Number.isFinite(window.periodHours) &&
      window.periodHours > 0
  );
  const preferredId =
    input.maxPeriodHours !== undefined && input.maxPeriodHours <= 5 ? 'five-hour' : 'weekly';
  const preferred =
    input.provider === 'codex'
      ? windows.find(
          (window) =>
            window.windowId === preferredId &&
            window.resetAtMs !== null &&
            window.periodHours !== null &&
            window.periodHours > 0 &&
            (input.maxPeriodHours === undefined || window.periodHours <= input.maxPeriodHours)
        )
      : undefined;
  const chosen = preferred ?? pickLaneWindow(windows, input.maxPeriodHours);
  if (!chosen) return live;
  const remaining =
    chosen.resetAtMs !== null && chosen.resetAtMs > now && chosen.remaining !== null
      ? Math.min(100, Math.max(0, chosen.remaining))
      : null;
  return {
    ...live,
    windowId: chosen.windowId,
    anchorMs: chosen.resetAtMs,
    periodHours: chosen.periodHours,
    remaining,
    recordedAtMs: chosen.observedAtMs,
    limits: [],
  };
}
