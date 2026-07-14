const app = document.getElementById('app');
const pageTitle = document.getElementById('page-title');
const pageSubtitle = document.getElementById('page-subtitle');
const statusPill = document.getElementById('status-pill');
let currentPage = 'overview';

const API = {
  async get(path) {
    const res = await fetch(path);
    if (!res.ok) throw new Error(await res.text());
    return res.json();
  },
  async post(path, body) {
    const res = await fetch(path, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(body || {})
    });
    if (!res.ok) throw new Error(await res.text());
    return res.json();
  }
};

function esc(v) {
  return String(v ?? '').replace(/[&<>'"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[c]));
}
function num(v) { return Number(v || 0).toLocaleString(); }
function routePill(route) { return `<span class="pill ${route === 'apikey' ? 'warn' : 'good'}">${esc(route || 'local')}</span>`; }
function setPageMeta(title, subtitle) { pageTitle.textContent = title; pageSubtitle.textContent = subtitle; }

async function refreshStatus() {
  try {
    const s = await API.get('/api/status');
    const auth = s.auth || {};
    statusPill.textContent = auth.local_available ? 'local auth ok' : (auth.apikey_available ? 'api key available' : 'auth missing');
    statusPill.className = 'pill ' + (auth.local_available || auth.apikey_available ? 'good' : 'bad');
  } catch (err) {
    statusPill.textContent = 'offline';
    statusPill.className = 'pill bad';
  }
}

async function render() {
  app.innerHTML = '<div class="card">Loading...</div>';
  document.querySelectorAll('.nav').forEach(n => n.classList.toggle('active', n.dataset.page === currentPage));
  try {
    if (currentPage === 'overview') await renderOverview();
    if (currentPage === 'logs') await renderLogs();
    if (currentPage === 'stats') await renderStats();
    if (currentPage === 'models') await renderModels();
    if (currentPage === 'settings') await renderSettings();
  } catch (err) {
    app.innerHTML = `<div class="card error"><strong>Failed:</strong><pre>${esc(err.message || err)}</pre></div>`;
  }
  refreshStatus();
}

async function renderOverview() {
  setPageMeta('Overview', 'Proxy status, auth, and recent Claude usage');
  const o = await API.get('/api/overview');
  const s = o.stats || {};
  const p = o.provider || {};
  const auth = p.auth || {};
  app.innerHTML = `
    <div class="grid cols-4">
      ${metric('Requests', s.total_requests)}
      ${metric('Errors', s.total_errors)}
      ${metric('Input tokens', s.total_logical_input_tokens || s.total_input_tokens)}
      ${metric('Output tokens', s.total_output_tokens)}
    </div>
    <div class="grid cols-2" style="margin-top:14px">
      <div class="card">
        <h2>Auth</h2>
        <p>Local Keychain: ${auth.local_available ? '<span class="pill good">available</span>' : '<span class="pill bad">unavailable</span>'}</p>
        <p>API key: ${auth.apikey_available ? '<span class="pill good">configured</span>' : '<span class="pill warn">not configured</span>'}</p>
        ${auth.local_expires_in ? `<p class="muted">Local token expires in ${esc(auth.local_expires_in)}</p>` : ''}
        ${auth.local_error ? `<p class="error">${esc(auth.local_error)}</p>` : ''}
      </div>
      <div class="card">
        <h2>Models</h2>
        <p>${num(p.total)} configured models</p>
        <p>${num(p.local)} local / ${num(p.apikey)} API key</p>
        <div class="actions" style="margin-top:12px"><button onclick="switchPage('models')">Manage models</button><button onclick="discoverModels()" class="btn primary">Discover models</button></div>
      </div>
    </div>
    <div class="card" style="margin-top:14px">
      <div class="split"><h2>Recent requests</h2><button onclick="switchPage('logs')">Open logs</button></div>
      ${logsTable(o.recent || [])}
    </div>`;
}

function metric(label, value) {
  return `<div class="card"><div class="label">${esc(label)}</div><div class="metric">${num(value)}</div></div>`;
}

async function renderLogs() {
  setPageMeta('Logs', 'Recent proxied Claude API calls');
  const logs = await API.get('/api/logs?limit=100');
  app.innerHTML = `<div class="card"><div class="split"><h2>Request logs</h2><button onclick="render()">Refresh</button></div>${logsTable(logs || [])}</div>`;
}

function logsTable(logs) {
  if (!logs.length) return '<p class="muted">No logs yet.</p>';
  return `<table><thead><tr><th>Time</th><th>Model</th><th>Route</th><th>Status</th><th>Tokens</th><th>Latency</th><th>Error</th></tr></thead><tbody>` + logs.map(l => {
    const tokens = (l.tokens || {});
    const total = (tokens.input_tokens || 0) + (tokens.output_tokens || 0) + (tokens.cache_read_tokens || 0) + (tokens.cache_create_tokens || 0);
    return `<tr>
      <td>${esc(new Date(l.timestamp).toLocaleString())}</td>
      <td><code>${esc(l.model)}</code></td>
      <td>${esc(l.route)}</td>
      <td>${Number(l.status) >= 400 ? '<span class="pill bad">' + esc(l.status) + '</span>' : '<span class="pill good">' + esc(l.status || '-') + '</span>'}</td>
      <td>${num(total)}</td>
      <td>${esc(l.latency || '')}</td>
      <td class="error">${esc(l.error || '')}</td>
    </tr>`;
  }).join('') + '</tbody></table>';
}

async function renderStats() {
  setPageMeta('Stats', 'Token totals and per-model breakdown');
  const [stats, daily, routes, totals] = await Promise.all([
    API.get('/api/stats'), API.get('/api/stats/daily?days=30'), API.get('/api/stats/routes'), API.get('/api/stats/tokens')
  ]);
  const models = Object.values(stats.by_model || {}).sort((a,b) => (b.total_tokens || 0) - (a.total_tokens || 0));
  app.innerHTML = `
    <div class="grid cols-4">
      ${metric('Total tokens', totals.total_tokens || stats.total_tokens)}
      ${metric('Fresh input', totals.fresh_input || stats.total_fresh_input_tokens)}
      ${metric('Cache read', totals.cache_read || stats.total_cache_read_tokens)}
      ${metric('Cache create', totals.cache_create || stats.total_cache_create_tokens)}
    </div>
    <div class="grid cols-2" style="margin-top:14px">
      <div class="card"><h2>Routes</h2>${routesTable(routes || [])}</div>
      <div class="card"><h2>Last 30 days</h2>${dailyTable(daily || [])}</div>
    </div>
    <div class="card" style="margin-top:14px"><h2>By model</h2>${modelsTable(models)}</div>`;
}

function routesTable(rows) {
  if (!rows.length) return '<p class="muted">No route stats.</p>';
  return `<table><thead><tr><th>Route</th><th>Requests</th><th>Tokens</th></tr></thead><tbody>${rows.map(r => `<tr><td>${esc(r.route)}</td><td>${num(r.requests)}</td><td>${num(r.total_tokens)}</td></tr>`).join('')}</tbody></table>`;
}
function dailyTable(rows) {
  if (!rows.length) return '<p class="muted">No daily stats.</p>';
  return `<table><thead><tr><th>Day</th><th>Requests</th><th>Tokens</th></tr></thead><tbody>${rows.slice(-10).map(r => `<tr><td>${esc(r.day)}</td><td>${num(r.requests)}</td><td>${num(r.total_tokens)}</td></tr>`).join('')}</tbody></table>`;
}
function modelsTable(rows) {
  if (!rows.length) return '<p class="muted">No model stats.</p>';
  return `<table><thead><tr><th>Model</th><th>Requests</th><th>Errors</th><th>Input</th><th>Output</th><th>Total</th></tr></thead><tbody>${rows.map(m => `<tr><td><code>${esc(m.model)}</code></td><td>${num(m.total_requests)}</td><td>${num(m.total_errors)}</td><td>${num(m.total_logical_input_tokens || m.total_input_tokens)}</td><td>${num(m.total_output_tokens)}</td><td>${num(m.total_tokens)}</td></tr>`).join('')}</tbody></table>`;
}

async function renderModels() {
  setPageMeta('Models', 'Discovered and manually configured Claude models');
  const models = await API.get('/api/models');
  app.innerHTML = `
    <div class="stack">
      <div class="card">
        <div class="split"><h2>Model discovery</h2><div class="actions"><button onclick="discoverModels('local')">Discover via local</button><button onclick="discoverModels('apikey')">Discover via API key</button><button class="btn primary" onclick="discoverModels()">Auto discover</button></div></div>
        <p class="muted">Discovery uses Anthropic <code>GET /v1/models</code>. It only adds/updates metadata and never deletes manual models.</p>
        <div id="discover-result"></div>
      </div>
      <div class="card">
        <h2>Add manual model</h2>
        <div class="form-row four">
          <div class="field"><label>Model ID</label><input id="add-model-id" placeholder="claude-new-model"></div>
          <div class="field"><label>Display name</label><input id="add-model-name" placeholder="Claude New Model"></div>
          <div class="field"><label>Route</label><select id="add-model-route"><option value="local">local</option><option value="apikey">apikey</option></select></div>
          <button class="btn primary" onclick="addModel()">Add</button>
        </div>
      </div>
      <div class="card"><h2>Configured models</h2>${modelsConfigTable(models || [])}</div>
    </div>`;
}

function modelsConfigTable(models) {
  if (!models.length) return '<p class="muted">No models configured.</p>';
  return `<table><thead><tr><th>Model</th><th>Display</th><th>Route</th><th>Last seen</th><th>Limits</th><th></th></tr></thead><tbody>${models.map(m => `<tr>
    <td><code>${esc(m.name)}</code>${m.discovered ? ' <span class="pill good">discovered</span>' : ''}</td>
    <td>${esc(m.display_name || '')}</td>
    <td><select onchange="setModelRoute('${esc(m.name)}', this.value)"><option value="local" ${m.route !== 'apikey' ? 'selected' : ''}>local</option><option value="apikey" ${m.route === 'apikey' ? 'selected' : ''}>apikey</option></select></td>
    <td>${m.last_seen ? esc(new Date(m.last_seen).toLocaleString()) : '<span class="muted">manual</span>'}</td>
    <td>${m.max_input_tokens ? num(m.max_input_tokens) + ' in' : ''}${m.max_tokens ? ' / ' + num(m.max_tokens) + ' out' : ''}</td>
    <td><button class="btn danger" onclick="removeModel('${esc(m.name)}')">Remove</button></td>
  </tr>`).join('')}</tbody></table>`;
}

async function renderSettings() {
  setPageMeta('Settings', 'Credentials, redirects, and runtime configuration');
  const [cfg, keys, redirects] = await Promise.all([API.get('/api/config'), API.get('/api/keys'), API.get('/api/redirects')]);
  app.innerHTML = `
    <div class="stack">
      <div class="card">
        <h2>Runtime</h2>
        <p>Proxy: <code>${esc(cfg.listen)}</code> · Admin: <code>${esc(cfg.admin_listen)}</code> · Data: <code>${esc(cfg.data_dir)}</code></p>
        <p class="muted">Set <code>CLAUDE_PROXY_CONFIG</code> for a custom config file and <code>CLAUDE_PROXY_LOG=debug</code> for debug logs.</p>
      </div>
      <div class="card">
        <h2>Default Claude route</h2>
        <p class="muted">Choose whether unknown models and newly added models use local Claude Code Keychain OAuth or an Anthropic API key. Use the checkbox to switch all configured models immediately.</p>
        <div class="form-row" style="margin-top:12px">
          <div class="field"><label>Auth source</label><select id="claude-source"><option value="keychain" ${(cfg.claude?.source || 'keychain') !== 'apikey' ? 'selected' : ''}>Local / Keychain</option><option value="apikey" ${(cfg.claude?.source || 'keychain') === 'apikey' ? 'selected' : ''}>API key</option></select></div>
          <label class="field"><span>Apply to existing model routes</span><input id="source-apply-models" type="checkbox" checked></label>
          <button class="btn primary" onclick="setClaudeSource()">Save route</button>
        </div>
        <div id="source-result" class="muted" style="margin-top:10px"></div>
      </div>
      <div class="card">
        <div class="split"><h2>Claude token</h2><button onclick="refreshToken()">Refresh Keychain token</button></div>
        <div id="token-result" class="muted"></div>
      </div>
      <div class="card">
        <h2>API keys</h2>
        ${apiKeysTable(keys || [])}
        <h2 style="margin-top:18px">Add API key</h2>
        <div class="form-row four">
          <div class="field"><label>Label</label><input id="key-label" placeholder="Production"></div>
          <div class="field"><label>API key</label><input id="key-value" type="password" placeholder="sk-ant-..."></div>
          <div class="field"><label>Base URL</label><input id="key-base" placeholder="https://api.anthropic.com"></div>
          <button class="btn primary" onclick="addAPIKey()">Add</button>
        </div>
        <div class="actions" style="margin-top:10px"><button onclick="testAPIKeyInput()">Test unsaved key</button><span id="key-test-result" class="muted"></span></div>
      </div>
      <div class="card">
        <h2>Model redirects</h2>
        ${redirectsTable(redirects || {})}
        <div class="form-row" style="margin-top:12px">
          <div class="field"><label>From</label><input id="redir-from" placeholder="claude-old-id"></div>
          <div class="field"><label>To (empty deletes)</label><input id="redir-to" placeholder="claude-new-id"></div>
          <button onclick="setRedirect()" class="btn primary">Save</button>
        </div>
      </div>
    </div>`;
}

function apiKeysTable(keys) {
  if (!keys.length) return '<p class="muted">No API keys configured.</p>';
  return `<table><thead><tr><th>Label</th><th>Key</th><th>Base URL</th><th></th></tr></thead><tbody>${keys.map(k => `<tr><td>${esc(k.label || '')}</td><td><code>${esc(k.api_key)}</code></td><td>${esc(k.base_url || 'default')}</td><td>${k.id === '_legacy' ? '<span class="muted">legacy</span>' : `<button class="btn danger" onclick="removeAPIKey('${esc(k.id)}')">Remove</button>`}</td></tr>`).join('')}</tbody></table>`;
}
function redirectsTable(redirs) {
  const rows = Object.entries(redirs);
  if (!rows.length) return '<p class="muted">No redirects configured.</p>';
  return `<table><thead><tr><th>From</th><th>To</th></tr></thead><tbody>${rows.map(([from,to]) => `<tr><td><code>${esc(from)}</code></td><td><code>${esc(to)}</code></td></tr>`).join('')}</tbody></table>`;
}

async function switchPage(page) { currentPage = page; await render(); }
async function discoverModels(route) {
  const el = document.getElementById('discover-result');
  if (el) el.innerHTML = '<p class="muted">Discovering...</p>';
  const res = await API.post('/api/models/discover', route ? { route } : {});
  if (el) el.innerHTML = `<div class="notice">Seen ${num(res.seen)}, added ${num(res.added)}, updated ${num(res.updated)} via ${esc(res.route)}/${esc(res.source)}.</div>`;
  if (currentPage !== 'models') currentPage = 'models';
  setTimeout(render, 700);
}
async function addModel() {
  await API.post('/api/models/add', { model: val('add-model-id'), display_name: val('add-model-name'), route: val('add-model-route') });
  render();
}
async function removeModel(model) {
  if (!confirm(`Remove ${model}?`)) return;
  await API.post('/api/models/remove', { model });
  render();
}
async function setModelRoute(model, route) {
  await API.post('/api/models/route', { model, route });
}
async function setClaudeSource() {
  const out = document.getElementById('source-result');
  const source = val('claude-source');
  const applyToModels = Boolean(document.getElementById('source-apply-models')?.checked);
  if (out) out.textContent = 'Saving...';
  const res = await API.post('/api/config/source', { source, apply_to_models: applyToModels });
  if (out) {
    out.textContent = `Saved: ${res.source === 'apikey' ? 'API key' : 'Local / Keychain'}${res.apply_to_models ? ' and updated existing model routes.' : '.'}`;
    out.className = 'pill good';
  }
  refreshStatus();
}
async function addAPIKey() {
  await API.post('/api/keys/add', { label: val('key-label'), api_key: val('key-value'), base_url: val('key-base') });
  render();
}
async function removeAPIKey(id) {
  if (!confirm('Remove this API key?')) return;
  await API.post('/api/keys/remove', { id });
  render();
}
async function testAPIKeyInput() {
  const out = document.getElementById('key-test-result');
  out.textContent = 'Testing...';
  const res = await API.post('/api/keys/test', { api_key: val('key-value'), base_url: val('key-base') });
  out.textContent = `${res.success ? 'OK' : 'Failed'}: ${res.message} (${res.latency_ms || 0}ms)`;
  out.className = res.success ? 'pill good' : 'pill bad';
}
async function refreshToken() {
  const out = document.getElementById('token-result');
  out.textContent = 'Refreshing...';
  const res = await API.post('/api/token/refresh', {});
  out.textContent = res.status === 'ok' ? 'Token refreshed.' : (res.message || 'Refresh failed');
}
async function setRedirect() {
  await API.post('/api/redirects/set', { from: val('redir-from'), to: val('redir-to') });
  render();
}
function val(id) { return (document.getElementById(id)?.value || '').trim(); }

document.querySelectorAll('.nav').forEach(btn => btn.addEventListener('click', () => switchPage(btn.dataset.page)));
document.getElementById('refresh-btn').addEventListener('click', render);
window.switchPage = switchPage;
window.discoverModels = discoverModels;
window.addModel = addModel;
window.removeModel = removeModel;
window.setModelRoute = setModelRoute;
window.setClaudeSource = setClaudeSource;
window.addAPIKey = addAPIKey;
window.removeAPIKey = removeAPIKey;
window.testAPIKeyInput = testAPIKeyInput;
window.refreshToken = refreshToken;
window.setRedirect = setRedirect;

refreshStatus();
render();
setInterval(refreshStatus, 10000);
