import { useMemo, useState } from "react";
import { Button } from "@/components/ui/Button";
import { IconRefreshCw } from "@/components/ui/icons";
import { useActivity } from "@/features/performance/api";
import { parseDelegation, type Contribution, type Report } from "./data";
import {
  dateTime,
  day,
  familyName,
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
  const { state, refresh } = useActivity("/delegation", parseDelegation);
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
          disabled={state.status === "loading"}
        >
          <IconRefreshCw size={15} /> Atualizar
        </Button>
      </header>
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
  const selected = new Set(rows.map((d) => d.dispatch_id));
  const contributions = report.contributions.filter((c) =>
    c.discoverers.some((d) => selected.has(d.dispatch_id)),
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
          {number(global.evaluated)} de {number(global.dispatched)} despachos
          tiveram o aproveitamento avaliado.
        </strong>
        <p>
          {number(global.unknown)} ainda não avaliados. Não são classificados
          como inúteis. A amostra inspecionada não representa necessariamente
          todo o histórico.
        </p>
        <progress
          max={Math.max(1, global.dispatched)}
          value={global.evaluated}
          aria-label="Cobertura da avaliação"
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
          <span>Despachos no recorte</span>
          <strong>{number(summary.dispatched)}</strong>
          <small>
            {number(rows.filter((d) => d.status === "completed").length)}{" "}
            concluídos
          </small>
        </div>
        <div>
          <span>Aproveitados / avaliados</span>
          <strong>
            {summary.accepted} / {summary.evaluated}
          </strong>
          <small>
            {percent(summary.rate)} · {number(summary.unknown)} não avaliados
          </small>
        </div>
        <div>
          <span>Aplicação comprovada</span>
          <strong>
            {summary.applied} / {summary.applicationEvaluated}
          </strong>
          <small>
            {summary.applicationUnknown} ainda não conferidos ·{" "}
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
          Cada célula mostra enviados, avaliados e aproveitados. A taxa usa
          apenas os avaliados.
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
                        {cell.evaluated} avaliados · {cell.accepted}{" "}
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
          Aproveitados / avaliados. Origem ou delegado desconhecidos ficam fora
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
      <section aria-label="Contribuições exclusivas por painel">
        <h2>O que cada família acrescentou</h2>
        <p className={styles.note}>
          Exclusivo: contribuição aceita ou aplicada que nenhuma outra família
          do painel identificou. Painéis com membros fora do filtro não entram
          na contagem.
        </p>
        {!panels.length ? (
          <p>Sem painéis completos neste recorte.</p>
        ) : (
          <div className={styles.tableScroll}>
            <table>
              <thead>
                <tr>
                  <th>Painel</th>
                  <th>Contribuições distintas</th>
                  <th>Exclusivas por família</th>
                  <th>As três acrescentaram?</th>
                </tr>
              </thead>
              <tbody>
                {panels.map((p) => (
                  <tr key={p.panel_id}>
                    <td>
                      {p.panel_id}
                      <small>
                        {p.scope.includes("fix")
                          ? "Revisão da correção"
                          : "Revisão inicial"}
                      </small>
                    </td>
                    <td>{p.union}</td>
                    <td>
                      <div className={styles.exclusives}>
                        {p.families.map((f) => (
                          <span key={f.family}>
                            {familyName(f.family)} <b>{f.exclusive}</b>
                            <small>{f.shared} compartilhadas</small>
                          </span>
                        ))}
                      </div>
                    </td>
                    <td>{p.allExclusive ? "Sim" : "Não"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
      <section aria-label="Achados e evidências">
        <h2>Propostas e achados inspecionados</h2>
        <p className={styles.note}>
          Aceito significa que o responsável decidiu aproveitar. Aplicação
          comprovada exige inspeção do artefato; não equivale a prova de
          execução ou implantação.
        </p>
        {!contributions.length && (
          <p>
            Nenhuma contribuição inspecionada neste recorte. Isso não demonstra
            ausência de contribuições.
          </p>
        )}
        {contributions.map((c) => (
          <ContributionDetails
            key={c.contribution_id}
            contribution={c}
            report={report}
          />
        ))}
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
        aria-label={`${label}: ${metric.accepted} aproveitados de ${metric.evaluated} avaliados`}
      />
      <small>
        {number(metric.dispatched)} despachos · {number(metric.unknown)} não
        avaliados
      </small>
    </div>
  );
}

function ContributionDetails({
  contribution: c,
  report,
}: {
  contribution: Contribution;
  report: Report;
}) {
  const evidenceIDs = new Set([
    ...(c.decision_evidence_ids ?? []),
    ...(c.artifact_evidence_ids ?? []),
  ]);
  const evidence = report.evidence.filter((e) =>
    evidenceIDs.has(e.evidence_id),
  );
  const families = [...new Set(c.discoverers.map((d) => d.child_family))];
  const disposition =
    c.accepted === true || c.applied === true
      ? "Aceita"
      : c.disposition === "rejected"
        ? "Rejeitada"
        : c.disposition === "noted"
          ? "Não selecionada"
          : "Sem decisão confirmada";
  const application = c.kind.endsWith("_artifact")
    ? "Artefato de verificação; aplicação não se aplica"
    : c.applied === true
      ? "Aplicação comprovada"
      : c.accepted === true && c.applied === null
        ? "Aplicação ainda não conferida"
        : "Sem aplicação comprovada";
  return (
    <details className={styles.finding}>
      <summary>
        <span>{c.label}</span>
        <small>
          {disposition} · {families.map(familyName).join(" + ")}
        </small>
      </summary>
      <p>
        {application} · {c.kind} · {c.severity}
      </p>
      <p>
        {families.length === 1
          ? "Exclusiva nesta avaliação"
          : "Descoberta compartilhada"}{" "}
        · {c.panel_id ?? "Contribuição fora de painel"}
      </p>
      <h3>Referências da decisão e do artefato</h3>
      {evidence.length ? (
        evidence.map((e) => (
          <div key={e.evidence_id} className={styles.evidence}>
            <span>{e.kind}</span>
            <code>{e.source_item_id || e.source_id || e.evidence_id}</code>
            {e.thread_id && <code>Thread: {e.thread_id}</code>}
            {e.base && <code>Base: {e.base}</code>}
            {e.head && <code>Head: {e.head}</code>}
            {e.sha256 && <code>SHA-256: {e.sha256}</code>}
          </div>
        ))
      ) : (
        <p>Referências detalhadas indisponíveis nesta publicação.</p>
      )}
    </details>
  );
}
