#!/usr/bin/env python3
"""Build Quota History from the shipped v1 markup, charts and styles."""

import hashlib
import json
import re
import sys
from pathlib import Path

repository = Path(__file__).resolve().parent.parent
source = repository / "management/v1-source"
manifest = json.loads((source / "source.json").read_text())
for name, digest in manifest["files"].items():
    if hashlib.sha256((source / name).read_bytes()).hexdigest() != digest:
        raise SystemExit("V1 reference changed. Restore the v1 source instead of redesigning Quota History.")
script = (source / "app.js").read_text()
checkout = Path(sys.argv[1])
if not (checkout / "package.json").is_file() or not (checkout / "tsconfig.json").is_file():
    raise SystemExit("Use the management checkout produced by scripts/build-management.sh.")
target = checkout / "src/features/quotaHistory"
target.mkdir(parents=True, exist_ok=True)


def function(name):
    match = re.search(r"^  (?:async )?function " + name + r"\(", script, re.M)
    if match is None:
        raise SystemExit(f"Missing v1 function: {name}")
    first_line = script[match.start():script.index("\n", match.start())]
    if first_line.endswith("}"):
        return first_line
    end = script.index("\n  }", match.start()) + len("\n  }")
    return script[match.start():end]


prefix = '''export function mountV1History(shadow, request, refresh) {
  const root = shadow.querySelector('#cp-monitor');
  const screen = shadow.querySelector('#screen');
  const tip = shadow.querySelector('#chart-tooltip');
  const elementById = id => shadow.getElementById(id);
  const focused = () => shadow.activeElement || document.activeElement;
  const state = {screen: 'history', provider: 'all', account: '', window: 'weekly', period: '24h', lookback: '1h', horizon: '7d', recurrence: true};
  const providerNames = {claude: 'Claude', codex: 'Codex', grok: 'Grok / xAI', muse: 'Muse / Meta'};
  let snapshot = null, view = null, committedSelection = null, connectionError = '';
  let authenticated = true, requestVersion = 0, sessionGeneration = 0;
  let pendingUpdate = null, pendingError = '', pausedAt = null, toastTimer;
  let alive = true, loadController = new AbortController();
  let refreshing = false, dirty = false;
  const events = new AbortController();
'''
prefix += script[script.index("  const esc ="):script.index("  async function api(")]
prefix += '''
  async function api(path, options = {}, version = null) {
    const response = await request(path, {...options, signal: loadController.signal});
    if (!alive || (version !== null && version !== requestVersion)) throw new DOMException('Consulta substituída.', 'AbortError');
    return response;
  }
  const post = async () => { refresh(); return {history: {queued: true}}; };
'''
names = ["selectionKeys", "selected", "interacting", "clearPending", "applyUpdate",
         "deferDisplay", "flushPending", "load", "header", "sourceStatuses", "providers",
         "keyID", "selectors", "groupSummary", "insufficient", "chartSection",
         "historyScreen", "heatmap", "drawCharts", "drawChart", "toast"]
parts = []
for name in names:
    body = function(name)
    body = body.replace("document.getElementById", "elementById").replace("document.activeElement", "focused()")
    if name == "load":
        body = body.replace("const version = ++requestVersion;", "loadController.abort(); loadController = new AbortController();\n    const version = ++requestVersion; refreshing = true;")
        body = body.replace("finally { if (version === requestVersion) root.setAttribute('aria-busy', 'false'); }", "finally { if (version === requestVersion) { root.setAttribute('aria-busy', 'false'); refreshing = false; if (dirty && alive) { dirty = false; void updated(); } } }")
        body = body.replace("if (!snapshot || !view || JSON.stringify(requested) !== JSON.stringify(committedSelection)) render();", "if (!snapshot || !view) render();\n    else if (committedSelection) Object.assign(state, committedSelection);")
    if name == "header":
        body = body.replace("history: ['Histórico de consumo'", "history: ['Quota History'")
        body = body.replace("Fontes nativas · Histórico local", "Credenciais do proxy · Histórico local")
    body = body.replace('data-filter="${p.provider}"', 'data-filter="${esc(p.provider)}"').replace('data-filter="${p}"', 'data-filter="${esc(p)}"')
    if name == "drawChart":
        body = body.replace("O monitor continua coletando com o painel fechado.", "As próximas leituras aparecerão nesta seleção.")
        body = body.replace("const minT = now - periodMs;", "const minT = state.period === 'all' ? Math.min(now - 86400000, ...series.map(p => p.t)) : now - periodMs;")
    if name == "selectors":
        body = body.replace("['30d', 'Últimos 30 dias']", "['30d', 'Últimos 30 dias'], ['all', 'Todo o histórico']")
    parts.append(body)
render = function("render").replace("document.getElementById", "elementById")
render = render.replace("<h1>Monitor de assinaturas</h1>", "<h1>Quota History</h1>")
render = render.replace("state.screen === 'overview' ? overview() : state.screen === 'accounts' ? providers() + selectors() + ledger() : state.screen === 'history' ? historyScreen() : forecastScreen()", "historyScreen()")
parts.append(render)
suffix = '''
  root.addEventListener('click', async event => {
    const button = event.target.closest('button');
    if (!button || button.disabled) return;
    const d = button.dataset;
    if (d.action === 'apply-pending') { flushPending(true); return; }
    if (d.action === 'retry') { await load(); return; }
    if (!snapshot) return;
    try {
      if (d.filter) { state.provider = d.filter; state.account = ''; state.window = 'weekly'; await load(); return; }
      if (d.heat) { button.closest('.panel').querySelector('.chart-inspection').textContent = d.heat; return; }
      if (d.action === 'refresh') {
        await post();
        toast('Consulta na fila ou em andamento. O painel permanece disponível.');
        await load();
      }
    } catch (error) { if (error.name !== 'AbortError') toast(error.message); }
  }, {signal: events.signal});
  root.addEventListener('change', async event => {
    const input = event.target;
    if (input.dataset.select) { state[input.dataset.select] = input.value; if (input.dataset.select === 'account') state.window = 'weekly'; await load(); }
  }, {signal: events.signal});
  const observer = new ResizeObserver(() => { if (snapshot && view) drawCharts(); });
  observer.observe(screen);
  root.addEventListener('focusout', () => setTimeout(() => { if (alive) flushPending(); }, 0), {signal: events.signal});
  root.addEventListener('toggle', event => {
    const details = event.target;
    const closingFocusedSummary = details.tagName === 'DETAILS' && !details.open && !screen.querySelector('details[open]') && focused() === details.querySelector('summary');
    flushPending(closingFocusedSummary);
  }, {capture: true, signal: events.signal});
  const updated = async () => {
    if (!alive) return;
    if (refreshing) { dirty = true; return; }
    dirty = false;
    await load(true);
  };
  window.addEventListener('quota-history-updated', updated);
  const timer = setInterval(updated, 30000);
  void load();
  return () => {
    alive = false;
    requestVersion++;
    sessionGeneration++;
    events.abort();
    loadController.abort();
    observer.disconnect();
    clearInterval(timer);
    clearTimeout(toastTimer);
    window.removeEventListener('quota-history-updated', updated);
  };
}
'''
(target / "v1Runtime.js").write_text(prefix + "\n".join(parts) + suffix)
(target / "v1Style.css").write_text((source / "style.css").read_text().replace(":root{", ":host{", 1)
    + '\n:host{display:block;min-width:0;background:var(--cp-bg);font-family:var(--cp-font);color:var(--cp-text);font-size:14px;line-height:1.5}.cp-root{min-width:0}\n')
