(() => {
'use strict';

const API = '';

// ===== Navigation =====
const navItems = document.querySelectorAll('.nav-item');
const pages = document.querySelectorAll('.page');

navItems.forEach(item => {
  item.addEventListener('click', () => {
    navItems.forEach(n => n.classList.remove('active'));
    pages.forEach(p => p.classList.remove('active'));
    item.classList.add('active');
    const page = document.getElementById('page-' + item.dataset.page);
    if (page) {
      page.classList.add('active');
      loadPage(item.dataset.page);
    }
  });
});

// ===== API helpers =====
async function api(path) {
  const r = await fetch(API + path);
  if (!r.ok) throw new Error(`HTTP ${r.status}`);
  return r.json();
}

async function apiPost(path, body) {
  const r = await fetch(API + path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  });
  return r.json();
}

function $(id) { return document.getElementById(id); }

// ===== Page loaders =====
async function loadPage(name) {
  const loaders = { dashboard: loadDashboard, keys: loadKeys, secrets: loadSecrets, csi: loadCSI, metrics: loadMetrics };
  if (loaders[name]) await loaders[name]();
}

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
    $('d-goroutines').textContent = metrics.goroutineCount;
    $('d-heap').textContent = metrics.heapAllocMB.toFixed(1);
    $('d-secrets').textContent = metrics.totalSecrets || 0;

    // Status indicators
    const baoInd = $('baoStatus');
    baoInd.className = 'status-indicator ' + (status.openbaoHealth === 'healthy' ? 'healthy' : 'unhealthy');

    const k8sInd = $('k8sStatus');
    k8sInd.className = 'status-indicator ' + (status.k8sConnected ? 'healthy' : 'unhealthy');

    const badge = $('d-status-badge');
    if (status.openbaoHealth === 'healthy') {
      badge.textContent = 'Healthy';
      badge.style.background = 'var(--success-bg)';
      badge.style.color = 'var(--success)';
    } else {
      badge.textContent = status.openbaoHealth;
      badge.style.background = 'var(--warning-bg)';
      badge.style.color = 'var(--warning)';
    }
  } catch (e) {
    console.error('Dashboard load error:', e);
  }
}

async function loadKeys() {
  try {
    const k = await api('/api/keys');
    $('k-name').textContent = k.keyName;
    $('k-path').textContent = k.keyPath;
    $('k-version').textContent = 'v' + k.version;
    $('k-algo').textContent = k.algorithm;
    $('k-block').textContent = k.blockSize + ' bit';
    $('k-size').textContent = k.keySize + ' bit';
    $('k-created').textContent = k.createdAt || 'N/A';
  } catch (e) {
    console.error('Keys load error:', e);
  }
}

$('btn-rotate').addEventListener('click', async () => {
  if (!confirm('Rotate the encryption key? Existing secrets will remain readable.')) return;
  const msg = $('rotate-msg');
  msg.className = 'rotate-msg';
  msg.style.display = 'block';
  msg.textContent = 'Rotating key...';
  msg.style.background = 'var(--warning-bg)';
  msg.style.color = 'var(--warning)';

  try {
    const r = await apiPost('/api/keys/rotate', {});
    if (r.success) {
      msg.className = 'rotate-msg ok';
      msg.textContent = r.message;
      loadKeys();
    } else {
      msg.className = 'rotate-msg err';
      msg.textContent = r.message;
    }
  } catch (e) {
    msg.className = 'rotate-msg err';
    msg.textContent = e.message;
  }
});

// Secrets
let allSecrets = [];

async function loadSecrets() {
  try {
    allSecrets = await api('/api/secrets');
    renderSecrets(allSecrets);
  } catch (e) {
    console.error('Secrets load error:', e);
  }
}

function renderSecrets(list) {
  const tbody = document.querySelector('#secrets-table tbody');
  if (!list.length) {
    tbody.innerHTML = '<tr><td colspan="6" class="empty-state"><p>No secrets found</p></td></tr>';
    return;
  }
  tbody.innerHTML = list.map(s => `
    <tr>
      <td class="name-cell">${esc(s.name)}</td>
      <td>${esc(s.namespace)}</td>
      <td><span class="type-badge">${esc(s.type)}</span></td>
      <td>${(s.dataKeys||[]).map(k => '<span class="data-key">' + esc(k) + '</span>').join(' ')}</td>
      <td><span class="cipher-preview">${esc(s.cipherPreview)}</span></td>
      <td>${timeAgo(s.createdAt)}</td>
    </tr>`).join('');
}

$('secrets-filter').addEventListener('input', function() {
  const q = this.value.toLowerCase();
  renderSecrets(allSecrets.filter(s => s.name.toLowerCase().includes(q) || s.namespace.toLowerCase().includes(q)));
});

// Decrypt
$('btn-decrypt').addEventListener('click', async () => {
  const res = $('dec-result');
  const key = $('dec-key').value.trim();
  const ct = $('dec-ct').value.trim();
  if (!key || !ct) {
    res.className = 'decrypt-result err';
    res.textContent = 'Please fill in both the key and ciphertext fields.';
    return;
  }
  try {
    const r = await apiPost('/api/secrets/decrypt', { keyBase64: key, ciphertext: ct });
    if (r.success) {
      res.className = 'decrypt-result ok';
      res.textContent = r.plaintext;
    } else {
      res.className = 'decrypt-result err';
      res.textContent = r.error;
    }
  } catch (e) {
    res.className = 'decrypt-result err';
    res.textContent = e.message;
  }
});

// Toggle key visibility
$('toggle-key-vis').addEventListener('click', () => {
  const inp = $('dec-key');
  inp.type = inp.type === 'password' ? 'text' : 'password';
});

// CSI
async function loadCSI() {
  try {
    const pods = await api('/api/csi/pods');
    const tbody = document.querySelector('#csi-table tbody');
    if (!pods || !pods.length) {
      tbody.innerHTML = '<tr><td colspan="6" class="empty-state"><p>No CSI pods found</p></td></tr>';
      return;
    }
    tbody.innerHTML = pods.map(p => `
      <tr>
        <td class="name-cell">${esc(p.name)}</td>
        <td>${esc(p.namespace)}</td>
        <td>${esc(p.node)}</td>
        <td class="${p.status === 'Running' ? 'status-running' : 'status-other'}">${esc(p.status)}</td>
        <td><span class="data-key">${esc(p.providerClass)}</span></td>
        <td class="mono">${esc(p.mountPath)}</td>
      </tr>`).join('');
  } catch (e) {
    console.error('CSI load error:', e);
  }
}

// Metrics
async function loadMetrics() {
  try {
    const m = await api('/api/metrics');
    $('m-enc').textContent = m.encryptOps;
    $('m-dec').textContent = m.decryptOps;
    $('m-enc-ms').textContent = m.avgEncryptMs.toFixed(2) + ' ms';
    $('m-dec-ms').textContent = m.avgDecryptMs.toFixed(2) + ' ms';
    $('m-rot').textContent = m.keyRotations;
    $('m-cache').textContent = m.cachedKeys;
    $('m-gor').textContent = m.goroutineCount;
    $('m-heap').textContent = m.heapAllocMB.toFixed(1) + ' MB';
  } catch (e) {
    console.error('Metrics load error:', e);
  }
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

// ===== Init =====
loadDashboard();

setInterval(() => {
  const active = document.querySelector('.nav-item.active');
  if (active) {
    const page = active.dataset.page;
    if (page === 'dashboard') loadDashboard();
    else if (page === 'metrics') loadMetrics();
  }
}, 10000);

})();
