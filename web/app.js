const app = document.getElementById('app');
const pageKicker = document.getElementById('page-kicker');
const pageTitle = document.getElementById('page-title');
const pageSubtitle = document.getElementById('page-subtitle');
const statusPill = document.getElementById('status-pill');
const logoutBtn = document.getElementById('logout-btn');
const adminTokenKey = 'claude-proxy-admin-token';
let currentPage = 'usage';
let usageRange = '24h';

class APIError extends Error {
  constructor(status, message) {
    super(message);
    this.status = status;
  }
}

function adminToken() {
  return sessionStorage.getItem(adminTokenKey) || '';
}

const API = {
  async request(path, options, token) {
    const headers = new Headers(options?.headers || {});
    const credential = token === undefined ? adminToken() : token;
    if (credential) headers.set('authorization', 'Bearer ' + credential);
    const res = await fetch(path, { ...(options || {}), headers });
    if (!res.ok) throw new APIError(res.status, await res.text());
    return res.json();
  },
  get(path, token) {
    return this.request(path, {}, token);
  },
  post(path, body) {
    return this.request(path, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(body || {})
    });
  }
};

function esc(v) {
  return String(v ?? '').replace(/[&<>'"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[c]));
}
function num(v) { return Number(v || 0).toLocaleString(); }
function money(v) {
  const n = Number(v || 0);
  if (!Number.isFinite(n)) return '—';
  if (n >= 100) return `$${n.toFixed(0)}`;
  if (n >= 1) return `$${n.toFixed(2)}`;
  if (n > 0) return `$${n.toFixed(4)}`;
  return '$0.00';
}
function pct(v) {
  if (v == null || v === '') return null;
  const n = Number(v);
  return Number.isFinite(n) ? n : null;
}
function whenLocal(iso) {
  if (!iso) return '';
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}
function setPageMeta(kicker, title, subtitle) {
  pageKicker.textContent = kicker;
  pageTitle.textContent = title;
  pageSubtitle.textContent = subtitle;
}
function val(id) { return (document.getElementById(id)?.value || '').trim(); }

function rangeButtons(active) {
  return ['24h','7d','30d','all'].map(r =>
    `<button type="button" class="${r === active ? 'active' : ''}" onclick="setUsageRange('${r}')">${r === 'all' ? 'All' : r}</button>`
  ).join('');
}

async function refreshStatus() {
  if (!adminToken()) return;
  try {
    const s = await API.get('/api/status');
    const auth = s.auth || {};
    statusPill.textContent = auth.local_available ? 'claude login ok' : (auth.apikey_available ? 'api key ready' : 'auth missing');
    statusPill.className = 'chip ' + (auth.local_available || auth.apikey_available ? 'good' : 'bad');
  } catch {
    statusPill.textContent = 'offline';
    statusPill.className = 'chip bad';
  }
}

function renderLogin(message) {
  document.querySelectorAll('.nav').forEach(n => { n.disabled = true; n.classList.remove('active'); });
  logoutBtn.hidden = true;
  statusPill.textContent = 'locked';
  statusPill.className = 'chip';
  setPageMeta('Security', 'Unlock the dashboard', 'Enter the separate admin token for this proxy');
  app.innerHTML = `<form class="panel login" onsubmit="loginAdmin(event)">
    <h2>Admin token</h2>
    <p class="hint">The token stays in this browser tab and is sent only to authenticated dashboard API calls.</p>
    <div class="field"><label for="admin-token">Token</label><input id="admin-token" type="password" autocomplete="current-password" autofocus required></div>
    <button class="primary" type="submit">Unlock</button>
    <div id="login-result" class="error">${esc(message || '')}</div>
  </form>`;
}

async function loginAdmin(event) {
  event.preventDefault();
  const token = val('admin-token');
  const result = document.getElementById('login-result');
  if (result) result.textContent = 'Checking…';
  try {
    await API.get('/api/status', token);
    sessionStorage.setItem(adminTokenKey, token);
    document.querySelectorAll('.nav').forEach(n => { n.disabled = false; });
    logoutBtn.hidden = false;
    await render();
  } catch (err) {
    if (result) result.textContent = err.status === 401 ? 'Invalid admin token.' : (err.message || 'Login failed.');
  }
}

function logoutAdmin() {
  sessionStorage.removeItem(adminTokenKey);
  renderLogin();
}

async function render() {
  if (!adminToken()) {
    renderLogin();
    return;
  }
  logoutBtn.hidden = false;
  app.innerHTML = '<div class="panel empty">Loading…</div>';
  document.querySelectorAll('.nav').forEach(n => n.classList.toggle('active', n.dataset.page === currentPage));
  try {
    if (currentPage === 'usage') await renderUsage();
    if (currentPage === 'requests') await renderRequests();
    if (currentPage === 'routing') await renderRouting();
    if (currentPage === 'access') await renderAccess();
  } catch (err) {
    if (err.status === 401) {
      sessionStorage.removeItem(adminTokenKey);
      renderLogin('Your admin token is invalid or has changed.');
      return;
    }
    app.innerHTML = `<div class="panel error"><strong>Failed</strong><pre>${esc(err.message || err)}</pre></div>`;
  }
  refreshStatus();
}

function stat(label, value, hint) {
  const display = typeof value === 'string' ? value : num(value);
  return `<div class="panel stat"><div class="label">${esc(label)}</div><div class="value">${display}</div>${hint ? `<div class="hint">${esc(hint)}</div>` : ''}</div>`;
}

function dialColor(n) {
  if (n >= 90) return 'var(--bad)';
  if (n >= 70) return 'var(--warn)';
  return 'var(--copper)';
}

function dial(label, windowOrPct, resetAt, note) {
  const n = typeof windowOrPct === 'number' ? windowOrPct : pct(windowOrPct && windowOrPct.utilization);
  const reset = resetAt || (windowOrPct && windowOrPct.resets_at);
  const used = n == null ? 0 : Math.max(0, Math.min(100, n));
  const r = 52;
  const c = 2 * Math.PI * r;
  const dash = (used / 100) * c;
  const value = n == null ? '—' : `${Math.round(n)}%`;
  return `<div class="dial">
    <svg viewBox="0 0 132 132" aria-hidden="true">
      <circle cx="66" cy="66" r="${r}" fill="none" stroke="#2c261d" stroke-width="10"/>
      <circle cx="66" cy="66" r="${r}" fill="none" stroke="${dialColor(used)}" stroke-width="10"
        stroke-linecap="round" stroke-dasharray="${dash} ${c}" transform="rotate(-90 66 66)"/>
      <text x="66" y="72" text-anchor="middle" fill="currentColor" font-family="IBM Plex Mono, monospace" font-size="22">${esc(value)}</text>
    </svg>
    <div class="dial-label">${esc(label)}</div>
    <div class="dial-reset">${esc(reset ? 'resets ' + whenLocal(reset) : (note || 'no reset yet'))}</div>
  </div>`;
}

function planDials(sub) {
  if (!sub || !sub.available) {
    return `<div class="panel"><h2>Plan limits</h2><p class="empty">${esc(sub && sub.error ? sub.error : 'Sign in with local Claude OAuth to read weekly limits.')}</p></div>`;
  }
  const scoped = (sub.limits || []).filter(l => l.kind === 'weekly_scoped');
  const title = [sub.subscription, sub.rate_limit_tier].filter(Boolean).join(' · ') || 'Claude plan';
  return `<div class="panel">
    <div class="split"><h2>Plan limits</h2><span class="chip ${sub.stale ? 'warn' : 'good'}">${esc(title)}${sub.stale ? ' · cached' : ''}</span></div>
    <div class="dials">
      ${dial('5-hour session', sub.session)}
      ${dial('Weekly all models', sub.weekly)}
      ${scoped[0] ? dial('Weekly ' + ((scoped[0].scope && scoped[0].scope.model && scoped[0].scope.model.display_name) || 'scoped'), scoped[0].percent, scoped[0].resets_at, scoped[0].is_active ? 'active now' : '') : dial('Weekly extra', null, '', 'no scoped cap')}
    </div>
    ${(sub.weekly_sonnet || sub.weekly_opus) ? `<div class="dials" style="margin-top:16px">
      ${sub.weekly_sonnet ? dial('Weekly Sonnet', sub.weekly_sonnet) : ''}
      ${sub.weekly_opus ? dial('Weekly Opus', sub.weekly_opus) : ''}
    </div>` : ''}
  </div>`;
}

function spark(rows) {
  if (!rows.length) return '<p class="empty">No spend in this window.</p>';
  const max = Math.max(...rows.map(r => Number(r.cost_usd || 0)), 0.0001);
  return `<div class="spark" aria-hidden="true">${rows.map(r => {
    const h = Math.max(4, Math.round((Number(r.cost_usd || 0) / max) * 72));
    return `<i style="height:${h}px" title="${esc(r.bucket)} ${money(r.cost_usd)}"></i>`;
  }).join('')}</div>`;
}

async function renderUsage() {
  setPageMeta('Usage', 'How full is the week', 'Plan limits plus equivalent API spend');
  const [usage, sub] = await Promise.all([
    API.get('/api/usage?range=' + encodeURIComponent(usageRange)),
    API.get('/api/subscription/usage').catch(() => ({}))
  ]);
  const totals = usage.totals || {};
  const tokens = totals.tokens || {};
  const pricing = usage.pricing || {};
  const fetched = pricing.fetched_at ? new Date(pricing.fetched_at).toLocaleString() : 'never';
  app.innerHTML = `
    ${planDials(sub)}
    <div class="split">
      <div class="seg">${rangeButtons(usageRange)}</div>
      <div class="actions">
        <span class="hint">prices ${pricing.stale ? 'stale' : 'live'} · ${esc(fetched)}</span>
        <button type="button" onclick="refreshPrices()">Refresh prices</button>
      </div>
    </div>
    <div class="grid cols-4">
      ${stat('Equivalent cost', money(totals.cost_usd), 'API list price, not a subscription bill')}
      ${stat('Requests', totals.requests || 0)}
      ${stat('Input tokens', tokens.input || 0)}
      ${stat('Output tokens', tokens.output || 0)}
    </div>
    <div class="grid cols-2">
      <div class="panel"><h2>${usage.granularity === 'hour' ? 'Spend by hour' : 'Spend by day'}</h2>${spark(usage.series || [])}${usageSeriesTable(usage.series || [])}</div>
      <div class="panel"><h2>By route</h2>${usageRoutesTable(usage.by_route || [])}</div>
    </div>
    <div class="panel"><h2>By model</h2>${usageModelsTable(usage.by_model || [])}</div>
    ${totals.unpriced_models && totals.unpriced_models.length ? `<div class="notice">Unpriced models: ${totals.unpriced_models.map(esc).join(', ')}</div>` : ''}`;
}

function usageRoutesTable(rows) {
  if (!rows.length) return '<p class="empty">No route stats yet.</p>';
  return `<table><thead><tr><th>Route</th><th>Requests</th><th>Tokens</th><th>Cost</th></tr></thead><tbody>${rows.map(r => `<tr>
    <td>${esc(r.route)}${r.equivalent ? ' <span class="pill warn">equivalent</span>' : ''}</td>
    <td>${num(r.requests)}</td>
    <td>${num((r.tokens || {}).total)}</td>
    <td>${money(r.cost_usd)}</td>
  </tr>`).join('')}</tbody></table>`;
}
function usageSeriesTable(rows) {
  if (!rows.length) return '';
  return `<table><thead><tr><th>When</th><th>Requests</th><th>Tokens</th><th>Cost</th></tr></thead><tbody>${rows.map(r => `<tr>
    <td>${esc(r.bucket)}</td>
    <td>${num(r.requests)}</td>
    <td>${num(r.tokens)}</td>
    <td>${money(r.cost_usd)}</td>
  </tr>`).join('')}</tbody></table>`;
}
function usageModelsTable(rows) {
  if (!rows.length) return '<p class="empty">No model stats yet.</p>';
  return `<table><thead><tr><th>Model</th><th>Requests</th><th>Input</th><th>Output</th><th>Cache</th><th>Cost</th></tr></thead><tbody>${rows.map(m => {
    const t = m.tokens || {};
    return `<tr>
      <td><code>${esc(m.model)}</code> ${m.priced === 'unpriced' ? '<span class="pill warn">unpriced</span>' : (m.priced === 'estimated' ? '<span class="pill">est.</span>' : '')}</td>
      <td>${num(m.requests)}</td>
      <td>${num(t.input)}</td>
      <td>${num(t.output)}</td>
      <td>${num((t.cache_read || 0) + (t.cache_create || 0))}</td>
      <td>${m.priced === 'unpriced' ? '—' : money(m.cost_usd)}</td>
    </tr>`;
  }).join('')}</tbody></table>`;
}

async function renderRequests() {
  setPageMeta('Requests', 'What just went through', 'Recent proxied Claude calls');
  const logs = await API.get('/api/logs?limit=100&range=' + encodeURIComponent(usageRange));
  app.innerHTML = `<div class="panel">
    <div class="split"><h2>Request log</h2><div class="actions"><div class="seg">${rangeButtons(usageRange)}</div><button type="button" onclick="render()">Refresh</button></div></div>
    ${logsTable(logs || [])}
  </div>`;
}

function formatLatency(v) {
  if (v == null || v === '') return '';
  const n = Number(v);
  if (!Number.isFinite(n)) return String(v);
  if (n >= 1e6) return Math.round(n / 1e6) + 'ms';
  return n + 'ms';
}

function logsTable(logs) {
  if (!logs.length) return '<p class="empty">No requests in this window. Send one through the proxy to populate this list.</p>';
  return `<table><thead><tr><th>Time</th><th>Model</th><th>Route</th><th>Status</th><th>Tokens</th><th>Cost</th><th>Latency</th><th>Error</th></tr></thead><tbody>` + logs.map(l => {
    const tokens = l.tokens || {};
    const total = (tokens.input_tokens || 0) + (tokens.output_tokens || 0) + (tokens.cache_read_tokens || 0) + (tokens.cache_create_tokens || 0);
    const cost = l.priced === 'unpriced' ? '<span class="empty">unpriced</span>' : money(l.cost_usd);
    return `<tr>
      <td>${esc(new Date(l.timestamp).toLocaleString())}</td>
      <td><code>${esc(l.model)}</code></td>
      <td>${esc(l.route)}</td>
      <td>${Number(l.status) >= 400 ? '<span class="pill bad">' + esc(l.status) + '</span>' : '<span class="pill good">' + esc(l.status || '-') + '</span>'}</td>
      <td>${num(total)}</td>
      <td>${cost}</td>
      <td>${esc(formatLatency(l.latency))}</td>
      <td class="error">${esc(l.error || '')}</td>
    </tr>`;
  }).join('') + '</tbody></table>';
}

async function renderRouting() {
  setPageMeta('Routing', 'Where each model goes', 'Claude Code login or API key, plus redirects');
  const [models, redirects, cfg] = await Promise.all([API.get('/api/models'), API.get('/api/redirects'), API.get('/api/config')]);
  app.innerHTML = `
    <div class="stack">
      <div class="panel">
        <div class="split"><h2>Default route</h2></div>
        <p class="hint">Unknown models follow this source. Check the box to rewrite every configured model too.</p>
        <div class="form-row" style="margin-top:12px">
          <div class="field"><label>Auth source</label><select id="claude-source"><option value="keychain" ${(cfg.claude?.source || 'keychain') !== 'apikey' ? 'selected' : ''}>Local / Claude Code</option><option value="apikey" ${(cfg.claude?.source || 'keychain') === 'apikey' ? 'selected' : ''}>API key</option></select></div>
          <label class="field"><span>Apply to existing models</span><input id="source-apply-models" type="checkbox" checked></label>
          <button class="primary" type="button" onclick="setClaudeSource()">Save route</button>
        </div>
        <div id="source-result" class="hint" style="margin-top:10px"></div>
      </div>
      <div class="panel">
        <div class="split">
          <h2>Models</h2>
          <div class="actions">
            <button type="button" onclick="discoverModels('local')">Discover local</button>
            <button type="button" onclick="discoverModels('apikey')">Discover API key</button>
            <button class="primary" type="button" onclick="discoverModels()">Auto discover</button>
          </div>
        </div>
        <div id="discover-result"></div>
        <div class="form-row four" style="margin:14px 0">
          <div class="field"><label>Model ID</label><input id="add-model-id" placeholder="claude-new-model"></div>
          <div class="field"><label>Display name</label><input id="add-model-name" placeholder="Claude New Model"></div>
          <div class="field"><label>Route</label><select id="add-model-route"><option value="local">local</option><option value="apikey">apikey</option></select></div>
          <button class="primary" type="button" onclick="addModel()">Add</button>
        </div>
        ${modelsConfigTable(models || [])}
      </div>
      <div class="panel">
        <h2>Redirects</h2>
        ${redirectsTable(redirects || {})}
        <div class="form-row" style="margin-top:12px">
          <div class="field"><label>From</label><input id="redir-from" placeholder="claude-old-id"></div>
          <div class="field"><label>To (empty deletes)</label><input id="redir-to" placeholder="claude-new-id"></div>
          <button class="primary" type="button" onclick="setRedirect()">Save</button>
        </div>
      </div>
    </div>`;
}

function modelsConfigTable(models) {
  if (!models.length) return '<p class="empty">No models configured. Discover from Anthropic or add one by hand.</p>';
  return `<table><thead><tr><th>Model</th><th>Display</th><th>Route</th><th>Last seen</th><th></th></tr></thead><tbody>${models.map(m => `<tr>
    <td><code>${esc(m.name)}</code>${m.discovered ? ' <span class="pill good">seen</span>' : ''}</td>
    <td>${esc(m.display_name || '')}</td>
    <td><select aria-label="Route for ${esc(m.name)}" onchange="setModelRoute('${esc(m.name)}', this.value)"><option value="local" ${m.route !== 'apikey' ? 'selected' : ''}>local</option><option value="apikey" ${m.route === 'apikey' ? 'selected' : ''}>apikey</option></select></td>
    <td>${m.last_seen ? esc(new Date(m.last_seen).toLocaleString()) : '<span class="empty">manual</span>'}</td>
    <td><button class="danger" type="button" onclick="removeModel('${esc(m.name)}')">Remove</button></td>
  </tr>`).join('')}</tbody></table>`;
}

async function renderAccess() {
  setPageMeta('Access', 'Who can talk to Claude', 'Claude Code token and API keys');
  const [cfg, keys] = await Promise.all([API.get('/api/config'), API.get('/api/keys')]);
  app.innerHTML = `
    <div class="stack">
      <div class="panel">
        <h2>Runtime</h2>
        <p>Proxy <code>${esc(cfg.listen)}</code> · Dashboard <code>${esc(cfg.admin_listen)}</code></p>
        <p class="hint">Data lives in <code>${esc(cfg.data_dir)}</code>. Use <code>CLAUDE_PROXY_LOG=debug</code> for verbose logs.</p>
      </div>
      <div class="panel">
        <div class="split"><h2>Claude Code token</h2><button type="button" onclick="refreshToken()">Refresh token</button></div>
        <div id="token-result" class="hint"></div>
      </div>
      <div class="panel">
        <h2>API keys</h2>
        ${apiKeysTable(keys || [])}
        <div class="form-row four" style="margin-top:16px">
          <div class="field"><label>Label</label><input id="key-label" placeholder="Production"></div>
          <div class="field"><label>API key</label><input id="key-value" type="password" placeholder="sk-ant-..."></div>
          <div class="field"><label>Base URL</label><input id="key-base" placeholder="https://api.anthropic.com"></div>
          <button class="primary" type="button" onclick="addAPIKey()">Add</button>
        </div>
        <div class="actions" style="margin-top:10px"><button type="button" onclick="testAPIKeyInput()">Test unsaved key</button><span id="key-test-result" class="hint"></span></div>
      </div>
    </div>`;
}

function apiKeysTable(keys) {
  if (!keys.length) return '<p class="empty">No API keys yet. Add one to route models off the subscription.</p>';
  return `<table><thead><tr><th>Label</th><th>Key</th><th>Base URL</th><th></th></tr></thead><tbody>${keys.map(k => `<tr><td>${esc(k.label || '')}</td><td><code>${esc(k.api_key)}</code></td><td>${esc(k.base_url || 'default')}</td><td>${k.id === '_legacy' ? '<span class="empty">legacy</span>' : `<button class="danger" type="button" onclick="removeAPIKey('${esc(k.id)}')">Remove</button>`}</td></tr>`).join('')}</tbody></table>`;
}
function redirectsTable(redirs) {
  const rows = Object.entries(redirs);
  if (!rows.length) return '<p class="empty">No redirects.</p>';
  return `<table><thead><tr><th>From</th><th>To</th></tr></thead><tbody>${rows.map(([from,to]) => `<tr><td><code>${esc(from)}</code></td><td><code>${esc(to)}</code></td></tr>`).join('')}</tbody></table>`;
}

async function switchPage(page) { currentPage = page; await render(); }
async function setUsageRange(range) { usageRange = range; await render(); }
async function refreshPrices() {
  try {
    await API.post('/api/prices/refresh', {});
  } catch (err) {
    const el = document.querySelector('.actions .hint');
    if (el) { el.textContent = err.message || 'Price refresh failed'; el.className = 'error'; return; }
    throw err;
  }
  await render();
}
async function discoverModels(route) {
  const el = document.getElementById('discover-result');
  if (el) el.innerHTML = '<p class="hint">Discovering…</p>';
  const res = await API.post('/api/models/discover', route ? { route } : {});
  if (el) el.innerHTML = `<div class="notice">Seen ${num(res.seen)}, added ${num(res.added)}, updated ${num(res.updated)} via ${esc(res.route)}/${esc(res.source)}.</div>`;
  currentPage = 'routing';
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
  if (out) out.textContent = 'Saving…';
  const res = await API.post('/api/config/source', { source, apply_to_models: applyToModels });
  if (out) out.textContent = `Saved ${res.source === 'apikey' ? 'API key' : 'Claude Code login'}${res.apply_to_models ? ' and updated model routes' : ''}.`;
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
  out.textContent = 'Testing…';
  const res = await API.post('/api/keys/test', { api_key: val('key-value'), base_url: val('key-base') });
  out.textContent = `${res.success ? 'OK' : 'Failed'}: ${res.message} (${res.latency_ms || 0}ms)`;
  out.className = res.success ? 'chip good' : 'chip bad';
}
async function refreshToken() {
  const out = document.getElementById('token-result');
  out.textContent = 'Refreshing…';
  const res = await API.post('/api/token/refresh', {});
  out.textContent = res.status === 'ok' ? 'Token refreshed.' : (res.message || 'Refresh failed');
}
async function setRedirect() {
  await API.post('/api/redirects/set', { from: val('redir-from'), to: val('redir-to') });
  render();
}

document.querySelectorAll('.nav').forEach(btn => btn.addEventListener('click', () => switchPage(btn.dataset.page)));
document.getElementById('refresh-btn').addEventListener('click', render);
logoutBtn.addEventListener('click', logoutAdmin);
window.switchPage = switchPage;
window.setUsageRange = setUsageRange;
window.refreshPrices = refreshPrices;
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
window.loginAdmin = loginAdmin;

render();
setInterval(refreshStatus, 10000);
