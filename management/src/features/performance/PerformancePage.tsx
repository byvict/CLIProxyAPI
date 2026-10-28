import { useState } from 'react';
import { Link } from 'react-router-dom';
import { Button } from '@/components/ui/Button';
import { IconRefreshCw } from '@/components/ui/icons';
import { ActivityCalendar, ActivityChart } from './ActivityCharts';
import {
  parseGitHub,
  parsePerformance,
  providerNames,
  providers,
  useActivity,
  type Activity,
  type Provider,
} from './api';
import { calendarDays, compact, duration, number, selectActivity, shortDate } from './metrics';
import styles from './Performance.module.scss';

export function PerformancePage() {
  const [provider, setProvider] = useState<Provider>('all');
  const [source, setSource] = useState<'t3' | 'github'>('t3');
  const performance = useActivity('/performance', parsePerformance);
  const github = useActivity('/infra/github', parseGitHub, source === 'github');
  const activity =
    performance.state.status === 'ready' && performance.state.data.status === 'ready'
      ? performance.state.data.activity
      : null;
  const selected = activity ? selectActivity(activity, provider) : null;
  const githubActivity =
    github.state.status === 'ready' && github.state.data.status === 'connected'
      ? github.state.data.activity
      : null;
  const selectedSource = source === 'github' ? github : performance;
  const busy = selectedSource.state.status === 'loading' || selectedSource.refreshing;
  const refresh = selectedSource.refresh;
  return (
    <div className={styles.page}>
      <header className={styles.heading}>
        <div>
          <h1>Performance</h1>
          <p>Sua atividade, dia após dia.</p>
        </div>
        <Button variant="secondary" size="sm" onClick={refresh} disabled={busy}>
          <IconRefreshCw size={15} /> Atualizar
        </Button>
      </header>
      {selectedSource.refreshing && <p role="status">Atualizando…</p>}
      {selectedSource.state.status === 'ready' && selectedSource.state.refreshError && (
        <div className="error-box" role="alert">{selectedSource.state.refreshError}</div>
      )}
      <nav className={styles.providerTabs} aria-label="Provedores">
        {providers.map((id) => (
          <button
            key={id}
            type="button"
            aria-pressed={provider === id}
            className={provider === id ? styles.activeProvider : ''}
            onClick={() => setProvider(id)}
          >
            {providerNames[id]}
          </button>
        ))}
      </nav>
      <main className={styles.content} aria-label={`Performance de ${providerNames[provider]}`}>
        <div className={styles.stats}>
          <div title="Maior duração entre início e conclusão de uma execução registrada.">
            <span>Maior execução</span>
            <strong>{selected ? duration(selected.total.longestDurationMs) : '—'}</strong>
          </div>
          <div title="Cada turno observado conta como uma execução. Chamadas sem início de turno não geram agentes.">
            <span>Agentes</span>
            <strong>{selected ? compact(selected.total.agents) : '—'}</strong>
          </div>
          <div title="Maior sequência de dias com execuções neste ano.">
            <span>Maior sequência</span>
            <strong>{selected ? `${selected.longestStreak}d` : '—'}</strong>
          </div>
          <div title="Dias consecutivos de atividade, incluindo hoje ou a sequência que terminou ontem.">
            <span>Sequência atual</span>
            <strong>{selected ? `${selected.currentStreak}d` : '—'}</strong>
          </div>
        </div>
        <section aria-label="Calendário de atividade">
          <div className={styles.sourceRow}>
            <div className={styles.sourceTabs} role="group" aria-label="Fonte da atividade">
              <button type="button" aria-pressed={source === 't3'} onClick={() => setSource('t3')}>
                {activity?.historyImported ? 'T3code + CLIs' : 'T3code'}
              </button>
              <button
                type="button"
                aria-pressed={source === 'github'}
                onClick={() => setSource('github')}
              >
                GitHub
              </button>
            </div>
            <span>
              {source === 't3' && activity && selected
                ? `${selected.total.reportedAgents || selected.total.tokenRecords ? compact(selected.total.tokens) : '—'} tokens em ${activity.year}`
                : source === 'github' && githubActivity
                  ? `${number(githubActivity.contributions)} contribuições em ${githubActivity.year}`
                  : ''}
            </span>
          </div>
          {source === 't3' ? (
            <>
              {performance.state.status === 'loading' && (
                <div className={styles.placeholder} role="status">
                  Carregando atividade do T3code…
                </div>
              )}
              {performance.state.status === 'error' && (
                <div className="error-box" role="alert">
                  {performance.state.message}{' '}
                  <button type="button" onClick={performance.refresh}>
                    Tentar novamente
                  </button>
                </div>
              )}
              {performance.state.status === 'ready' &&
                performance.state.data.status === 'unavailable' && (
                  <div className={styles.placeholder}>
                    Nenhum histórico T3code disponível neste servidor.
                    <small>
                      Execute o proxy na mesma máquina e no mesmo usuário do T3code para ler sua
                      atividade local.
                    </small>
                  </div>
                )}
              {activity && selected && (
                <ActivityCalendar
                  year={activity.year}
                  today={activity.today}
                  unit="tokens"
                  days={calendarDays(activity.year).flatMap((date) => {
                    if (!date) return [];
                    const day = selected.days.find((entry) => entry.date === date);
                    return [
                      {
                        date,
                        value:
                          !activity.availableFrom ||
                          date < activity.availableFrom ||
                          (day && !day.reportedAgents && !day.tokenRecords)
                            ? null
                            : (day?.tokens ?? 0),
                      },
                    ];
                  })}
                />
              )}
            </>
          ) : (
            <>
              {github.state.status === 'loading' && (
                <div className={styles.placeholder} role="status">
                  Carregando contribuições do GitHub…
                </div>
              )}
              {github.state.status === 'error' && (
                <div className="error-box" role="alert">
                  {github.state.message} <Link to="/infra">Ver conexão</Link>
                </div>
              )}
              {github.state.status === 'ready' && github.state.data.status === 'disconnected' && (
                <div className={styles.placeholder}>
                  Conecte sua conta para acompanhar as contribuições.
                  <Link to="/infra" className={styles.connectLink}>
                    Conectar GitHub →
                  </Link>
                </div>
              )}
              {githubActivity && (
                <>
                  <ActivityCalendar
                    year={githubActivity.year}
                    today={
                      activity?.today ??
                      new Intl.DateTimeFormat('en-CA', { timeZone: 'America/Sao_Paulo' }).format(
                        new Date()
                      )
                    }
                    unit="contribuições"
                    days={githubActivity.days.map((day) => ({ date: day.date, value: day.count }))}
                  />
                  <div className={styles.githubSummary}>
                    {number(githubActivity.pullRequests)} pull requests ·{' '}
                    {number(githubActivity.reviews)} reviews · {number(githubActivity.commits)}{' '}
                    commits
                  </div>
                  <p className={styles.note}>
                    @{githubActivity.login} · Atividade da conta inteira, sem atribuição por
                    provedor.
                  </p>
                </>
              )}
            </>
          )}
        </section>
        {activity && selected && <TokenCharts activity={activity} provider={provider} />}
      </main>
    </div>
  );
}

function TokenCharts({ activity, provider }: { activity: Activity; provider: Provider }) {
  const selected = selectActivity(activity, provider);
  const recentTokens = selected.recent.reduce((sum, day) => sum + day.tokens, 0);
  const recentAgents = selected.recent.reduce((sum, day) => sum + day.agents, 0);
  const hasTokens = selected.recent.some((day) => day.reportedAgents > 0 || (day.tokenRecords ?? 0) > 0);
  return (
    <>
      <section className={styles.chartSection} aria-label="Tokens">
        <h2>Tokens</h2>
        <p className={styles.chartTotal}>
          {hasTokens ? `${compact(recentTokens)} tokens` : 'Sem dados de tokens'}
        </p>
        {hasTokens ? (
          <ActivityChart
            unit="tokens"
            kind="area"
            points={selected.recent.map((day) => ({
              date: day.date,
              value:
                day.date < activity.availableFrom || (day.agents > 0 && !day.reportedAgents && !day.tokenRecords)
                  ? null
                  : day.tokens,
            }))}
          />
        ) : (
          <div className={styles.placeholder}>
            O T3code ainda não registrou tokens para este provedor nos últimos 30 dias.
          </div>
        )}
      </section>
      <section className={styles.chartSection} aria-label="Agentes">
        <h2>Agentes</h2>
        <p className={styles.chartTotal}>{compact(recentAgents)} agentes</p>
        <div className={styles.legend}>
          <i /> {activity.historyImported ? 'T3code + CLIs' : 'T3code'} ({compact(recentAgents)})
        </div>
        <ActivityChart
          unit="agentes"
          kind="bars"
          points={selected.recent.map((day) => ({
            date: day.date,
            value: !activity.availableFrom || day.date < activity.availableFrom ? null : day.agents,
          }))}
        />
      </section>
      <footer className={styles.note}>
        {activity.availableFrom
          ? `Histórico disponível desde ${shortDate(activity.availableFrom)}. `
          : 'Nenhuma execução registrada neste ano. '}
        Cada agente corresponde a um turno. Gráficos: últimos 30 dias, horário de São Paulo.
        {activity.historyImported && (
          <span className={styles.coverage}>
            Histórico recuperado do T3code e dos CLIs. Chamadas com tokens sem início de turno
            preservado entram no consumo, sem criar agentes. Leituras parciais continuam sinalizadas.
          </span>
        )}
        {selected.total.reportedAgents < selected.total.agents ||
        selected.total.partialAgents > 0 ? (
          <span className={styles.coverage}>
            Tokens informados em {number(selected.total.reportedAgents)} de{' '}
            {number(selected.total.agents)} execuções; {number(selected.total.partialAgents)} com
            leitura parcial. Totais podem estar incompletos.
          </span>
        ) : null}
      </footer>
    </>
  );
}
