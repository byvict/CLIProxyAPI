import { useEffect, useState, type FormEvent } from 'react';
import { Link } from 'react-router-dom';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { IconGithub } from '@/components/ui/icons';
import { ProviderHeaderCard } from '@/features/providers/components/ProviderHeaderCard';
import { apiClient } from '@/services/api/client';
import { useAuthStore } from '@/stores';
import { invalidateActivity, parseGitHub, useActivity } from '@/features/performance/api';
import pageStyles from '@/features/providers/ProvidersWorkbenchPage.module.scss';
import categoryStyles from '@/features/providers/components/ProviderCategoryList.module.scss';
import panelStyles from '@/features/providers/components/ProviderResourcePanel.module.scss';
import styles from './Infra.module.scss';

export function InfraPage() {
  const { state, refresh, refreshing } = useActivity('/infra/github', parseGitHub);
  const apiBase = useAuthStore((store) => store.apiBase);
  const managementKey = useAuthStore((store) => store.managementKey);
  const [token, setToken] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const connected =
    state.status === 'ready' && state.data.status === 'connected' ? state.data : null;
  useEffect(() => {
    setToken('');
    setError('');
    setSaving(false);
  }, [apiBase, managementKey]);
  async function connect(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const revision = apiClient.getConnectionRevision();
    const client = apiClient.captureConnection();
    setSaving(true);
    setError('');
    try {
      await client.post('/infra/github', { token: token.trim() }, { timeout: 0 });
      if (revision === apiClient.getConnectionRevision()) {
        setToken('');
        invalidateActivity('/infra/github');
        refresh();
      }
    } catch (cause) {
      if (revision === apiClient.getConnectionRevision())
        setError(cause instanceof Error ? cause.message : 'Não foi possível conectar ao GitHub.');
    } finally {
      if (revision === apiClient.getConnectionRevision()) setSaving(false);
    }
  }
  return (
    <div className={pageStyles.page}>
      <ProviderHeaderCard
        title="Infra"
        totalActive={connected ? 1 : 0}
        totalResources={1}
        providerFamilies={1}
        updatedAtLabel=""
        showSummary={false}
        showNewAction={false}
        isFetching={state.status === 'loading' || refreshing || saving}
        onRefresh={refresh}
        onNew={() => {}}
      />
      <div className={pageStyles.layout}>
        <aside className={categoryStyles.aside} aria-label="Infraestruturas">
          <p className={categoryStyles.eyebrow}>Infraestruturas</p>
          <div className={`${categoryStyles.item} ${categoryStyles.active}`} aria-current="true">
            <span className={categoryStyles.itemLeft}>
              <IconGithub size={26} />
              <span>GitHub</span>
            </span>
            <span>1</span>
          </div>
        </aside>
        <section className={panelStyles.panel} aria-label="Conexão GitHub">
          <div className={styles.titleRow}>
            <div className={styles.identity}>
              <IconGithub size={28} />
              <div>
                <h2>GitHub</h2>
                <p>Código, colaboração e contribuições.</p>
              </div>
            </div>
            <span className={connected ? styles.connected : styles.status}>
              {state.status === 'loading'
                ? 'Verificando…'
                : connected
                  ? 'Conectado'
                  : state.status === 'error'
                    ? 'Conexão indisponível'
                    : 'Não conectado'}
            </span>
          </div>
          {state.status === 'ready' && state.refreshError && <div className="error-box" role="alert">{state.refreshError}</div>}
          {state.status === 'error' && (
            <div className="error-box" role="alert">
              {state.message}
            </div>
          )}
          {connected && (
            <div className={styles.account}>
              <div>
                <strong>@{connected.activity.login}</strong>
                <p>
                  {connected.source === 'environment'
                    ? 'Token do ambiente do servidor'
                    : 'Token salvo no servidor'}
                </p>
              </div>
              <Link to="/performance">Ver Performance →</Link>
            </div>
          )}
          <form onSubmit={connect} className={styles.form}>
            <h3>{connected ? 'Atualizar conexão' : 'Conectar com token'}</h3>
            <p>
              Use um personal access token da sua conta. O token é validado no GitHub antes de ser
              salvo no servidor e também atende às integrações GitHub já existentes.
            </p>
            <Input
              label="Personal access token"
              type="password"
              name="github-token"
              autoComplete="off"
              spellCheck={false}
              placeholder="Cole seu token GitHub"
              value={token}
              onChange={(event) => setToken(event.target.value)}
              disabled={saving}
              required
              maxLength={4096}
            />
            {error && (
              <div className="error-box" role="alert">
                {error}
              </div>
            )}
            <div className={styles.actions}>
              <Button
                type="submit"
                loading={saving}
                disabled={!token.trim() || state.status === 'loading'}
              >
                {connected ? 'Validar e atualizar' : 'Conectar GitHub'}
              </Button>
              <a
                href="https://github.com/settings/personal-access-tokens/new"
                target="_blank"
                rel="noreferrer"
              >
                Criar token no GitHub ↗
              </a>
            </div>
            <p className={styles.help}>
              Contribuições privadas dependem das permissões do token e das regras da organização.{' '}
              <a
                href="https://docs.github.com/en/graphql/guides/forming-calls-with-graphql#authenticating-with-graphql"
                target="_blank"
                rel="noreferrer"
              >
                Ver permissões
              </a>
            </p>
          </form>
        </section>
      </div>
    </div>
  );
}
