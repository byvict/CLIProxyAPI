import { describe, expect, test } from 'bun:test';
import { parseDelegation, type Dispatch, type Report } from '../src/features/delegation/data';
import {
  familyContributions,
  indexReport,
  matrix,
  panelMetrics,
  relationships,
  selectDispatches,
  totals,
} from '../src/features/delegation/metrics';

function dispatch(id: string, family: string): Dispatch {
  return {
    dispatch_id: id,
    project_id: 'project',
    started_at: '2026-10-28T02:00:00Z',
    status: 'completed',
    role: 'review',
    round: 'initial_review',
    root: { family: 'gpt', model: 'gpt-6' },
    dispatcher: { family: 'claude', model: 'claude-opus' },
    child: { family, model: family },
    root_thread_id: 'root',
    child_thread_id: id,
    audit_overhead: false,
  };
}
const report: Report = {
  schema_version: 't3-observatory/1.0',
  generation_id: 'generation',
  source_as_of: '2026-10-28T12:00:00Z',
  disposition_as_of: '2026-10-28T12:00:00Z',
  window: { start_inclusive: '2026-10-21T00:00:00Z', end_inclusive: '2026-10-28T12:00:00Z' },
  coverage: { dispatch_census: 'local', attribution_scope: 'bounded' },
  dispatches: [
    dispatch('a', 'gpt'),
    dispatch('b', 'claude'),
    dispatch('c', 'muse'),
    dispatch('unknown', 'grok'),
    { ...dispatch('audit', 'gpt'), audit_overhead: true },
  ],
  attribution_coverage: ['a', 'b', 'c'].map((dispatch_id) => ({
    dispatch_id,
    evaluable: true,
    evidence_ids: [],
  })),
  contributions: [
    {
      contribution_id: 'one',
      panel_id: 'panel',
      label: 'A shared missing check',
      kind: 'test_coverage',
      severity: 'warning',
      accepted: true,
      applied: null,
      disposition: 'accepted',
      discoverers: [
        { dispatch_id: 'a', child_family: 'gpt' },
        { dispatch_id: 'b', child_family: 'claude' },
      ],
      decision_evidence_ids: [],
      artifact_evidence_ids: [],
    },
  ],
  panels: [
    {
      panel_id: 'panel',
      complete: true,
      dispatch_ids: ['a', 'b', 'c'],
      expected_families: ['gpt', 'claude', 'muse'],
      scope: 'initial',
      independence_limit: 'root asserted',
    },
  ],
  evidence: [],
};
const all = { from: '', to: '', role: '', model: '', project: '' };
describe('Delegation evidence semantics', () => {
  test('family contributions consolidate complete panels without treating shared findings as globally distinct', () => {
    const data: Report = {
      ...report,
      dispatches: [
        ...report.dispatches,
        dispatch('a2', 'gpt'),
        dispatch('b2', 'claude'),
        dispatch('c2', 'muse'),
      ],
      contributions: [
        ...report.contributions,
        {
          ...report.contributions[0],
          contribution_id: 'two',
          panel_id: 'second',
          discoverers: [{ dispatch_id: 'c2', child_family: 'muse' }],
        },
      ],
      panels: [
        ...report.panels,
        {
          ...report.panels[0],
          panel_id: 'second',
          dispatch_ids: ['a2', 'b2', 'c2'],
        },
      ],
    };
    const panels = panelMetrics(data, selectDispatches(data, all));
    expect(familyContributions(panels)).toEqual([
      { family: 'claude', exclusive: 0, shared: 1, panels: 2 },
      { family: 'gpt', exclusive: 0, shared: 1, panels: 2 },
      { family: 'muse', exclusive: 1, shared: 0, panels: 2 },
    ]);
    expect(panels.reduce((sum, panel) => sum + panel.union, 0)).toBe(2);
  });
  test('family aggregates stay unavailable when filters leave no complete panel', () => {
    expect(
      familyContributions(
        panelMetrics(
          report,
          selectDispatches(report, { ...all, model: 'gpt' }),
        ),
      ),
    ).toEqual([]);
  });
  test('implementation artifacts still need inspected application evidence', () => {
    const data: Report = { ...report, contributions: [{ ...report.contributions[0], kind: 'implementation_artifact' }] };
    const result = totals(selectDispatches(data, all), indexReport(data));
    expect(result.applicationUnknown).toBe(2);
    expect(result.notApplicable).toBe(0);
  });
  test('unreviewed dispatches and audit overhead never enter the evaluable denominator', () => {
    const rows = selectDispatches(report, all),
      result = totals(rows, indexReport(report));
    expect(result.dispatched).toBe(4);
    expect(result.evaluated).toBe(3);
    expect(result.unknown).toBe(1);
    expect(result.accepted).toBe(2);
    expect(result.rate).toBe(2 / 3);
    expect(result.applicationUnknown).toBe(2);
    expect(result.applied).toBe(0);
    expect(result.applicationEvaluated).toBe(1);
  });
  test('inspected work with unknown utilization stays distinct from unread work and rate denominators', () => {
    const data: Report = {
      ...report,
      dispatches: [...report.dispatches, dispatch('unread', 'grok')],
      attribution_coverage: [
        ...report.attribution_coverage,
        { dispatch_id: 'unknown', evaluable: false, evidence_ids: [] },
        { dispatch_id: 'audit', evaluable: false, evidence_ids: [] },
      ],
    };
    const result = totals(selectDispatches(data, all), indexReport(data));
    expect(result.dispatched).toBe(5);
    expect(result.inspected).toBe(4);
    expect(result.uninspected).toBe(1);
    expect(result.unresolved).toBe(1);
    expect(result.evaluated).toBe(3);
    expect(result.rate).toBe(2 / 3);
    const filtered = totals(
      selectDispatches(data, { ...all, model: 'grok' }),
      indexReport(data),
    );
    expect(filtered.inspected).toBe(1);
    expect(filtered.uninspected).toBe(1);
    expect(filtered.unresolved).toBe(1);
    expect(filtered.rate).toBeNull();
  });
  test('a family without inspected outcomes has a null rate, not a zero efficiency', () => {
    const rows = selectDispatches(report, { ...all, model: 'grok' });
    expect(totals(rows, indexReport(report)).rate).toBeNull();
    expect(matrix(rows, indexReport(report), 'root').cells[0][0].rate).toBeNull();
  });
  test('filtering a panel member out removes the whole panel from exclusive denominators', () => {
    const rows = selectDispatches(report, all),
      panels = panelMetrics(report, rows);
    expect(panels[0].union).toBe(1);
    expect(panels[0].allExclusive).toBe(false);
    expect(panels[0].families.find((f) => f.family === 'gpt')?.shared).toBe(1);
    expect(panelMetrics(report, selectDispatches(report, { ...all, model: 'gpt' }))).toHaveLength(
      0
    );
  });
  test('root and immediate dispatcher families are independent dimensions', () => {
    const rows = selectDispatches(report, { ...all, model: 'gpt' });
    expect(relationships(rows, indexReport(report), 'root')[0].same.dispatched).toBe(1);
    expect(relationships(rows, indexReport(report), 'dispatcher')[0].cross.dispatched).toBe(1);
  });
  test('date filters use Sao Paulo dispatch dates and preserve the unknown set', () => {
    expect(selectDispatches(report, { ...all, from: '2026-10-28' })).toHaveLength(0);
    expect(selectDispatches(report, { ...all, from: '2026-10-27', to: '2026-10-27' })).toHaveLength(
      4
    );
  });
  test('used verification artifacts are not mistaken for unapplied code changes', () => {
    const data: Report = {
      ...report,
      contributions: [
        {
          ...report.contributions[0],
          kind: 'verification_artifact',
          discoverers: [{ dispatch_id: 'a', child_family: 'gpt' }],
        },
      ],
    };
    const result = totals(selectDispatches(data, all), indexReport(data));
    expect(result.accepted).toBe(1);
    expect(result.notApplicable).toBe(1);
    expect(result.applicationUnknown).toBe(0);
  });
  test('malformed or old exports without evaluable dispatch identities fail at the boundary', () => {
    expect(parseDelegation({ status: 'ready', report })).toEqual({ status: 'ready', report });
    expect(() =>
      parseDelegation({ status: 'ready', report: { ...report, attribution_coverage: undefined } })
    ).toThrow();
    expect(() =>
      parseDelegation({
        status: 'ready',
        report: { ...report, contributions: [{ ...report.contributions[0], applied: 'true' }] },
      })
    ).toThrow();
    expect(parseDelegation({ status: 'unavailable' })).toEqual({ status: 'unavailable' });
  });
});
