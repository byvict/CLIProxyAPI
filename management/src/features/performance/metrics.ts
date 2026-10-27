import type { Activity, ActivityDay, Provider } from './api';

export const emptyDay = (date: string): ActivityDay => ({
  date,
  agents: 0,
  tokens: 0,
  tokenRecords: 0,
  reportedAgents: 0,
  partialAgents: 0,
  longestDurationMs: 0,
});
export function addDays(date: string, count: number): string {
  const value = new Date(`${date}T12:00:00Z`);
  value.setUTCDate(value.getUTCDate() + count);
  return value.toISOString().slice(0, 10);
}
export function selectActivity(activity: Activity, provider: Provider) {
  const byDate = new Map<string, ActivityDay>();
  for (const entry of activity.providers) {
    if (provider !== 'all' && entry.id !== provider) continue;
    for (const day of entry.days) {
      const total = byDate.get(day.date) ?? emptyDay(day.date);
      total.agents += day.agents;
      total.tokens += day.tokens;
      total.tokenRecords = (total.tokenRecords ?? 0) + (day.tokenRecords ?? 0);
      total.reportedAgents += day.reportedAgents;
      total.partialAgents += day.partialAgents;
      total.longestDurationMs = Math.max(total.longestDurationMs, day.longestDurationMs);
      byDate.set(day.date, total);
    }
  }
  const days = [...byDate.values()].sort((a, b) => a.date.localeCompare(b.date));
  const total = days.reduce(
    (sum, day) => ({
      ...sum,
      agents: sum.agents + day.agents,
      tokens: sum.tokens + day.tokens,
      tokenRecords: (sum.tokenRecords ?? 0) + (day.tokenRecords ?? 0),
      reportedAgents: sum.reportedAgents + day.reportedAgents,
      partialAgents: sum.partialAgents + day.partialAgents,
      longestDurationMs: Math.max(sum.longestDurationMs, day.longestDurationMs),
    }),
    emptyDay('')
  );
  let longestStreak = 0;
  let streak = 0;
  let previous = '';
  for (const day of days) {
    if (!day.agents && !day.tokens) continue;
    streak = previous && addDays(previous, 1) === day.date ? streak + 1 : 1;
    longestStreak = Math.max(longestStreak, streak);
    previous = day.date;
  }
  let currentStreak = 0;
  const isActive = (date: string) => Boolean(byDate.get(date)?.agents || byDate.get(date)?.tokens);
  let cursor = isActive(activity.today) ? activity.today : addDays(activity.today, -1);
  while (isActive(cursor)) {
    currentStreak++;
    cursor = addDays(cursor, -1);
  }
  const recent = Array.from({ length: 30 }, (_, index) => {
    const date = addDays(activity.today, index - 29);
    return byDate.get(date) ?? emptyDay(date);
  });
  return { days, total, longestStreak, currentStreak, recent };
}

export function calendarDays(year: number) {
  const start = `${year}-01-01`;
  const offset = new Date(`${start}T12:00:00Z`).getUTCDay();
  const result: (string | null)[] = Array.from({ length: offset }, () => null);
  for (let date = start; date.startsWith(`${year}-`); date = addDays(date, 1)) result.push(date);
  while (result.length % 7) result.push(null);
  return result;
}
export function intensity(value: number, maximum: number) {
  return value <= 0 || maximum <= 0
    ? 0
    : Math.min(4, Math.max(1, Math.ceil((value / maximum) * 4)));
}
export const compact = (value: number) =>
  new Intl.NumberFormat('en', { notation: 'compact', maximumFractionDigits: 1 }).format(value);
export const number = (value: number) => new Intl.NumberFormat('pt-BR').format(value);
export const shortDate = (date: string) =>
  new Date(`${date}T12:00:00Z`).toLocaleDateString('pt-BR', {
    day: 'numeric',
    month: 'short',
    timeZone: 'America/Sao_Paulo',
  });
export function duration(ms: number) {
  if (!ms) return '—';
  if (ms >= 86_400_000) return `${(ms / 86_400_000).toFixed(1)}d`;
  if (ms >= 3_600_000) return `${(ms / 3_600_000).toFixed(1)}h`;
  return `${Math.max(1, Math.round(ms / 60_000))}m`;
}
