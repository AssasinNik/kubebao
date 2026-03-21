(() => {
'use strict';

let TOKEN = sessionStorage.getItem('kubebao_token') || '';
const API = '';

// ===== Auth =====
const $ = id => document.getElementById(id);

async function checkStatus() {
  try {
    const r = await fetch(API + '/api/status');
    const s = await r.json();
    const el = $('login-bao-status');
    if (s.openbaoHealth === 'healthy') {
      el.textContent = 'OpenBao: Connected';
      el.className = 'login-status ok';
    } else {
      el.textContent = 'OpenBao: ' + (s.openbaoHealth || 'unknown');
      el.className = 'login-status err';
    }
  } catch { /* ignore */ }
}

function showLogin() {
  $('login-screen').style.display = 'flex';
  $('app').style.display = 'none';
  checkStatus();
}

function showApp() {
  $('login-screen').style.display = 'none';
  $('app').style.display = 'flex';
  loadPage('dashboard');
  loadNamespaces();
}

$('login-submit').addEventListener('click', async () => {
  const t = $('login-token').value.trim();
  if (!t) { $('login-error').textContent = 'Token is required'; return; }
  $('login-error').textContent = '';
  try {
    const r = await fetch(API + '/api/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ token: t })
    });
    const d = await r.json();
    if (d.success) {
      TOKEN = t;
      sessionStorage.setItem('kubebao_token', t);
      showApp();
    } else {
      $('login-error').textContent = d.error || 'Authentication failed';
    }
  } catch (e) {
    $('login-error').textContent = 'Connection error';
  }
});

$('login-token').addEventListener('keydown', e => { if (e.key === 'Enter') $('login-submit').click(); });

$('login-toggle-vis').addEventListener('click', () => {
  const inp = $('login-token');
  inp.type = inp.type === 'password' ? 'text' : 'password';
});

$('btn-logout').addEventListener('click', () => {
  TOKEN = '';
  sessionStorage.removeItem('kubebao_token');
  showLogin();
});

// Auto-login if token stored
if (TOKEN) {
  (async () => {
    try {
      const r = await fetch(API + '/api/auth/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ token: TOKEN })
      });
      const d = await r.json();
      if (d.success) { showApp(); return; }
    } catch { /* ignore */ }
    showLogin();
  })();
} else {
  showLogin();
}

// ===== API helpers =====
async function api(path) {
  const r = await fetch(API + path, { headers: { 'X-Token': TOKEN } });
  if (r.status === 401) { showLogin(); throw new Error('unauthorized'); }
  if (!r.ok) throw new Error('HTTP ' + r.status);
  return r.json();
}

async function apiPost(path, body) {
  const r = await fetch(API + path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Token': TOKEN },
    body: JSON.stringify(body)
  });
  if (r.status === 401) { showLogin(); throw new Error('unauthorized'); }
  return r.json();
}

// ===== Navigation =====
document.querySelectorAll('.nav-item').forEach(item => {
  item.addEventListener('click', () => {
    document.querySelectorAll('.nav-item').forEach(n => n.classList.remove('active'));
    document.querySelectorAll('.page').forEach(p => p.classList.remove('active'));
    item.classList.add('active');
    const page = document.getElementById('page-' + item.dataset.page);
    if (page) { page.classList.add('active'); loadPage(item.dataset.page); }
  });
});

// ===== Page loaders =====
async function loadPage(name) {
  const m = {
    dashboard: loadDashboard, keys: loadKeys, secrets: loadSecrets,
    csi: loadCSI, metrics: loadMetrics, openbao: loadOpenBao
  };
  if (m[name]) await m[name]();
}

// ===== Dashboard =====
async function loadDashboard() {
  try {
    const [status, metrics] = await Promise.all([api('/api/status'), api('/api/metrics')]);
    $('d-provider').textContent = status.kmsProvider || '--';
    $('d-uptime').textContent = status.uptime || '--';
    $('d-keyname').textContent = status.keyName || '--';
    $('d-bao').textContent = status.openbaoHealth || '--';
    $('d-goversion').textContent = status.goVersion || '--';
    $('d-enc').textContent = metrics.encryptOps;
    $('d-dec').textContent = metrics.decryptOps;
    $('d-secrets').textContent = metrics.totalSecrets || 0;

    const baoInd = $('baoStatus');
    baoInd.className = 'status-indicator ' + (status.openbaoHealth === 'healthy' ? 'healthy' : 'unhealthy');
    const k8sInd = $('k8sStatus');
    k8sInd.className = 'status-indicator ' + (status.k8sConnected ? 'healthy' : 'unhealthy');

    const badge = $('d-status-badge');
    if (status.openbaoHealth === 'healthy') {
      badge.textContent = 'Operational';
      badge.style.background = 'var(--success-dim)'; badge.style.color = 'var(--success)';
    } else {
      badge.textContent = status.openbaoHealth;
      badge.style.background = 'var(--warning-dim)'; badge.style.color = 'var(--warning)';
    }

    drawHeapChart(metrics.history || []);
  } catch (e) { console.error('Dashboard error:', e); }
}

// ===== Keys =====
async function loadKeys() {
  try {
    const k = await api('/api/keys');
    $('k-name').textContent = k.keyName;
    $('k-path').textContent = k.keyPath;
    $('k-version').textContent = 'v' + k.version;
    $('k-algo').textContent = k.algorithm;
    $('k-block').textContent = k.blockSize + ' bit';
    $('k-size').textContent = k.keySize + ' bit';
    $('k-created').textContent = fmtTime(k.createdAt);
    $('k-rotated').textContent = k.lastRotated ? fmtTime(k.lastRotated) : 'Never';
    $('k-rotations').textContent = k.totalRotations || 0;
    $('k-mode').textContent = k.mode || '--';
    $('k-standard').textContent = k.standard || '--';
  } catch (e) { console.error('Keys error:', e); }
}

$('btn-rotate').addEventListener('click', async () => {
  if (!confirm('Rotate the encryption key? This generates a new 256-bit key.')) return;
  const msg = $('rotate-msg');
  msg.style.display = 'block';
  msg.className = 'alert warn';
  msg.textContent = 'Rotating key...';
  try {
    const r = await apiPost('/api/keys/rotate', {});
    if (r.success) {
      msg.className = 'alert ok'; msg.textContent = r.message;
      loadKeys();
    } else {
      msg.className = 'alert err'; msg.textContent = r.message;
    }
  } catch (e) { msg.className = 'alert err'; msg.textContent = e.message; }
});

$('btn-show-key').addEventListener('click', async () => {
  const panel = $('key-value-panel');
  if (panel.style.display !== 'none') { panel.style.display = 'none'; return; }
  try {
    const k = await api('/api/keys/current');
    $('key-value-display').textContent = k.key || '(no key found)';
    panel.style.display = 'block';
  } catch (e) {
    $('key-value-display').textContent = 'Error: ' + e.message;
    panel.style.display = 'block';
  }
});

// ===== Secrets =====
let allSecrets = [];

async function loadSecrets() {
  try {
    const ns = $('ns-filter').value;
    allSecrets = await api('/api/secrets' + (ns ? '?namespace=' + ns : ''));
    renderSecrets(allSecrets);
  } catch (e) { console.error('Secrets error:', e); }
}

function renderSecrets(list) {
  const tbody = document.querySelector('#secrets-table tbody');
  if (!list || !list.length) {
    tbody.innerHTML = '<tr><td colspan="6" class="empty-state"><p>No secrets found</p></td></tr>';
    return;
  }
  tbody.innerHTML = list.map(s => `
    <tr data-ns="${esc(s.namespace)}" data-name="${esc(s.name)}">
      <td class="name-cell">${esc(s.name)}</td>
      <td>${esc(s.namespace)}</td>
      <td><span class="type-badge">${esc(s.type)}</span></td>
      <td>${(s.dataKeys||[]).map(k=>'<span class="data-key">'+esc(k)+'</span>').join(' ')}</td>
      <td class="mono">${formatBytes(s.size)}</td>
      <td>${timeAgo(s.createdAt)}</td>
    </tr>`).join('');

  tbody.querySelectorAll('tr').forEach(tr => {
    tr.addEventListener('click', () => openSecretModal(tr.dataset.ns, tr.dataset.name));
  });
}

$('secrets-filter').addEventListener('input', function() {
  const q = this.value.toLowerCase();
  renderSecrets(allSecrets.filter(s => s.name.toLowerCase().includes(q) || s.namespace.toLowerCase().includes(q)));
});

$('ns-filter').addEventListener('change', loadSecrets);

async function loadNamespaces() {
  try {
    const ns = await api('/api/namespaces');
    const sel = $('ns-filter');
    sel.innerHTML = '<option value="">All Namespaces</option>';
    (ns || []).forEach(n => { const o = document.createElement('option'); o.value = n; o.textContent = n; sel.appendChild(o); });
  } catch { /* ignore */ }
}

// Secret Modal
async function openSecretModal(ns, name) {
  $('modal-secret-title').textContent = name;
  $('modal-secret-body').innerHTML = '<p class="text-muted">Loading...</p>';
  $('secret-modal').style.display = 'flex';
  try {
    const s = await api('/api/secrets/' + ns + '/' + name);
    let html = '<table class="modal-kv">';
    html += `<tr><td>Namespace</td><td>${esc(s.namespace)}</td></tr>`;
    html += `<tr><td>Type</td><td>${esc(s.type)}</td></tr>`;
    html += `<tr><td>UID</td><td class="mono" style="font-size:11px">${esc(s.uid)}</td></tr>`;
    html += `<tr><td>Version</td><td>${esc(s.resourceVersion)}</td></tr>`;
    html += `<tr><td>Created</td><td>${fmtTime(s.createdAt)}</td></tr>`;

    if (s.labels && Object.keys(s.labels).length) {
      html += '<tr><td>Labels</td><td>' + Object.entries(s.labels).map(([k,v]) => '<span class="data-key">' + esc(k) + '=' + esc(v) + '</span>').join(' ') + '</td></tr>';
    }
    if (s.annotations && Object.keys(s.annotations).length) {
      html += '<tr><td>Annotations</td><td>' + Object.entries(s.annotations).map(([k,v]) => '<span class="data-key">' + esc(k) + '</span>').join(' ') + '</td></tr>';
    }
    html += '</table>';

    html += '<h3 style="font-size:13px;margin:12px 0 8px;color:var(--text-0)">Data (' + Object.keys(s.data || {}).length + ' keys)</h3>';
    for (const [k, v] of Object.entries(s.data || {})) {
      const shortened = v.length > 80 ? v.substring(0, 80) + '...' : v;
      html += '<div class="modal-data-item"><span class="data-key-name">' + esc(k) + '</span><br>' + esc(shortened) + '</div>';
    }
    $('modal-secret-body').innerHTML = html;
  } catch (e) {
    $('modal-secret-body').innerHTML = '<p style="color:var(--danger)">Error: ' + esc(e.message) + '</p>';
  }
}

$('modal-close').addEventListener('click', () => { $('secret-modal').style.display = 'none'; });
$('secret-modal').addEventListener('click', e => { if (e.target === $('secret-modal')) $('secret-modal').style.display = 'none'; });

// ===== OpenBao =====
async function loadOpenBao() {
  try {
    const info = await api('/api/openbao');

    let cardsHtml = '';
    cardsHtml += infoCard('Address', info.address || '--', '');
    cardsHtml += infoCard('Version', info.version || '--', 'blue');
    const sealed = info.sealed;
    cardsHtml += infoCard('Sealed', sealed === false ? 'No' : sealed === true ? 'Yes' : '--', sealed === false ? 'green' : 'red');
    $('bao-cards').innerHTML = cardsHtml;

    // Mounts
    const mounts = info.mounts || [];
    if (mounts.length) {
      let t = '<table class="kv-table">';
      mounts.forEach(m => { t += '<tr><td class="mono">' + esc(m.path) + '</td><td>' + esc(m.type) + '</td></tr>'; });
      t += '</table>';
      $('bao-mounts').innerHTML = t;
    } else { $('bao-mounts').textContent = 'No mounts available'; }

    // Auth
    const auth = info.authMethods || [];
    if (auth.length) {
      let t = '<table class="kv-table">';
      auth.forEach(a => { t += '<tr><td class="mono">' + esc(a.path) + '</td><td>' + esc(a.type) + '</td></tr>'; });
      t += '</table>';
      $('bao-auth').innerHTML = t;
    } else { $('bao-auth').textContent = 'No auth methods available'; }

    // Seal status
    const ss = info.sealStatus;
    if (ss) {
      let t = '<table class="kv-table">';
      t += '<tr><td>Type</td><td>' + esc(ss.type) + '</td></tr>';
      t += '<tr><td>Sealed</td><td>' + (ss.sealed ? 'Yes' : 'No') + '</td></tr>';
      t += '<tr><td>Threshold</td><td>' + (ss.t || '--') + '</td></tr>';
      t += '<tr><td>Shares</td><td>' + (ss.n || '--') + '</td></tr>';
      t += '<tr><td>Progress</td><td>' + (ss.progress || 0) + '/' + (ss.t || '--') + '</td></tr>';
      t += '</table>';
      $('bao-seal').innerHTML = t;
    } else {
      $('bao-seal').textContent = 'Unavailable (token may lack permissions)';
    }
  } catch (e) { console.error('OpenBao error:', e); }
}

function infoCard(label, val, dotColor) {
  return '<div class="info-card"><div class="info-card-head"><div class="ic-dot ' + dotColor + '"></div>' + esc(label) + '</div><div class="info-card-val">' + esc(val) + '</div></div>';
}

// ===== CSI =====
async function loadCSI() {
  try {
    const pods = await api('/api/csi/pods');
    const tbody = document.querySelector('#csi-table tbody');
    if (!pods || !pods.length) {
      tbody.innerHTML = '<tr><td colspan="8" class="empty-state"><p>No CSI pods found</p></td></tr>';
      return;
    }
    tbody.innerHTML = pods.map(p => `
      <tr>
        <td class="name-cell">${esc(p.name)}</td>
        <td>${esc(p.namespace)}</td>
        <td>${esc(p.node)}</td>
        <td class="${p.status==='Running'?'status-running':'status-other'}">${esc(p.status)}</td>
        <td>${esc(p.ready)}</td>
        <td><span class="data-key">${esc(p.providerClass)}</span></td>
        <td class="mono">${esc(p.mountPath)}</td>
        <td>${esc(p.age)}</td>
      </tr>`).join('');
  } catch (e) { console.error('CSI error:', e); }
}

// ===== Metrics =====
async function loadMetrics() {
  try {
    const m = await api('/api/metrics');

    const cards = [
      ['Encrypt Ops', m.encryptOps, 'blue'],
      ['Decrypt Ops', m.decryptOps, 'green'],
      ['Key Rotations', m.keyRotations, 'yellow'],
      ['Secrets', m.totalSecrets, ''],
      ['Pods', m.totalPods, ''],
      ['Namespaces', m.namespaces, ''],
      ['Goroutines', m.goroutineCount, 'blue'],
      ['CPU Cores', m.cpuCount, ''],
    ];
    $('metrics-grid').innerHTML = cards.map(([l,v,c]) =>
      '<div class="metric-card"><span class="mc-label">' + l + '</span><span class="mc-val mono">' + v + '</span></div>'
    ).join('');

    let memRows = '';
    memRows += '<tr><td>Heap</td><td>' + m.heapAllocMB.toFixed(2) + ' MB</td></tr>';
    memRows += '<tr><td>Stack</td><td>' + (m.stackAllocMB||0).toFixed(2) + ' MB</td></tr>';
    memRows += '<tr><td>Sys Total</td><td>' + (m.sysMB||0).toFixed(2) + ' MB</td></tr>';
    memRows += '<tr><td>Total Allocs</td><td>' + (m.totalAllocs||0) + ' MB</td></tr>';
    memRows += '<tr><td>Live Objects</td><td>' + (m.liveObjects||0).toLocaleString() + '</td></tr>';
    $('mem-table').innerHTML = memRows;

    let rtRows = '';
    rtRows += '<tr><td>Goroutines</td><td>' + m.goroutineCount + '</td></tr>';
    rtRows += '<tr><td>GC Runs</td><td>' + (m.numGC||0) + '</td></tr>';
    rtRows += '<tr><td>GC Pause</td><td>' + (m.gcPauseMs||0).toFixed(2) + ' ms</td></tr>';
    rtRows += '<tr><td>Avg Encrypt</td><td>' + m.avgEncryptMs.toFixed(2) + ' ms</td></tr>';
    rtRows += '<tr><td>Avg Decrypt</td><td>' + m.avgDecryptMs.toFixed(2) + ' ms</td></tr>';
    rtRows += '<tr><td>Uptime</td><td>' + (m.uptimeSeconds||0) + 's</td></tr>';
    $('rt-table').innerHTML = rtRows;

    drawMetricsChart(m.history || []);
  } catch (e) { console.error('Metrics error:', e); }
}

// ===== Decrypt =====
$('btn-decrypt').addEventListener('click', async () => {
  const res = $('dec-result');
  const key = $('dec-key').value.trim();
  const ct = $('dec-ct').value.trim();
  if (!key || !ct) { res.className = 'decrypt-result err'; res.textContent = 'Both fields required.'; return; }
  try {
    const r = await apiPost('/api/secrets/decrypt', { keyBase64: key, ciphertext: ct });
    if (r.success) { res.className = 'decrypt-result ok'; res.textContent = r.plaintext; }
    else { res.className = 'decrypt-result err'; res.textContent = r.error; }
  } catch (e) { res.className = 'decrypt-result err'; res.textContent = e.message; }
});

$('toggle-key-vis').addEventListener('click', () => {
  const inp = $('dec-key');
  inp.type = inp.type === 'password' ? 'text' : 'password';
});

// ===== Charts (Canvas) =====
function drawHeapChart(history) {
  const canvas = $('chart-heap');
  if (!canvas) return;
  const ctx = canvas.getContext('2d');
  const W = canvas.parentElement.clientWidth - 32;
  const H = 140;
  canvas.width = W * 2; canvas.height = H * 2;
  canvas.style.width = W + 'px'; canvas.style.height = H + 'px';
  ctx.scale(2, 2);
  ctx.clearRect(0, 0, W, H);

  if (!history.length) {
    ctx.fillStyle = '#71717a'; ctx.font = '12px sans-serif';
    ctx.fillText('Collecting data...', W/2 - 50, H/2);
    return;
  }

  const heaps = history.map(p => p.heap);
  const maxH = Math.max(...heaps, 1) * 1.2;

  ctx.strokeStyle = '#27272a'; ctx.lineWidth = 0.5;
  for (let i = 0; i < 4; i++) {
    const y = H - (H * (i / 3));
    ctx.beginPath(); ctx.moveTo(40, y); ctx.lineTo(W, y); ctx.stroke();
    ctx.fillStyle = '#71717a'; ctx.font = '9px sans-serif';
    ctx.fillText((maxH * i / 3).toFixed(1) + ' MB', 0, y + 3);
  }

  const step = (W - 40) / Math.max(history.length - 1, 1);

  // Heap fill
  ctx.beginPath();
  ctx.moveTo(40, H);
  history.forEach((p, i) => ctx.lineTo(40 + i * step, H - (p.heap / maxH) * H));
  ctx.lineTo(40 + (history.length - 1) * step, H);
  ctx.closePath();
  ctx.fillStyle = 'rgba(59,130,246,0.08)';
  ctx.fill();

  // Heap line
  ctx.beginPath();
  history.forEach((p, i) => {
    const x = 40 + i * step, y = H - (p.heap / maxH) * H;
    i === 0 ? ctx.moveTo(x, y) : ctx.lineTo(x, y);
  });
  ctx.strokeStyle = '#3b82f6'; ctx.lineWidth = 1.5; ctx.stroke();
}

function drawMetricsChart(history) {
  const canvas = $('chart-metrics');
  if (!canvas) return;
  const ctx = canvas.getContext('2d');
  const W = canvas.parentElement.clientWidth - 32;
  const H = 160;
  canvas.width = W * 2; canvas.height = H * 2;
  canvas.style.width = W + 'px'; canvas.style.height = H + 'px';
  ctx.scale(2, 2);
  ctx.clearRect(0, 0, W, H);

  if (!history.length) {
    ctx.fillStyle = '#71717a'; ctx.font = '12px sans-serif';
    ctx.fillText('Collecting data...', W/2 - 50, H/2);
    return;
  }

  const gors = history.map(p => p.gor);
  const heaps = history.map(p => p.heap);
  const maxG = Math.max(...gors, 1) * 1.3;
  const maxH = Math.max(...heaps, 1) * 1.3;
  const step = (W - 50) / Math.max(history.length - 1, 1);

  // Grid
  ctx.strokeStyle = '#27272a'; ctx.lineWidth = 0.5;
  for (let i = 0; i <= 3; i++) {
    const y = H - (H * i / 3);
    ctx.beginPath(); ctx.moveTo(50, y); ctx.lineTo(W, y); ctx.stroke();
  }

  // Goroutines
  ctx.beginPath();
  history.forEach((p, i) => {
    const x = 50 + i * step, y = H - (p.gor / maxG) * H;
    i === 0 ? ctx.moveTo(x, y) : ctx.lineTo(x, y);
  });
  ctx.strokeStyle = '#22c55e'; ctx.lineWidth = 1.5; ctx.stroke();

  // Heap
  ctx.beginPath();
  history.forEach((p, i) => {
    const x = 50 + i * step, y = H - (p.heap / maxH) * H;
    i === 0 ? ctx.moveTo(x, y) : ctx.lineTo(x, y);
  });
  ctx.strokeStyle = '#3b82f6'; ctx.lineWidth = 1.5; ctx.stroke();

  // Legend
  ctx.fillStyle = '#22c55e'; ctx.fillRect(W - 160, 6, 10, 3);
  ctx.fillStyle = '#a1a1aa'; ctx.font = '10px sans-serif'; ctx.fillText('Goroutines', W - 145, 11);
  ctx.fillStyle = '#3b82f6'; ctx.fillRect(W - 80, 6, 10, 3);
  ctx.fillStyle = '#a1a1aa'; ctx.fillText('Heap (MB)', W - 65, 11);
}

// ===== Utilities =====
function esc(s) {
  if (s == null) return '';
  const d = document.createElement('div');
  d.textContent = String(s);
  return d.innerHTML;
}

function timeAgo(iso) {
  if (!iso) return '--';
  const diff = (Date.now() - new Date(iso).getTime()) / 1000;
  if (diff < 0) return 'just now';
  if (diff < 60) return Math.floor(diff) + 's ago';
  if (diff < 3600) return Math.floor(diff / 60) + 'm ago';
  if (diff < 86400) return Math.floor(diff / 3600) + 'h ago';
  return Math.floor(diff / 86400) + 'd ago';
}

function fmtTime(s) {
  if (!s) return '--';
  try {
    const d = new Date(s);
    return d.toLocaleString();
  } catch { return s; }
}

function formatBytes(b) {
  if (!b && b !== 0) return '--';
  if (b < 1024) return b + ' B';
  if (b < 1024*1024) return (b/1024).toFixed(1) + ' KB';
  return (b/1024/1024).toFixed(1) + ' MB';
}

// ===== Auto-refresh =====
setInterval(() => {
  const active = document.querySelector('.nav-item.active');
  if (!active) return;
  const page = active.dataset.page;
  if (page === 'dashboard') loadDashboard();
  else if (page === 'metrics') loadMetrics();
}, 10000);

})();
