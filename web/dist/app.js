// Operations page for the freeze-dryer steam sterilization validation backend.
// It drives plan locking, probe registration and live-state inspection through
// the same /api/v1 JSON interfaces exposed by the Go service, with no external
// dependencies.

const stateEl = document.getElementById('backend-state');
const logEl = document.getElementById('action-log');

function log(message) {
  const line = document.createElement('li');
  line.textContent = message;
  logEl.prepend(line);
}

async function api(method, path, body) {
  const opts = { method, headers: { 'Content-Type': 'application/json' } };
  if (body !== undefined) opts.body = JSON.stringify(body);
  const res = await fetch(path, opts);
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(`${data.code || res.status}: ${(data.reasons || []).join(', ')}`);
  }
  return data;
}

async function refresh() {
  try {
    const [health, version] = await Promise.all([
      fetch('/api/v1/health').then((r) => r.json()),
      fetch('/api/v1/version').then((r) => r.json()),
    ]);
    stateEl.textContent = `后端已连接 · ${health.status} · 算法 ${version.algorithm_version}`;
    stateEl.className = 'connected';
    await Promise.all([loadValidations(), loadProbes()]);
  } catch (err) {
    stateEl.textContent = `后端不可用：${err.message}`;
    stateEl.className = 'disconnected';
  }
}

function demoPlan() {
  const regions = [
    { id: 'r-chamber', name: '腔体', kind: 'chamber' },
    { id: 'r-shelf', name: '搁板', kind: 'shelf' },
    { id: 'r-condenser', name: '冷凝器', kind: 'condenser' },
    { id: 'r-drain', name: '排水口', kind: 'drain' },
  ];
  const positions = [
    { id: 'p1', region_id: 'r-chamber', load_layer: 0 },
    { id: 'p2', region_id: 'r-chamber', load_layer: 0 },
    { id: 'p3', region_id: 'r-shelf', load_layer: 1 },
    { id: 'p4', region_id: 'r-shelf', load_layer: 1 },
  ];
  return {
    id: 'v1',
    structure_digest: '',
    load_digest: '',
    regions,
    positions,
    probe_summaries: [
      { probe_id: 'probe-1', position_id: 'p1', certificate: 'cert-1' },
      { probe_id: 'probe-2', position_id: 'p2', certificate: 'cert-2' },
      { probe_id: 'probe-3', position_id: 'p3', certificate: 'cert-3' },
      { probe_id: 'probe-4', position_id: 'p4', certificate: 'cert-4' },
    ],
    exposure: {
      min_temperature: 121000,
      min_pressure: 100000,
      max_pressure: 200000,
      min_duration: 60000,
    },
    sampling_interval: 1000,
    lethality_threshold: 1000000,
  };
}

async function lockPlan() {
  try {
    const plan = demoPlan();
    const result = await api('POST', '/api/v1/validations/lock', {
      operation_id: `web-lock-${Date.now()}`,
      plan,
    });
    log(`方案锁定成功：${result.validation_id} 代次 ${result.generation}`);
    await loadValidations();
  } catch (err) {
    log(`方案锁定失败：${err.message}`);
  }
}

async function registerProbe() {
  try {
    const probe = {
      id: 'probe-1',
      type: 'temperature',
      range_min: 100000,
      range_max: 140000,
      certificate: 'cert-1',
      calibration_batch: 'batch-a',
      valid_from: 0,
      valid_until: 1099511627776,
      status: 'active',
    };
    await api('POST', '/api/v1/probes', probe);
    log(`探头注册成功：${probe.id}`);
    await loadProbes();
  } catch (err) {
    log(`探头注册失败：${err.message}`);
  }
}

async function loadValidations() {
  const el = document.getElementById('validation-list');
  try {
    const plans = await api('GET', '/api/v1/validations');
    el.innerHTML = '';
    for (const p of plans) {
      const item = document.createElement('li');
      item.textContent = `${p.id} · 代次 ${p.generation} · ${p.status}`;
      el.appendChild(item);
    }
  } catch (err) {
    el.textContent = `加载失败：${err.message}`;
  }
}

async function loadProbes() {
  const el = document.getElementById('probe-list');
  try {
    const probes = await api('GET', '/api/v1/probes');
    el.innerHTML = '';
    for (const p of probes) {
      const item = document.createElement('li');
      item.textContent = `${p.id} · ${p.type} · 量程 ${p.range_min}–${p.range_max}`;
      el.appendChild(item);
    }
  } catch (err) {
    el.textContent = `加载失败：${err.message}`;
  }
}

document.getElementById('lock-plan').addEventListener('click', lockPlan);
document.getElementById('register-probe').addEventListener('click', registerProbe);

refresh();
setInterval(refresh, 3000);
