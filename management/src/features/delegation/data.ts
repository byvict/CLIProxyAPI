export interface Identity {
  family: string;
  model: string | null;
}
export interface Dispatch {
  dispatch_id: string;
  project_id: string;
  started_at: string;
  status: string;
  role: string;
  round: string | null;
  root: Identity;
  dispatcher: Identity;
  child: Identity;
  root_thread_id: string;
  child_thread_id: string | null;
  audit_overhead: boolean;
}
export interface Coverage {
  dispatch_id: string;
  evaluable: boolean;
  evidence_ids: string[] | null;
}
export interface Contribution {
  contribution_id: string;
  panel_id: string | null;
  label: string;
  kind: string;
  severity: string;
  accepted: boolean | null;
  applied: boolean | null;
  disposition: string;
  discoverers: { dispatch_id: string; child_family: string }[];
  decision_evidence_ids: string[] | null;
  artifact_evidence_ids: string[] | null;
}
export interface Panel {
  panel_id: string;
  complete: boolean;
  dispatch_ids: string[];
  expected_families: string[];
  scope: string;
  independence_limit: string;
}
export interface Evidence {
  evidence_id: string;
  kind: string;
  thread_id?: string;
  source_item_id?: string;
  source_id?: string;
  base?: string;
  head?: string;
  sha256?: string;
}
export interface Report {
  schema_version: string;
  generation_id: string;
  source_as_of: string;
  disposition_as_of: string;
  window: { start_inclusive: string; end_inclusive: string };
  coverage: { dispatch_census: string; attribution_scope: string };
  dispatches: Dispatch[];
  attribution_coverage: Coverage[];
  contributions: Contribution[];
  panels: Panel[];
  evidence: Evidence[];
}
export type Response = { status: 'unavailable' } | { status: 'ready'; report: Report };

const record = (v: unknown): v is Record<string, unknown> =>
  typeof v === 'object' && v !== null && !Array.isArray(v);
const text = (v: unknown): v is string => typeof v === 'string';
const nullableText = (v: unknown): v is string | null => v === null || text(v);
const nullableBool = (v: unknown): v is boolean | null => v === null || typeof v === 'boolean';
const list = <T>(v: unknown, guard: (value: unknown) => value is T): v is T[] =>
  Array.isArray(v) && v.every(guard);
const textList = (v: unknown): v is string[] => list(v, text);
const nullableList = (v: unknown): v is string[] | null => v === null || textList(v);
const identity = (v: unknown): v is Identity =>
  record(v) && text(v.family) && nullableText(v.model);
const timestamp = (v: unknown): v is string => text(v) && Number.isFinite(Date.parse(v));
function dispatch(v: unknown): v is Dispatch {
  return (
    record(v) &&
    text(v.dispatch_id) &&
    text(v.project_id) &&
    timestamp(v.started_at) &&
    text(v.status) &&
    text(v.role) &&
    nullableText(v.round) &&
    identity(v.root) &&
    identity(v.dispatcher) &&
    identity(v.child) &&
    text(v.root_thread_id) &&
    nullableText(v.child_thread_id) &&
    typeof v.audit_overhead === 'boolean'
  );
}
function coverage(v: unknown): v is Coverage {
  return (
    record(v) &&
    text(v.dispatch_id) &&
    typeof v.evaluable === 'boolean' &&
    nullableList(v.evidence_ids)
  );
}
function discoverer(v: unknown): v is Contribution['discoverers'][number] {
  return record(v) && text(v.dispatch_id) && text(v.child_family);
}
function contribution(v: unknown): v is Contribution {
  return (
    record(v) &&
    text(v.contribution_id) &&
    nullableText(v.panel_id) &&
    text(v.label) &&
    text(v.kind) &&
    text(v.severity) &&
    nullableBool(v.accepted) &&
    nullableBool(v.applied) &&
    text(v.disposition) &&
    list(v.discoverers, discoverer) &&
    nullableList(v.decision_evidence_ids) &&
    nullableList(v.artifact_evidence_ids)
  );
}
function panel(v: unknown): v is Panel {
  return (
    record(v) &&
    text(v.panel_id) &&
    typeof v.complete === 'boolean' &&
    textList(v.dispatch_ids) &&
    textList(v.expected_families) &&
    text(v.scope) &&
    text(v.independence_limit)
  );
}
function evidence(v: unknown): v is Evidence {
  return (
    record(v) &&
    text(v.evidence_id) &&
    text(v.kind) &&
    ['thread_id', 'source_item_id', 'source_id', 'base', 'head', 'sha256'].every(
      (key) => v[key] === undefined || text(v[key])
    )
  );
}
function report(v: unknown): v is Report {
  return (
    record(v) &&
    v.schema_version === 't3-observatory/1.0' &&
    text(v.generation_id) &&
    timestamp(v.source_as_of) &&
    timestamp(v.disposition_as_of) &&
    record(v.window) &&
    timestamp(v.window.start_inclusive) &&
    timestamp(v.window.end_inclusive) &&
    record(v.coverage) &&
    text(v.coverage.dispatch_census) &&
    text(v.coverage.attribution_scope) &&
    list(v.dispatches, dispatch) &&
    list(v.attribution_coverage, coverage) &&
    list(v.contributions, contribution) &&
    list(v.panels, panel) &&
    list(v.evidence, evidence)
  );
}
export function parseDelegation(v: unknown): Response {
  if (record(v) && v.status === 'unavailable') return { status: 'unavailable' };
  if (record(v) && v.status === 'ready' && report(v.report))
    return { status: 'ready', report: v.report };
  throw new Error('O relatório de agentes está incompleto ou usa um formato incompatível.');
}
