import { useMemo, useState } from "react";
import { Button } from "@/components/ui/Button";
import { IconRefreshCw } from "@/components/ui/icons";
import { useActivity } from "@/features/performance/api";
import { parseDelegation, type Report } from "./data";
import {
  dateTime,
  day,
  familyName,
  familyContributions,
  indexReport,
  matrix,
  number,
  panelMetrics,
  percent,
  projectName,
  relationships,
  roleName,
  selectDispatches,
  totals,
  type Basis,
  type Filters,
} from "./metrics";
import styles from "./Delegation.module.scss";

export function DelegationPage() {
  const { state, refresh, refreshing } = useActivity("/delegation", parseDelegation);
  return (
    <div className={styles.page}>
      <header className={styles.heading}>
        <div>
          <h1>Agentes</h1>
          <p>Despachos reais e contribuições aproveitadas.</p>
        </div>
        <Button
          variant="secondary"
          size="sm"
          onClick={refresh}
          disabled={state.status === "loading" || refreshing}
        >
          <IconRefreshCw size={15} /> Atualizar
        </Button>
      </header>
      {refreshing && <p role="status">Atualizando…</p>}
      {state.status === "ready" && state.refreshError && <div className="error-box" role="alert">{state.refreshError}</div>}
      {state.status === "loading" && (
        <p role="status">Carregando acompanhamento…</p>
      )}
      {state.status === "error" && (
        <div className="error-box" role="alert">
          {state.message} <button onClick={refresh}>Tentar novamente</button>
        </div>
      )}
      {state.status === "ready" &&
        (state.data.status === "unavailable" ? (
          <div className={styles.empty}>
            <h2>A coleta ainda não está disponível</h2>
            <p>
              A rotina publicará os dados após validar o histórico. A ausência
              do relatório não significa ausência de trabalho.
            </p>
          </div>
        ) : (
          <Dashboard report={state.data.report} />
        ))}
    </div>
  );
}

function Dashboard({ report }: { report: Report }) {
  const [filters, setFilters] = useState<Filters>({
    from: "",
    to: "",
    project: "",
    role: "",
    model: "",
  });
  const [basis, setBasis] = useState<Basis>("root");
  const index = useMemo(() => indexReport(report), [report]);
  const work = useMemo(
    () => report.dispatches.filter((d) => !d.audit_overhead),
    [report],
  );
  const rows = useMemo(
    () => selectDispatches(report, filters),
    [report, filters],
  );
  const summary = totals(rows, index);
  const global = totals(work, index);
  const cells = matrix(rows, index, basis);
  const groups = relationships(rows, index, basis);
  const panels = panelMetrics(report, rows);
  const families = familyContributions(panels);
  const contributionScale = Math.max(
    1,
    ...families.flatMap((f) => [f.exclusive, f.shared]),
  );
  const distinctContributions = panels.reduce(
    (total, panel) => total + panel.union,
    0,
  );
  const allExclusive = panels.filter((p) => p.allExclusive).length;
  const roles = [...new Set(work.map((d) => d.role))].sort();
  const projects = [...new Set(work.map((d) => d.project_id))].sort();
  const models = [
    ...new Set(work.flatMap((d) => (d.child.model ? [d.child.model] : []))),
  ].sort();
  const update = (key: keyof Filters, value: string) =>
    setFilters((prior) => ({ ...prior, [key]: value }));
  return (
    <main aria-label="Acompanhamento dos agentes">
      <p className={styles.period}>
        Coleta: {dateTime(report.window.start_inclusive)} —{" "}
        {dateTime(report.window.end_inclusive)} · São Paulo
      </p>
      <aside className={styles.coverage} aria-label="Cobertura da auditoria">
        <strong>
          {number(global.dispatched)} despachos identificados.{" "}
          {number(global.inspected)} analisados.
        </strong>
        <p>
          Nos analisados, os resultados e as decisões disponíveis foram conferidos.{" "}
          Há {number(global.evaluated)} com aproveitamento avaliável e{" "}
          {number(global.unresolved)} com aproveitamento ainda desconhecido.
          Faltam analisar {number(global.uninspected)} despachos.
        </p>
        <progress
          max={Math.max(1, global.dispatched)}
          value={global.inspected}
          aria-label="Cobertura da análise"
        />
      </aside>
      <div className={styles.filters}>
        <label>
          De
          <input
            type="date"
            value={filters.from}
            min={day(report.window.start_inclusive)}
            max={day(report.window.end_inclusive)}
            onChange={(e) => update("from", e.target.value)}
          />
        </label>
        <label>
          Até
          <input
            type="date"
            value={filters.to}
            min={day(report.window.start_inclusive)}
            max={day(report.window.end_inclusive)}
            onChange={(e) => update("to", e.target.value)}
          />
        </label>
        <label>
          Projeto
          <select
            aria-label="Projeto"
            value={filters.project}
            onChange={(e) => update("project", e.target.value)}
          >
            <option value="">Todos</option>
            {projects.map((p) => (
              <option key={p} value={p}>
                {projectName(p)}
              </option>
            ))}
          </select>
        </label>
        <label>
          Cadeira
          <select
            aria-label="Cadeira"
            value={filters.role}
            onChange={(e) => update("role", e.target.value)}
          >
            <option value="">Todas</option>
            {roles.map((role) => (
              <option key={role} value={role}>
                {roleName(role)}
              </option>
            ))}
          </select>
        </label>
        <label>
          Modelo delegado
          <select
            aria-label="Modelo delegado"
            value={filters.model}
            onChange={(e) => update("model", e.target.value)}
          >
            <option value="">Todos</option>
            {models.map((m) => (
              <option key={m}>{m}</option>
            ))}
          </select>
        </label>
      </div>
      {filters.from && filters.to && filters.from > filters.to && (
        <p role="alert">A data inicial deve ser anterior à final.</p>
      )}
      <div className={styles.stats} aria-live="polite">
        <div>
          <span>Despachos identificados</span>
          <strong>{number(summary.dispatched)}</strong>
          <small>
            {number(rows.filter((d) => d.status === "completed").length)}{" "}
            concluídos
          </small>
        </div>
        <div>
          <span>Aproveitados / avaliáveis</span>
          <strong>
            {summary.accepted} / {summary.evaluated}
          </strong>
          <small>
            {percent(summary.rate)} · {number(summary.uninspected)} sem análise ·{" "}
            {number(summary.unresolved)} com uso inconclusivo
          </small>
        </div>
        <div>
          <span>Aplicação comprovada</span>
          <strong>
            {summary.applied} / {summary.applicationEvaluated}
          </strong>
          <small>
            {summary.applicationUnknown} sem comprovação ·{" "}
            {summary.notApplicable} não aplicáveis
          </small>
        </div>
        <div>
          <span>Exclusivos nas três famílias</span>
          <strong>
            {allExclusive} / {panels.length}
          </strong>
          <small>Somente painéis completos no recorte</small>
        </div>
      </div>
      {!rows.length && (
        <p className={styles.empty}>
          Nenhum despacho no período e filtros selecionados.
        </p>
      )}
      <section aria-label="Família de origem e delegado">
        <div className={styles.sectionHeading}>
          <h2>Quem despacha para quem</h2>
          <div
            className={styles.toggle}
            role="group"
            aria-label="Origem do despacho"
          >
            <button
              type="button"
              aria-pressed={basis === "root"}
              onClick={() => setBasis("root")}
            >
              Agente raiz
            </button>
            <button
              type="button"
              aria-pressed={basis === "dispatcher"}
              onClick={() => setBasis("dispatcher")}
            >
              Despachante imediato
            </button>
          </div>
        </div>
        <p className={styles.note}>
          Cada célula mostra identificados, avaliáveis e aproveitados. A taxa
          usa apenas os que têm aproveitamento avaliável.
        </p>
        <div className={styles.tableScroll}>
          <table className={styles.matrix}>
            <caption>
              {basis === "root"
                ? "Família do agente raiz"
                : "Família do despachante"}{" "}
              × família do delegado
            </caption>
            <thead>
              <tr>
                <th scope="col">Origem ↓ / delegado →</th>
                {cells.children.map((child) => (
                  <th scope="col" key={child}>
                    {familyName(child)}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {cells.parents.map((parent, i) => (
                <tr key={parent}>
                  <th scope="row">{familyName(parent)}</th>
                  {cells.cells[i].map((cell) => (
                    <td key={cell.child} data-known={cell.evaluated > 0}>
                      <strong>
                        {number(cell.dispatched)} <small>enviados</small>
                      </strong>
                      <span>
                        {cell.evaluated} avaliáveis · {cell.accepted}{" "}
                        aproveitados
                      </span>
                      <span>
                        {cell.evaluated ? percent(cell.rate) : "Sem avaliação"}
                      </span>
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
      <section aria-label="Mesma família e famílias diferentes">
        <h2>Aproveitamento por cadeira</h2>
        <p className={styles.note}>
          Aproveitados / avaliáveis. Origem ou delegado desconhecidos ficam fora
          desta comparação.
        </p>
        <div className={styles.roleCharts}>
          {groups.map((group) => (
            <div key={group.role} className={styles.roleChart}>
              <h3>{roleName(group.role)}</h3>
              <RateBar label="Mesma família" metric={group.same} />
              <RateBar label="Famílias diferentes" metric={group.cross} />
            </div>
          ))}
        </div>
        <p className={styles.note}>
          Diferenças de tarefa, modelo, esforço e seleção afetam os resultados.
          Esta observação não estabelece superioridade de uma família.
        </p>
      </section>
      <section aria-label="Contribuições por família">
        <h2>Contribuições por família</h2>
        {!panels.length ? (
          <p>Sem painéis completos analisados neste recorte.</p>
        ) : (
          <>
            <p className={styles.note}>
              {number(distinctContributions)} contribuições distintas aceitas ou
              aplicadas em {number(panels.length)} painéis completos analisados.
              Exclusivas foram identificadas somente por uma família.
              Compartilhadas foram identificadas por mais de uma.
            </p>
            <div className={styles.roleCharts}>
              {families.map((family) => (
                <div key={family.family} className={styles.roleChart}>
                  <h3>{familyName(family.family)}</h3>
                  <div className={styles.barRow}>
                    <div>
                      <span>Exclusivas</span>
                      <span>{number(family.exclusive)}</span>
                    </div>
                    <progress
                      max={contributionScale}
                      value={family.exclusive}
                      aria-label={`${familyName(family.family)}: ${family.exclusive} contribuições exclusivas`}
                    />
                  </div>
                  <div className={styles.barRow}>
                    <div>
                      <span>Compartilhadas</span>
                      <span>{number(family.shared)}</span>
                    </div>
                    <progress
                      max={contributionScale}
                      value={family.shared}
                      aria-label={`${familyName(family.family)}: ${family.shared} contribuições compartilhadas`}
                    />
                  </div>
                  <p className={styles.note}>
                    {number(family.panels)} painéis analisados
                  </p>
                </div>
              ))}
            </div>
            <p className={styles.note}>
              Uma contribuição compartilhada aparece em cada família que a
              identificou. A soma entre famílias pode superar o total de
              contribuições distintas. Entram somente painéis completos cujos
              participantes estão no filtro.
            </p>
          </>
        )}
      </section>
      <footer className={styles.note}>
        Histórico local persistido no T3; tarefas nativas não registradas e
        máquinas remotas não estão cobertas. Classificação das cadeiras é
        preliminar fora da amostra inspecionada. Custos não estimados. A
        auditoria fica fora dos despachos de trabalho.
        <br />
        Dados observados em {dateTime(report.source_as_of)} · Julgamentos até{" "}
        {dateTime(report.disposition_as_of)} · São Paulo
      </footer>
    </main>
  );
}

function RateBar({
  label,
  metric,
}: {
  label: string;
  metric: ReturnType<typeof totals>;
}) {
  return (
    <div className={styles.barRow}>
      <div>
        <span>{label}</span>
        <span>
          {metric.evaluated
            ? `${metric.accepted}/${metric.evaluated} · ${percent(metric.rate)}`
            : "Sem avaliação"}
        </span>
      </div>
      <progress
        value={metric.rate ?? 0}
        max={1}
        aria-label={`${label}: ${metric.accepted} aproveitados de ${metric.evaluated} avaliáveis`}
      />
      <small>
        {number(metric.dispatched)} despachos · {number(metric.uninspected)} sem
        análise · {number(metric.unresolved)} com uso inconclusivo
      </small>
    </div>
  );
}
