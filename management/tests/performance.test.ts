import { describe, expect, test } from 'bun:test';
import { calendarDays, emptyDay, selectActivity } from '../src/features/performance/metrics';
import { parseGitHub, parsePerformance, type Activity } from '../src/features/performance/api';

const activity: Activity = {
  year: 2026,
  today: '2026-10-27',
  timezone: 'America/Sao_Paulo',
  generatedAt: '2026-10-27T12:00:00-03:00',
  availableFrom: '2026-10-24',
  providers: [
    {
      id: 'claude',
      days: [
        {
          ...emptyDay('2026-10-24'),
          agents: 2,
          tokens: 200,
          reportedAgents: 2,
          longestDurationMs: 500,
        },
        { ...emptyDay('2026-10-26'), agents: 1, tokens: 100, reportedAgents: 1 },
      ],
    },
    {
      id: 'codex',
      days: [
        {
          ...emptyDay('2026-10-25'),
          agents: 1,
          tokens: 300,
          reportedAgents: 1,
          longestDurationMs: 1000,
        },
      ],
    },
    { id: 'grok', days: [{ ...emptyDay('2026-10-27'), agents: 1 }] },
    {
      id: 'meta',
      days: [
        { ...emptyDay('2026-10-27'), agents: 1, tokens: 50, reportedAgents: 1, partialAgents: 1 },
      ],
    },
  ],
};
describe('Performance activity', () => {
  test('Todos sums each provider once and computes the union of active days', () => {
    const all = selectActivity(activity, 'all');
    expect(all.total.agents).toBe(6);
    expect(all.total.tokens).toBe(650);
    expect(all.total.reportedAgents).toBe(5);
    expect(all.total.partialAgents).toBe(1);
    expect(all.total.longestDurationMs).toBe(1000);
    expect(all.longestStreak).toBe(4);
    expect(all.currentStreak).toBe(4);
    expect(all.recent).toHaveLength(30);
    expect(all.recent[29].date).toBe(activity.today);
    expect(selectActivity(activity, 'claude').total.tokens).toBe(300);
    expect(selectActivity(activity, 'claude').currentStreak).toBe(1);
    expect(selectActivity(activity, 'codex').currentStreak).toBe(0);
  });
  test('aggregation does not mutate provider readings', () => {
    const original = JSON.stringify(activity);
    selectActivity(activity, 'all');
    selectActivity(activity, 'all');
    expect(JSON.stringify(activity)).toBe(original);
  });
  test('missing Grok token usage stays distinct from reported zero tokens', () => {
    const grok = selectActivity(activity, 'grok');
    expect(grok.total.agents).toBe(1);
    expect(grok.total.reportedAgents).toBe(0);
    expect(grok.total.tokens).toBe(0);
  });
  test('calendar covers complete leap years and weeks', () => {
    const days = calendarDays(2024);
    expect(days.filter(Boolean)).toHaveLength(366);
    expect(days).toContain('2024-02-29');
    expect(days.length % 7).toBe(0);
  });
  test('malformed payloads and negative metrics are rejected', () => {
    expect(parsePerformance({ status: 'ready', activity })).toEqual({ status: 'ready', activity });
    expect(() =>
      parsePerformance({
        status: 'ready',
        activity: {
          ...activity,
          providers: [{ id: 'codex', days: [{ ...emptyDay('2026-10-27'), tokens: -1 }] }],
        },
      })
    ).toThrow();
    expect(parseGitHub({ status: 'disconnected' })).toEqual({ status: 'disconnected' });
    expect(() => parseGitHub({ status: 'connected', activity: { login: 'user' } })).toThrow();
  });
});
