import type { Contribution, Dispatch, Report } from './data';

export const number = (v: number) => new Intl.NumberFormat('pt-BR').format(v);
export const percent = (v: number | null) =>
  v === null
    ? '—'
    : new Intl.NumberFormat('pt-BR', { style: 'percent', maximumFractionDigits: 1 }).format(v);
export const day = (v: string) =>
  new Intl.DateTimeFormat('en-CA', { timeZone: 'America/Sao_Paulo' }).format(new Date(v));
export const dateTime = (v: string) =>
  new Intl.DateTimeFormat('pt-BR', {
    dateStyle: 'short',
    timeStyle: 'short',
    timeZone: 'America/Sao_Paulo',
  }).format(new Date(v));
const familyNames: Record<string, string> = {
  gpt: 'GPT',
  claude: 'Claude',
  grok: 'Grok',
  muse: 'Muse',
  unknown: 'Desconhecida',
};
const roleNames: Record<string, string> = {
  review: 'Revisão',
  verification: 'Verificação',
  implementation: 'Implementação',
  research: 'Pesquisa',
  design: 'Design',
  unknown: 'Sem classificação',
};
const projectNames: Record<string, string> = {
  'f7c3140b-4bb3-4936-9a7f-983405f2702c': 'Clinext',
  '2291c2c1-6211-4fbb-85b3-d255fb03310d': 'CLIProxyAPI',
  '57a8ee62-4ba8-487f-937e-cb7a9b3d145f': 'Sem projeto',
};
export const familyName = (v: string) => familyNames[v] ?? v;
export const roleName = (v: string) => roleNames[v] ?? v;
export const projectName = (v: string) => projectNames[v] ?? v;

export interface Filters {
  from: string;
  to: string;
  project: string;
  role: string;
  model: string;
}
export type Basis = 'root' | 'dispatcher';
export function selectDispatches(report: Report, filter: Filters) {
  return report.dispatches.filter(
    (d) =>
      !d.audit_overhead &&
      (!filter.from || day(d.started_at) >= filter.from) &&
      (!filter.to || day(d.started_at) <= filter.to) &&
      (!filter.project || d.project_id === filter.project) &&
      (!filter.role || d.role === filter.role) &&
      (!filter.model || d.child.model === filter.model)
  );
}
export function indexReport(report: Report) {
  const evaluable = new Set(
    report.attribution_coverage.filter((v) => v.evaluable).map((v) => v.dispatch_id)
  );
  const contributions = new Map<string, Contribution[]>();
  for (const c of report.contributions)
    for (const d of c.discoverers) {
      const rows = contributions.get(d.dispatch_id) ?? [];
      rows.push(c);
      contributions.set(d.dispatch_id, rows);
    }
  return { evaluable, contributions };
}
export function totals(rows: Dispatch[], index: ReturnType<typeof indexReport>) {
  let evaluated = 0,
    accepted = 0,
    applied = 0,
    applicationUnknown = 0,
    notApplicable = 0;
  for (const d of rows) {
    if (!index.evaluable.has(d.dispatch_id)) continue;
    evaluated++;
    const cs = index.contributions.get(d.dispatch_id) ?? [];
    if (cs.some((c) => c.accepted === true || c.applied === true)) accepted++;
    if (cs.some((c) => c.applied === true)) applied++;
    else if (cs.length && cs.every((c) => c.kind === 'verification_artifact')) notApplicable++;
    else if (cs.some((c) => c.accepted === true && c.applied === null)) applicationUnknown++;
  }
  const applicationEvaluated = evaluated - applicationUnknown - notApplicable;
  return {
    dispatched: rows.length,
    evaluated,
    accepted,
    applied,
    applicationUnknown,
    notApplicable,
    applicationEvaluated,
    unknown: rows.length - evaluated,
    rate: evaluated ? accepted / evaluated : null,
    appliedRate: applicationEvaluated ? applied / applicationEvaluated : null,
  };
}
export function matrix(rows: Dispatch[], index: ReturnType<typeof indexReport>, basis: Basis) {
  const parents = [...new Set(rows.map((d) => d[basis].family))].sort();
  const children = [...new Set(rows.map((d) => d.child.family))].sort();
  return {
    parents,
    children,
    cells: parents.map((parent) =>
      children.map((child) => ({
        parent,
        child,
        ...totals(
          rows.filter((d) => d[basis].family === parent && d.child.family === child),
          index
        ),
      }))
    ),
  };
}
export function relationships(
  rows: Dispatch[],
  index: ReturnType<typeof indexReport>,
  basis: Basis
) {
  return [...new Set(rows.map((d) => d.role))].sort().map((role) => {
    const known = rows.filter(
      (d) => d.role === role && d[basis].family !== 'unknown' && d.child.family !== 'unknown'
    );
    return {
      role,
      same: totals(
        known.filter((d) => d[basis].family === d.child.family),
        index
      ),
      cross: totals(
        known.filter((d) => d[basis].family !== d.child.family),
        index
      ),
    };
  });
}
export function panelMetrics(report: Report, rows: Dispatch[]) {
  const ids = new Set(rows.map((d) => d.dispatch_id));
  return report.panels
    .filter((p) => p.complete && p.dispatch_ids.every((id) => ids.has(id)))
    .map((p) => {
      const cs = report.contributions.filter(
        (c) => c.panel_id === p.panel_id && (c.accepted === true || c.applied === true)
      );
      const families = p.expected_families.map((family) => ({
        family,
        exclusive: cs.filter((c) => c.discoverers.every((d) => d.child_family === family)).length,
        shared: cs.filter(
          (c) =>
            c.discoverers.some((d) => d.child_family === family) &&
            c.discoverers.some((d) => d.child_family !== family)
        ).length,
      }));
      return {
        ...p,
        families,
        union: cs.length,
        allExclusive: families.every((f) => f.exclusive > 0),
      };
    });
}

export function familyContributions(panels: ReturnType<typeof panelMetrics>) {
  const families = new Map<
    string,
    { family: string; exclusive: number; shared: number; panels: number }
  >();
  for (const panel of panels) {
    for (const entry of panel.families) {
      const total = families.get(entry.family) ?? {
        family: entry.family,
        exclusive: 0,
        shared: 0,
        panels: 0,
      };
      total.exclusive += entry.exclusive;
      total.shared += entry.shared;
      total.panels++;
      families.set(entry.family, total);
    }
  }
  return [...families.values()].sort((a, b) =>
    a.family.localeCompare(b.family),
  );
}
