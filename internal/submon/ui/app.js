(() => {
  'use strict';
  const root = document.getElementById('cp-monitor');
  const screen = document.getElementById('screen');
  const tip = document.getElementById('chart-tooltip');
  const state = {screen: 'overview', provider: 'all', account: '', window: 'weekly', period: '24h', lookback: '1h', horizon: '7d', recurrence: true, chart: 'remaining', collapsed: false, search: ''};
  const providerNames = {claude: 'Claude', codex: 'Codex', grok: 'Grok / xAI', muse: 'Muse / Meta'};
  const emails = new Map();
  let snapshot = null;
  let connectionError = "";
  let view = null;
  let committedSelection = null;
  let authenticated = null;
  let requestVersion = 0;
  let lastFocus;
  let toastTimer;
  const esc = value => String(value ?? '').replace(/[&<>"']/g, c => ({'&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'}[c]));
  const fmt = (n, digits = 1) => n == null ? '—' : Number(n).toLocaleString('pt-BR', {maximumFractionDigits: digits});
  const date = value => value && !String(value).startsWith("0001-") ? new Date(value).toLocaleString('pt-BR', {timeZone: 'America/Sao_Paulo', day: '2-digit', month: 'short', hour: '2-digit', minute: '2-digit'}) : 'Não informado';
  const scopeLabel = value => value === 'all' ? 'Todos os modelos' : value.replace(/^provider-label:/, '');
  const basisLabel = value => value.startsWith('account-percent:') ? 'Percentual desta conta; plano não informado' : ({'native-subscription':'Assinatura nativa','native-credits':'Créditos da assinatura','unknown-plan':'Plano não informado'}[value] || value);
  const percent = n => n == null ? 'Sem leitura' : fmt(n) + '%';
  const name = p => providerNames[p] || p;
  const age = seconds => seconds == null ? 'Sem observação' : seconds < 60 ? 'há menos de 1 min' : seconds < 3600 ? 'há ' + Math.floor(seconds / 60) + ' min' : 'há ' + fmt(seconds / 3600) + ' h';
  const freshness = value => ({fresh: 'Atual', stale: 'Desatualizada', missing: 'Sem leitura'}[value] || value);
  const statusLabel = a => !a.active ? 'Fora de sessão' : a.freshness !== 'fresh' ? freshness(a.freshness) : ({unavailable: 'Sem cota', limited: 'Limite por modelo', unknown: 'Indisponível', available: 'Disponível'}[a.availability.state]);
  const reset = value => {
    if (!value) return 'Nenhum reset informado';
    const hours = (new Date(value) - new Date(snapshot.generatedAt)) / 3600000;
    return (hours > 0 ? 'em ' + (hours >= 24 ? Math.floor(hours / 24) + 'd ' : '') + Math.floor(hours % 24) + 'h' : 'data vencida; aguardando leitura') + ' · ' + date(value);
  };
  async function api(path, options = {}) {
    const response = await fetch(path, {credentials: 'same-origin', cache: 'no-store', ...options, headers: {'Content-Type': 'application/json', ...options.headers}});
    const data = await response.json();
    if (response.status === 401) { authenticated = false; snapshot = null; view = null; committedSelection = null; emails.clear(); login(); throw new Error('Autenticação necessária.'); }
    if (!response.ok) throw new Error(data.message || 'Não foi possível consultar o monitor.');
    return data;
  }
  const post = (path, body) => api(path, {method: 'POST', body: JSON.stringify(body)});
  function login() {
    screen.innerHTML = `<section class="panel login"><h1>Monitor de assinaturas</h1><p class="subtitle">Use a chave privada do arquivo access.key no diretório de dados do monitor.</p><form id="login-form"><label for="monitor-key">Chave do monitor</label><input id="monitor-key" type="password" autocomplete="off" required><button class="btn btn-primary" type="submit">Entrar</button><p id="login-error" role="alert" class="warning"></p></form><p class="aside-caption">A sessão dura até 12 horas. Horários de São Paulo.</p></section>`;
  }
  screen.addEventListener('submit', async event => {
    if (event.target.id !== 'login-form') return;
    event.preventDefault();
    const input = document.getElementById('monitor-key');
    const key = input.value;
    input.value = '';
    try {
      const response = await fetch('/session', {method: 'POST', credentials: 'same-origin', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({key})});
      if (!response.ok) throw new Error('Chave inválida ou sessão indisponível.');
      authenticated = true;
      await load();
    } catch (error) { const message = document.getElementById('login-error'); if (message) message.textContent = error.message; }
  });
  function selectionKeys(data = snapshot, selection = state) {
    const byId = new Map();
    for (const a of data.accounts) {
      if (a.provider !== selection.provider || (selection.account && a.id !== selection.account) || (!selection.account && !a.active)) continue;
      for (const w of (selection.account ? a.historyWindows : a.windows)) byId.set(w.id, w);
    }
    return [...byId.values()];
  }
  function selected() {
    return Object.fromEntries(['provider', 'account', 'window', 'period', 'lookback', 'horizon', 'recurrence'].map(key => [key, state[key]]));
  }
  async function load() {
    const version = ++requestVersion;
    const requested = selected();
    root.setAttribute('aria-busy', 'true');
    if (!snapshot || !view || JSON.stringify(requested) !== JSON.stringify(committedSelection)) render();
    try {
      const nextSnapshot = await api('/api/v1/snapshot');
      if (version !== requestVersion) return;
      const keys = selectionKeys(nextSnapshot, requested);
      if (requested.provider !== 'all' && !keys.some(w => w.id === requested.window)) {
        requested.window = (keys.find(w => w.key.kind === 'weekly' && w.key.scope === 'all') || keys[0])?.id || 'weekly';
      }
      const params = new URLSearchParams({...requested, recurrence: String(requested.recurrence)});
      const nextView = await api('/api/v1/view?' + params);
      if (version !== requestVersion) return;
      snapshot = nextSnapshot;
      view = nextView;
      Object.assign(state, requested);
      committedSelection = requested;
      authenticated = true;
      connectionError = "";
      render();
    } catch (error) {
      if (version !== requestVersion || authenticated === false) return;
      if (committedSelection) Object.assign(state, committedSelection);
      connectionError = error.message;
      render();
      toast(error.message);
    } finally { if (version === requestVersion) root.setAttribute('aria-busy', 'false'); }
  }
  function header() {
    const active = snapshot.accounts.filter(a => a.active);
    const loaded = active.filter(a => a.observedAt);
    const titles = {overview: ['Monitor de assinaturas', 'Saldos nativos, consumo observado e próximas renovações.'], accounts: ['Cotas por conta', 'Janelas independentes e histórico de cada login.'], history: ['Histórico de consumo', 'Deltas observados por hora e dia, com cobertura explícita.'], forecast: ['Ritmo e projeções', 'Saldo e ritmo sustentável da conta e janela selecionadas.']};
    const [title, subtitle] = titles[state.screen];
    return `<div class="topline"><span class="eyebrow">CLI Proxy · Assinaturas</span><span class="demo">Fontes nativas · Histórico local</span></div><div class="heading"><div><h1>${title}</h1><p class="subtitle">${subtitle}</p></div><div class="actions"><button class="btn btn-primary" data-action="refresh">Atualizar fontes</button></div></div><nav class="provider-tabs" aria-label="Provedores">${['all', ...snapshot.providers.map(p => p.provider)].map(p => `<button class="${state.provider === p ? 'active' : ''}" data-filter="${p}" aria-pressed="${state.provider === p}">${p === 'all' ? 'Todos' : name(p)} <span class="count">${p === 'all' ? active.length : snapshot.providers.find(row => row.provider === p).accountCount}</span></button>`).join('')}</nav><div class="summary-line"><span><strong>${active.length}</strong> contas atuais</span><span><strong>${loaded.length}</strong> com leitura</span><span>${snapshot.accounts.length - active.length} fora de sessão</span><span class="summary-end">${date(snapshot.generatedAt)} · São Paulo</span></div>`;
  }
  function sourceStatuses() {
    return `<div class="source-status">${snapshot.sources.filter(s => state.provider === 'all' || state.provider === s.provider).filter(s => s.status !== 'ok' || s.inFlightSince || s.queued).map(s => `<p><strong>${esc(name(s.provider))}</strong> · ${esc(s.inFlightSince ? 'Consulta pendente desde ' + date(s.inFlightSince) : s.queued ? 'Consulta na fila' : s.message)}<br><span class="muted">Coleta programada ${date(s.nextAt)}.${s.notBefore && !s.notBefore.startsWith('0001-') ? ' Bloqueio mínimo até ' + date(s.notBefore) + '.' : ''} ${s.provider === 'muse' ? 'O mint pode emitir uma chave, que é descartada. Intervalo mínimo de 30 min.' : 'A CLI nativa cuida da renovação do login.'}</span></p>`).join('')}</div>`;
  }
  function providers() {
    return `<section class="providers" aria-label="Resumo por provedor">${snapshot.providers.filter(p => state.provider === 'all' || state.provider === p.provider).map(p => `<article class="provider-card"><button class="card-name" data-filter="${p.provider}" aria-label="Ver ${esc(name(p.provider))}"><span><i class="provider-mark">${esc(name(p.provider)[0])}</i><strong>${esc(name(p.provider))}</strong></span><span class="muted">${p.accountCount} contas · ${p.loadedCount} leituras</span></button>${p.windows.length ? p.windows.map(w => {
      const accounts = w.total.contributors.map(id => snapshot.accounts.find(a => a.id === id)).filter(Boolean);
      return `<div class="window-summary"><div class="small muted">${esc(w.label)} · ${esc(scopeLabel(w.key.scope))} · ${esc(basisLabel(w.key.capacityBasis))}</div><div class="provider-value num">${w.total.remaining == null ? 'Sem leitura' : fmt(w.total.remaining) + '%'} <small>${w.total.remaining == null ? 'Nenhuma conta contribui com saldo conhecido.' : 'de ' + fmt(w.total.capacity) + '% normalizados'}</small></div><div class="segments">${accounts.map(a => { const reading = a.windows.find(row => row.id === keyID(w.key)); return `<span class="track" tabindex="0" title="${esc(a.identity.label)} · ${percent(reading?.remaining)} · ${freshness(reading?.freshness)}" aria-label="${esc(a.identity.label)} ${percent(reading?.remaining)} ${freshness(reading?.freshness)}"><span style="width:${reading?.remaining || 0}%;background:${reading?.remaining === 0 ? 'var(--cp-bad)' : 'var(--cp-accent)'}"></span></span>`; }).join('')}</div><small>${w.total.missing.length ? w.total.missing.length + ' contas sem esta leitura · ' : ''}${accounts.some(a => a.freshness !== 'fresh') ? 'Inclui leitura desatualizada · ' : ''}Reset ${reset(w.nextReset)}</small></div>`;
    }).join('') : '<div class="provider-value">—<small>Sem leitura válida. Não é saldo zero.</small></div>'}<p class="aside-caption">Percentuais de janelas e planos distintos permanecem separados.</p></article>`).join('')}</section>`;
  }
  function keyID(key) {
    for (const a of snapshot.accounts) for (const w of a.windows) if (JSON.stringify(w.key) === JSON.stringify(key)) return w.id;
    return '';
  }
  function selectors() {
    if (state.provider === 'all') return '';
    const accounts = snapshot.accounts.filter(a => a.provider === state.provider);
    const select = (key, label, options) => `<label>${label}<select data-select="${key}">${options.map(([value, text]) => `<option value="${esc(value)}" ${state[key] === value ? 'selected' : ''}>${esc(text)}</option>`).join('')}</select></label>`;
    return `<div class="selection">${select('account', 'Conta', [['', 'Contas atuais compatíveis'], ...accounts.map(a => [a.id, a.identity.label + (a.active ? '' : ' · fora de sessão')])])}${select('window', 'Janela', selectionKeys().map(w => [w.id, w.label + ' · ' + scopeLabel(w.key.scope) + ' · ' + basisLabel(w.key.capacityBasis) + (w.current === false ? ' · histórico' : '')]))}${select('period', 'Histórico', [['24h', 'Últimas 24 horas'], ['7d', 'Últimos 7 dias'], ['30d', 'Últimos 30 dias']])}</div>`;
  }
  function alerts() {
    const accounts = snapshot.accounts.filter(a => state.provider === 'all' || a.provider === state.provider).filter(a => !state.account || a.id === state.account);
    const rows = accounts.flatMap(a => {
      const messages = [];
      if (a.freshness !== 'fresh') messages.push(`${freshness(a.freshness)} · última observação ${age(a.ageSeconds)}. ${a.message}`);
      for (const l of a.availability.limiting) messages.push(`Janela limitante ${l.label} · ${scopeLabel(l.scope)}. Retorno informado ${date(l.returnsAt)}. ${a.freshness !== 'fresh' ? 'Saldo desatualizado.' : ''}`);
      const predictions = state.provider === 'all' ? a.windows.map(w => ({label: w.label, exhaustsAt: w.exhaustsAt, freshness: w.freshness, base: '1h'})) : view.groups.flatMap(g => g.accounts.filter(row => row.accountId === a.id).map(row => ({label: g.label, exhaustsAt: row.exhaustsAt, freshness: row.freshness, base: state.lookback})));
      for (const prediction of predictions) if (prediction.exhaustsAt) messages.push(`Estimativa de esgotamento de ${prediction.label} em ${date(prediction.exhaustsAt)}, antes do reset informado. Base ${prediction.base} · leitura ${freshness(prediction.freshness).toLowerCase()}.`);
      return messages.map(message => `<p><strong>${esc(a.identity.label)}</strong><br>${esc(message)}</p>`);
    });
    return rows.length ? `<div class="section-title"><h2>O que pede atenção</h2></div><section class="alert-list">${rows.join('')}</section>` : '';
  }
  function groupSummary(g) {
    const rates = g.accounts.map(a => a.recentPpH).filter(n => n != null);
    return `<div class="chart-summary"><span class="big num">${g.total.remaining == null ? 'Sem leitura atual' : fmt(g.total.remaining) + '%'}</span><small>${g.total.remaining == null ? 'O histórico permanece disponível abaixo.' : 'de ' + fmt(g.total.capacity) + '% normalizados restantes'}</small><span class="pace">${rates.length === 1 ? fmt(rates[0], 2) + ' p.p./h observados' : rates.length ? rates.length + ' ritmos por conta abaixo' : 'Ritmo ainda indisponível'}</span><span class="scope-label">${esc(name(state.provider))} · ${esc(g.label)} · ${esc(scopeLabel(g.key.scope))} · ${g.total.contributors.length} contribuintes · ${g.total.missing.length} sem leitura</span></div><p class="coverage">${esc(g.total.normalization)} ${g.total.contributors.map(id => esc(snapshot.accounts.find(a => a.id === id)?.identity.label || id)).join(', ')}</p>`;
  }
  function insufficient(a) { return a.insufficient ? `<p class="notice">${esc(a.insufficient)}</p>` : ''; }
  function chartSection(a, kind) {
    const notes = `<p class="coverage">Cobertura do histórico ${fmt(a.coverage.fraction * 100)}% · cobertura da base do ritmo ${fmt(a.rateCoverage.fraction * 100)}%. ${a.coverage.complete ? 'Período completo.' : 'Período incompleto.'} ${fmt(a.unattributedPp)} p.p. não atribuídos a horas. Ritmo observado entre amostras.</p>`;
    return `<section class="panel"><div class="panel-top"><div><h2>${esc(a.label)}</h2><p>${freshness(a.freshness)} · ${esc(kind === 'rate' ? 'Média de uma hora' : 'Saldo observado e estimado')}</p></div></div>${insufficient(a)}<div class="chart large" data-account="${a.accountId}" data-window="${a.windowId}" data-chart="${kind}"></div><p class="chart-inspection" aria-live="polite">Use o ponteiro ou as setas para inspecionar cada leitura.</p>${notes}${kind === 'rate' ? `<p class="chart-note">Pico de 1 hora ${a.peak ? fmt(a.peak.ppH, 2) + ' p.p./h · ' + date(a.peak.from) + ' a ' + date(a.peak.to) + ' · cobertura ' + fmt(a.peak.coverage * 100) + '%' : 'indisponível por falta de uma hora contínua'}. Renovações, ajustes e lacunas são excluídos.</p>` : '<p class="chart-note"><span><i class="legend-mark"></i>Observado <i class="legend-mark dashed"></i>Estimativa após Agora</span><span>O primeiro reset é informado. Recorrências são inferidas.</span></p>'}</section>`;
  }
  function overview() {
    return providers() + selectors() + (view.selectionRequired ? `<section class="panel empty">${esc(view.selectionRequired)}</section>` : view.groups.map(g => `<section class="panel summary-total">${groupSummary(g)}<div class="panel-top"><div class="chart-tabs" role="tablist"><button role="tab" aria-selected="${state.chart === 'remaining'}" data-chart-tab="remaining" class="${state.chart === 'remaining' ? 'active' : ''}">Saldo restante</button><button role="tab" aria-selected="${state.chart === 'rate'}" data-chart-tab="rate" class="${state.chart === 'rate' ? 'active' : ''}">Ritmo de consumo</button></div><button class="btn btn-small" data-action="collapse">${state.collapsed ? 'Expandir gráficos' : 'Recolher gráficos'}</button></div>${state.collapsed ? '' : settings()}</section>${state.collapsed ? '' : g.accounts.map(a => chartSection(a, state.chart)).join('')}`).join('')) + alerts() + ledger();
  }
  function settings() {
    return `<details class="forecast-settings"><summary>Ajustar a previsão</summary><label>Base do ritmo observado<select data-select="lookback">${['1h', '6h', '24h'].map(value => `<option ${value === state.lookback ? 'selected' : ''} value="${value}">${value === '1h' ? 'Última hora' : 'Últimas ' + value.replace('h', ' horas')}</option>`).join('')}</select></label><label>Horizonte<select data-select="horizon">${['3d', '7d', '14d'].map(value => `<option ${state.horizon === value ? 'selected' : ''} value="${value}">Próximos ${value.replace('d', ' dias')}</option>`).join('')}</select></label><label class="check-label"><input type="checkbox" data-toggle="recurrence" ${state.recurrence ? 'checked' : ''}>Inferir recorrências após o reset informado</label><p class="setting-caption">Ao renovar, o saldo volta ao teto. A projeção usa apenas leituras contínuas da mesma conta e janela. Lacunas suspendem a estimativa. Uma mudança de plano mantém um histórico separado.</p></details>`;
  }
  function quotaColumn(a, type) {
    const windows = a.windows.filter(w => type === 'session' ? w.key.kind === 'session' : type === 'model' ? w.key.scope !== 'all' : w.key.scope === 'all' && w.key.kind !== 'session');
    if (!windows.length) return `<div><span class="window-title muted">${type === 'session' ? 'Sessão' : type === 'model' ? 'Por modelo' : 'Semanal / período'}</span><p class="account-meta">Não informada</p></div>`;
    return `<div class="window-stack">${windows.map(w => `<div><div class="window-title"><span>${esc(w.label)}</span><strong class="num ${w.remaining === 0 ? 'danger' : ''}">${percent(w.remaining)}</strong></div><div class="track"><span style="width:${w.remaining || 0}%;background:${w.remaining === 0 ? 'var(--cp-bad)' : w.freshness === 'fresh' ? 'var(--cp-accent)' : 'var(--cp-dim)'}"></span></div><p class="window-reset">${reset(w.resetsAt)}</p><p class="account-meta">${freshness(w.freshness)} · ${age(w.ageSeconds)}</p></div>`).join('')}</div>`;
  }
  function ledger() {
    const search = state.search.toLocaleLowerCase('pt-BR');
    const list = snapshot.accounts.filter(a => (state.provider === 'all' || a.provider === state.provider) && (!state.account || state.account === a.id)).filter(a => [a.identity.label, a.id, name(a.provider), emails.get(a.id) || ''].some(s => s.toLocaleLowerCase('pt-BR').includes(search)));
    return `<div class="section-title"><h2>Cotas por conta</h2><span class="muted small">Janelas independentes</span></div><section class="panel"><div class="ledger-toolbar"><div class="search-wrap"><span class="search-symbol" aria-hidden="true">⌕</span><input id="account-search" aria-label="Buscar conta ou e-mail revelado" placeholder="Buscar conta ou e-mail revelado" value="${esc(state.search)}"></div><div class="actions"><select class="readonly-select" aria-label="Layout" disabled><option>Ledger</option></select><select class="readonly-select" aria-label="Configuração" disabled><option>Default</option></select></div></div><div id="account-rows">${list.length ? snapshot.providers.map(p => {
      const rows = list.filter(a => a.provider === p.provider);
      if (!rows.length) return '';
      return `<div class="ledger-group"><div class="ledger-group-head"><strong>${esc(name(p.provider))}</strong><span class="muted">${rows.length} contas</span></div>${rows.map(a => `<article class="account-row"><div class="account-identity"><div><h3 class="account-name">${esc(a.identity.label)}</h3><p class="account-meta">${esc(name(a.provider))} · ${a.active ? 'Login atual' : 'Fora de sessão'}<br>Última observação ${age(a.ageSeconds)}<br>${date(a.observedAt)}</p>${a.identity.basis === 'provider_email' ? '<p class="account-meta">Identidade pelo e-mail informado pelo provedor.</p>' : ''}${a.identity.hasEmail ? `<p class="account-meta">${emails.has(a.id) ? esc(emails.get(a.id)) + '<br>' : ''}<button class="text-button" data-email="${a.id}">${emails.has(a.id) ? 'Ocultar e-mail' : 'Revelar este e-mail'}</button></p>` : '<p class="account-meta">E-mail não informado.</p>'}</div><span class="badge ${a.availability.state === 'unavailable' ? 'bad' : a.freshness !== 'fresh' ? 'warn' : 'ok'}">${esc(statusLabel(a))}</span></div>${quotaColumn(a, 'weekly')}${quotaColumn(a, 'session')}${quotaColumn(a, 'model')}<div class="account-actions"><small class="muted">Créditos de reset desconhecidos</small><button class="btn btn-small" disabled title="${esc(a.resetAction.reason)}">Reset indisponível</button><span class="account-meta">${esc(a.resetAction.reason)}</span><button class="btn btn-small" data-refresh-account="${a.id}" ${a.active ? '' : 'disabled'}>Atualizar cota</button><button class="text-button" data-analyze="${a.id}">Analisar conta</button></div></article>`).join('')}</div>`;
    }).join('') : '<p class="empty muted">Nenhuma conta identificada nesta seleção. Fontes sem identidade permanecem no estado informado acima.</p>'}</div></section>`;
  }
  function historyScreen() {
    if (view.selectionRequired) return providers() + `<section class="panel empty">${esc(view.selectionRequired)}</section>`;
    return selectors() + view.groups.map(g => `<section class="panel summary-total">${groupSummary(g)}</section>${g.accounts.map(a => `<div class="section-title"><h2>${esc(a.label)}</h2></div>${chartSection(a, 'rate')}<section class="panel"><div class="panel-top"><h2>Consumo por ${state.period === '24h' ? 'hora' : 'dia'}</h2></div><div class="chart large" data-account="${a.accountId}" data-window="${a.windowId}" data-chart="bars"></div><p class="chart-inspection" aria-live="polite">Use o ponteiro ou as setas para inspecionar.</p><p class="coverage">Barras somam apenas intervalos contidos no período. Contorno tracejado indica cobertura incompleta.</p><details><summary>Intervalos não atribuídos e ciclos concluídos</summary><div class="table-scroll"><table class="data-table"><thead><tr><th>Intervalo</th><th>Consumo não atribuído</th></tr></thead><tbody>${a.unattributed.map(d => `<tr><td>${date(d.from)} a ${date(d.to)}</td><td>${fmt(d.consumedPp)} p.p.</td></tr>`).join('') || '<tr><td>Nenhum intervalo</td><td>—</td></tr>'}</tbody></table></div>${a.unusedCycles.map(c => `<p class="coverage">Reset confirmado ${date(c.resetAt)}. Se não houve ajuste não observado, o saldo não utilizado ficou entre 0 e ${fmt(c.upperBoundPp)} p.p. A última amostra foi em ${date(c.lastObservedAt)}.</p>`).join('')}</details></section><div class="section-title"><h2>Em que horas o consumo acelera?</h2><p>São Paulo · ${state.period}</p></div><section class="panel"><div class="heatmap-scroll">${heatmap(a)}</div><p class="chart-inspection" aria-live="polite">Células com cobertura parcial não representam consumo exato de toda a hora.</p></section>`).join('')}`).join('');
  }
  function heatmap(a) {
    const max = Math.max(1, ...a.heatmap.map(c => c.consumedPp));
    return `<div class="heatmap"><span></span>${Array.from({length: 24}, (_, hour) => `<span class="heat-label">${hour}h</span>`).join('')}${['Dom', 'Seg', 'Ter', 'Qua', 'Qui', 'Sex', 'Sáb'].map((day, weekday) => `<span class="heat-label">${day}</span>${a.heatmap.filter(c => c.weekday === weekday).map(c => `<button class="heat-cell" data-heat="${esc(`${day}, ${c.hour}h · ${fmt(c.consumedPp)} p.p. atribuídos · cobertura ${fmt(c.coverage * 100)}% · ${c.complete ? 'completa' : 'incompleta'}`)}" aria-label="${esc(`${day} ${c.hour} horas, ${fmt(c.consumedPp)} pontos, cobertura ${fmt(c.coverage * 100)} por cento`)}" style="background:color-mix(in srgb,var(--cp-accent) ${c.coverage ? 10 + c.consumedPp / max * 70 : 0}%,var(--cp-panel-alt));border-style:${c.complete ? 'solid' : 'dashed'}"></button>`).join('')}`).join('')}</div>`;
  }
  function forecastScreen() {
    if (view.selectionRequired) return providers() + `<section class="panel empty">${esc(view.selectionRequired)}</section>`;
    return selectors() + settings() + view.groups.map(g => `<section class="panel summary-total">${groupSummary(g)}</section>${g.accounts.map(a => {
      const account = snapshot.accounts.find(row => row.id === a.accountId);
      const w = account.windows.find(row => row.id === keyID(g.key));
      return `<div class="section-title"><h2>${esc(a.label)}</h2></div><div class="metric-grid"><div class="metric"><span class="metric-label">Saldo · ${esc(g.label)}</span><div class="metric-value num">${percent(w?.remaining)}</div><p class="metric-caption">${reset(w?.resetsAt)}</p></div><div class="metric"><span class="metric-label">Ritmo observado · ${state.lookback}</span><div class="metric-value num">${fmt(a.recentPpH, 2)} <small>p.p./h</small></div><p class="metric-caption">Entre amostras · cobertura ${fmt(a.rateCoverage.fraction * 100)}%</p></div><div class="metric"><span class="metric-label">Ritmo sustentável</span><div class="metric-value num">${fmt(a.sustainablePpH, 2)} <small>p.p./h</small></div><p class="metric-caption">Saldo dividido pelo tempo até o reset</p></div></div>${a.exhaustsAt ? `<p class="notice">Neste ritmo, o saldo pode acabar em ${date(a.exhaustsAt)}, antes da renovação.</p>` : ''}${chartSection(a, 'remaining')}<p class="assumption">Próximo reset informado ${date(w?.resetsAt)}. Projeção limitada entre zero e o teto da janela. Recorrências ${state.recurrence ? 'inferidas após o primeiro reset' : 'desativadas'}.</p>`;
    }).join('')}`).join('') + alerts();
  }
  function render() {
    tip.style.display = 'none';
    if (!snapshot || !view || JSON.stringify(selected()) !== JSON.stringify(committedSelection)) {
      screen.innerHTML = `<section class="panel empty"><h1>Monitor de assinaturas</h1><p role="alert">${esc(connectionError || 'Carregando seleção…')}</p>${connectionError ? '<button class="btn" data-action="retry">Tentar novamente</button>' : ''}</section>`;
      return;
    }
    for (const button of root.querySelectorAll('[data-nav]')) { if (button.dataset.nav === state.screen) button.setAttribute('aria-current', 'page'); else button.removeAttribute('aria-current'); }
    const content = state.screen === 'overview' ? overview() : state.screen === 'accounts' ? providers() + selectors() + ledger() : state.screen === 'history' ? historyScreen() : forecastScreen();
    screen.innerHTML = header() + (connectionError ? `<p class="notice" role="alert">${esc(connectionError)} Dados preservados da consulta em ${date(snapshot.generatedAt)}. Os estados abaixo pertencem àquela consulta.</p>` : "") + sourceStatuses() + content + '<footer class="footer"><span>Observações persistidas · horários de São Paulo</span><span>p.p. = pontos percentuais da cota</span></footer>';
    drawCharts();
  }
  function drawCharts() {
    for (const host of root.querySelectorAll('[data-chart]')) drawChart(host);
  }
  function drawChart(host) {
    const account = view.groups.flatMap(g => g.accounts).find(a => a.accountId === host.dataset.account && a.windowId === host.dataset.window);
    if (!account) return;
    const kind = host.dataset.chart;
    const group = view.groups.find(g => g.accounts.includes(account));
    const now = new Date(view.generatedAt).getTime();
    const series = kind === 'rate' ? account.rate.map(p => ({t: new Date(p.to).getTime(), value: p.ppH, note: `Média de 1 hora · ${date(p.from)} a ${date(p.to)} · cobertura ${fmt(p.coverage * 100)}%`, breakBefore: p.breakBefore})) : kind === 'bars' ? (state.period === '24h' ? account.hourly : account.daily).map(b => ({t: new Date(b.from).getTime(), value: b.coverage > 0 ? b.consumedPp : null, complete: b.complete, note: `${date(b.from)} a ${date(b.to)} · cobertura ${fmt(b.coverage * 100)}% · ${b.hasUnattributed ? 'Há intervalos não atribuídos nesta fronteira; consulte a lista abaixo' : 'Sem intervalos cruzando a fronteira'}`})) : account.observed.map(p => ({t: new Date(p.at).getTime(), value: p.remaining, note: 'Observação persistida. Intervalos entre amostras não são leituras exatas.'}));
    const future = kind === 'remaining' ? account.forecast.map(p => ({t: new Date(p.at).getTime(), value: p.remaining, note: p.assumption === 'reported' ? 'Reset informado pelo provedor' : p.assumption === 'inferred' ? 'Recorrência inferida' : 'Projeção estimada'})) : [];
    const all = [...series, ...future];
    if (!all.some(p => p.value != null)) { host.innerHTML = '<div class="chart-empty">Sem histórico observado nesta seleção.<br>O monitor continua coletando com o painel fechado.</div>'; return; }
    const width = Math.max(240, host.clientWidth);
    const height = host.clientHeight || 280;
    const m = {l: 44, r: 18, t: 22, b: 34};
    const plotWidth = width - m.l - m.r;
    const plotHeight = height - m.t - m.b;
    const periodMs = {'24h': 86400000, '7d': 604800000, '30d': 2592000000}[state.period];
    const minT = now - periodMs;
    const maxT = future.length ? Math.max(now, ...future.map(p => p.t)) : now;
    const yMax = kind === 'remaining' ? 100 : Math.max(1, ...all.filter(p => p.value != null).map(p => p.value)) * 1.15;
    const x = t => m.l + (t - minT) / Math.max(1, maxT - minT) * plotWidth;
    const y = value => m.t + (1 - value / yMax) * plotHeight;
    const unit = kind === 'remaining' ? '%' : kind === 'rate' ? ' p.p./h' : ' p.p.';
    const path = points => {
      let started = false;
      return points.map(p => { if (p.value == null) { started = false; return ''; } const command = started && !p.breakBefore ? 'L' : 'M'; started = true; return command + x(p.t).toFixed(2) + ',' + y(p.value).toFixed(2); }).join(' ');
    };
    const area = points => {
      const groups = [];
      let current = [];
      for (const p of points) { if (p.value == null || p.breakBefore) { if (current.length) groups.push(current); current = []; } if (p.value != null) current.push(p); }
      if (current.length) groups.push(current);
      return groups.map(points => path(points) + ` L${x(points.at(-1).t)},${y(0)} L${x(points[0].t)},${y(0)} Z`).join(' ');
    };
    const fillId = `fill-${kind}-${host.dataset.account}-${host.dataset.window}`;
    let svg = `<svg viewBox="0 0 ${width} ${height}" role="img" aria-label="${esc(group.label)} · ${esc(account.label)} · ${kind === 'rate' ? 'Ritmo observado em uma hora' : kind === 'bars' ? 'Consumo atribuído ao período' : 'Saldo observado e projetado'}. Use as setas para inspecionar."><defs><linearGradient id="${fillId}" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stop-color="var(--cp-accent)" stop-opacity=".25"/><stop offset="100%" stop-color="var(--cp-accent)" stop-opacity=".02"/></linearGradient></defs>`;
    for (let i = 0; i <= 4; i++) { const value = yMax * i / 4; svg += `<line class="chart-grid" x1="${m.l}" x2="${width - m.r}" y1="${y(value)}" y2="${y(value)}"/><text x="${m.l - 8}" y="${y(value) + 4}" text-anchor="end">${fmt(value)}</text>`; }
    const tickCount = width < 450 ? 3 : 5;
    for (let i = 0; i < tickCount; i++) { const t = minT + (maxT - minT) * i / (tickCount - 1); svg += `<text x="${x(t)}" y="${height - 12}" text-anchor="${i === 0 ? 'start' : i === tickCount - 1 ? 'end' : 'middle'}">${esc(date(t))}</text>`; }
    if (kind === 'bars') {
      const barWidth = Math.max(2, plotWidth / Math.max(1, series.length) * .65);
      for (const p of series) if (p.value != null) svg += `<rect x="${x(p.t)}" y="${y(p.value)}" width="${barWidth}" height="${Math.max(1, y(0) - y(p.value))}" fill="var(--cp-accent)" fill-opacity=".45" stroke="var(--cp-accent)" ${p.complete ? '' : 'stroke-dasharray="3 3"'}/>`;
    } else {
      svg += `<path d="${area(series)}" fill="url(#${fillId})"/><path class="chart-line" d="${path(series)}"/>`;
      if (future.length) svg += `<path d="${area(future)}" fill="url(#${fillId})"/><path class="chart-line chart-dashed" d="${path(future)}"/><line class="chart-guide" x1="${x(now)}" x2="${x(now)}" y1="${m.t}" y2="${height - m.b}"/><text x="${x(now) + 5}" y="14">Agora</text>`;
    }
    const inspectable = all.filter(p => p.t >= minT && p.t <= maxT);
    svg += `<g data-guide hidden><line class="chart-guide" y1="${m.t}" y2="${height - m.b}"/><circle r="4" class="chart-point"/></g><rect data-hit x="${m.l}" y="${m.t}" width="${plotWidth}" height="${plotHeight}" fill="transparent" tabindex="0" role="slider" aria-label="Inspecionar leituras de ${esc(account.label)}" aria-valuemin="0" aria-valuemax="${Math.max(0, inspectable.length - 1)}" aria-valuenow="0"/></svg>`;
    host.innerHTML = svg;
    const overlay = host.querySelector('[data-hit]');
    const guide = host.querySelector('[data-guide]');
    const inspection = host.nextElementSibling;
    let index = 0;
    function inspect(at) {
      index = Math.max(0, Math.min(inspectable.length - 1, at));
      const point = inspectable[index];
      if (!point) return;
      const text = `${date(point.t)} · ${point.value == null ? 'Sem leitura' : fmt(point.value, 2) + unit} · ${point.note}`;
      inspection.textContent = text;
      overlay.setAttribute('aria-valuenow', String(index));
      overlay.setAttribute('aria-valuetext', text);
      guide.removeAttribute('hidden');
      guide.querySelector('line').setAttribute('x1', x(point.t));
      guide.querySelector('line').setAttribute('x2', x(point.t));
      const dot = guide.querySelector('circle');
      dot.setAttribute('cx', x(point.t));
      dot.setAttribute('cy', y(point.value || 0));
      dot.style.display = point.value == null ? 'none' : '';
      return text;
    }
    overlay.addEventListener('pointermove', event => {
      const rect = host.getBoundingClientRect();
      const t = minT + ((event.clientX - rect.left) / rect.width * width - m.l) / plotWidth * (maxT - minT);
      let nearest = 0;
      for (let i = 1; i < inspectable.length; i++) if (Math.abs(inspectable[i].t - t) < Math.abs(inspectable[nearest].t - t)) nearest = i;
      const text = inspect(nearest);
      if (!text) return;
      tip.textContent = text;
      tip.style.display = 'block';
      tip.style.left = Math.max(10, Math.min(window.innerWidth - tip.offsetWidth - 10, event.clientX + 12)) + 'px';
      tip.style.top = Math.max(10, event.clientY - tip.offsetHeight - 12) + 'px';
    });
    overlay.addEventListener('pointerleave', () => { tip.style.display = 'none'; guide.setAttribute('hidden', ''); });
    overlay.addEventListener('focus', () => inspect(index));
    overlay.addEventListener('keydown', event => {
      if (!['ArrowRight', 'ArrowLeft', 'Home', 'End'].includes(event.key)) return;
      event.preventDefault();
      inspect(event.key === 'Home' ? 0 : event.key === 'End' ? inspectable.length - 1 : index + (event.key === 'ArrowRight' ? 1 : -1));
    });
  }
  function toast(message) {
    const element = document.getElementById('toast');
    clearTimeout(toastTimer);
    element.textContent = message;
    element.style.display = 'block';
    toastTimer = setTimeout(() => element.style.display = 'none', 4500);
  }
  function modal(title, text) {
    lastFocus = document.activeElement;
    document.getElementById('modal-title').textContent = title;
    document.getElementById('modal-body').innerHTML = '<pre></pre>';
    document.querySelector('#modal-body pre').textContent = text;
    document.getElementById('modal-actions').innerHTML = '<button class="btn" data-action="close-modal">Fechar</button>';
    document.getElementById('modal-backdrop').hidden = false;
    document.querySelector('.modal').focus();
  }
  function closeModal() {
    document.getElementById('modal-backdrop').hidden = true;
    if (lastFocus?.isConnected) lastFocus.focus();
  }
  root.addEventListener('click', async event => {
    const button = event.target.closest('button');
    if (!button || button.disabled) return;
    const d = button.dataset;
    if (d.action === 'retry') { await load(); return; }
    if (d.action === 'close-modal') { closeModal(); return; }
    if (!snapshot) return;
    try {
      if (d.nav) { state.screen = d.nav; render(); return; }
      if (d.filter) { state.provider = d.filter; state.account = ''; state.window = 'weekly'; await load(); return; }
      if (d.chartTab) { state.chart = d.chartTab; render(); return; }
      if (d.analyze) { const account = snapshot.accounts.find(a => a.id === d.analyze); state.provider = account.provider; state.account = account.id; state.screen = 'forecast'; await load(); return; }
      if (d.email) {
        if (emails.has(d.email)) emails.delete(d.email);
        else { const result = await post('/api/v1/accounts/' + d.email + '/email', {}); emails.set(d.email, result.email); }
        render();
        return;
      }
      if (d.heat) { button.closest('.panel').querySelector('.chart-inspection').textContent = d.heat; return; }
      if (d.refreshAccount || d.action === 'refresh') {
        const result = await post('/api/v1/refresh', d.refreshAccount ? {account: d.refreshAccount} : {});
        const selected = Object.values(result);
        toast(selected.some(s => s.queued || s.inFlightSince) ? 'Consulta na fila ou em andamento. O painel permanece disponível.' : 'Intervalo de coleta ou backoff preservado. Veja a próxima consulta permitida.');
        await load();
        return;
      }
      if (d.action === 'collapse') { state.collapsed = !state.collapsed; render(); return; }
      if (d.action === 'snapshot') { modal('Dados para a escalação', JSON.stringify(await api('/api/v1/snapshot?format=t3-usage'), null, 2)); }
    } catch (error) { toast(error.message); }
  });
  root.addEventListener('change', async event => {
    const input = event.target;
    if (input.dataset.select) { state[input.dataset.select] = input.value; if (input.dataset.select === 'account') state.window = 'weekly'; await load(); }
    if (input.dataset.toggle === 'recurrence') { state.recurrence = input.checked; await load(); }
  });
  root.addEventListener('input', event => {
    if (event.target.id !== 'account-search') return;
    state.search = event.target.value;
    const start = event.target.selectionStart;
    const wrapper = document.createElement('div');
    wrapper.innerHTML = ledger();
    document.getElementById('account-rows').innerHTML = wrapper.querySelector('#account-rows').innerHTML;
    event.target.setSelectionRange(start, start);
  });
  document.getElementById('modal-backdrop').addEventListener('click', event => { if (event.target.id === 'modal-backdrop') closeModal(); });
  document.addEventListener('keydown', event => {
    if (event.key === 'Escape') closeModal();
    if (event.key !== 'Tab' || document.getElementById('modal-backdrop').hidden) return;
    const buttons = [...document.querySelectorAll('.modal button')];
    const first = buttons[0], last = buttons.at(-1);
    if (event.shiftKey && (document.activeElement === first || document.activeElement.classList.contains('modal'))) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && (document.activeElement === last || document.activeElement.classList.contains('modal'))) { event.preventDefault(); first.focus(); }
  });
  new ResizeObserver(() => { if (snapshot && view) drawCharts(); }).observe(screen);
  setInterval(() => { if (authenticated !== false && !screen.contains(document.activeElement) && !screen.querySelector('details[open]') && document.getElementById('modal-backdrop').hidden) load(); }, 30000);
  load();
})();
