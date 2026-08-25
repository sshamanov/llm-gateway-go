(function() {
  'use strict';

  const POLL_INTERVALS = {
    backends: 2000,
    hosts: 2000,
    queue: 2000,
    scheduler: 3000,
    models: 10000,
    config: 30000
  };

  let lastUpdate = null;

  function formatUptime(seconds) {
    const h = Math.floor(seconds / 3600);
    const m = Math.floor((seconds % 3600) / 60);
    const s = Math.floor(seconds % 60);
    return `${h}h ${m}m ${s}s`;
  }

  function badgeClass(status) {
    const map = {
      healthy: 'badge-healthy', degraded: 'badge-degraded',
      down: 'badge-down', disabled: 'badge-disabled',
      full: 'badge-full', available: 'badge-available'
    };
    return map[status] || '';
  }

  async function fetchJSON(url) {
    const resp = await fetch(url);
    if (!resp.ok) throw new Error(`${url}: ${resp.status}`);
    return resp.json();
  }

  // ---- Backends Panel ----
  async function updateBackends() {
    const el = document.getElementById('backends-content');
    try {
      const data = await fetchJSON('/debug/backends');
      if (!data || !data.backends) { el.innerHTML = '<p>No backends</p>'; return; }
      let html = '<table><thead><tr><th>ID</th><th>Type</th><th>Health</th><th>Active</th><th>Models</th></tr></thead><tbody>';
      for (const b of data.backends) {
        const health = b.healthy ? 'healthy' : (b.enabled ? 'down' : 'disabled');
        html += `<tr>
          <td>${b.id || b.backend_id || '-'}</td>
          <td>${b.type || 'ollama'}</td>
          <td><span class="badge ${badgeClass(health)}">${health}</span></td>
          <td>${b.active_jobs ?? '-'}</td>
          <td>${(b.loaded_models || []).join(', ') || '-'}</td>
        </tr>`;
      }
      html += '</tbody></table>';
      el.innerHTML = html;
    } catch (err) { el.classList.add('stale'); }
  }

  // ---- Hosts Panel ----
  async function updateHosts() {
    const el = document.getElementById('hosts-content');
    try {
      const data = await fetchJSON('/debug/hosts');
      if (!data || !data.hosts) { el.innerHTML = '<p>No hosts</p>'; return; }
      let html = '<table><thead><tr><th>Host</th><th>Active</th><th>Capacity</th><th>Status</th></tr></thead><tbody>';
      for (const h of data.hosts) {
        // Server-provided status (accounts for backend health); fall back to
        // capacity-based for older endpoints that omit it.
        const status = h.status || (h.active_jobs >= h.capacity ? 'full' : 'available');
        html += `<tr>
          <td>${h.host_id}</td>
          <td>${h.active_jobs}</td>
          <td>${h.capacity}</td>
          <td><span class="badge ${badgeClass(status)}">${status}</span></td>
        </tr>`;
      }
      html += '</tbody></table>';
      el.innerHTML = html;
    } catch (err) { el.classList.add('stale'); }
  }

  // ---- Queue Panel ----
  async function updateQueue() {
    const el = document.getElementById('queue-content');
    try {
      const data = await fetchJSON('/debug/queue');
      if (!data || !data.queue) { el.innerHTML = '<p>Queue unavailable</p>'; return; }
      const q = data.queue;
      let html = `<p>Total: ${q.total_jobs} | Pending: ${q.pending_count}</p>`;
      if (q.by_kind) {
        html += '<p>By kind: ';
        for (const [k, v] of Object.entries(q.by_kind)) {
          html += `${k}: ${v} `;
        }
        html += '</p>';
      }
      if (q.top_jobs && q.top_jobs.length > 0) {
        html += '<table><thead><tr><th>ID</th><th>Kind</th><th>State</th><th>Age</th><th>Model</th></tr></thead><tbody>';
        for (const j of q.top_jobs) {
          html += `<tr>
            <td>${j.id.substring(0, 8)}</td>
            <td>${j.kind}</td>
            <td>${j.state}</td>
            <td>${(j.age_ms / 1000).toFixed(1)}s</td>
            <td>${j.requested_model}</td>
          </tr>`;
        }
        html += '</tbody></table>';
      }
      el.innerHTML = html;
    } catch (err) { el.classList.add('stale'); }
  }

  // ---- Scheduler Panel ----
  async function updateScheduler() {
    const el = document.getElementById('scheduler-content');
    try {
      const data = await fetchJSON('/debug/scheduler');
      if (!data || !data.scheduler) { el.innerHTML = '<p>No data</p>'; return; }
      const s = data.scheduler;
      document.getElementById('uptime').textContent = `Uptime: ${formatUptime(s.uptime_seconds || 0)}`;
      document.getElementById('health').textContent = s.queue_pending > 10 ? 'busy' : 'healthy';
      document.getElementById('health').className = `badge ${badgeClass(s.queue_pending > 10 ? 'degraded' : 'healthy')}`;

      let html = '<table>';
      if (s.config) {
        html += `<tr><td>Strategy</td><td>${s.config.strategy || '-'}</td></tr>`;
        html += `<tr><td>Queue Max</td><td>${s.config.queue_max || '-'}</td></tr>`;
        html += `<tr><td>Aging/s</td><td>${s.config.aging_per_sec || '-'}</td></tr>`;
        html += `<tr><td>Retry Max</td><td>${s.config.retry_max || '-'}</td></tr>`;
        html += `<tr><td>Lookahead</td><td>${s.config.top_n_lookahead || '-'}</td></tr>`;
        html += `<tr><td>Exploration</td><td>${s.config.exploration_bonus || '-'}</td></tr>`;
      }
      html += `<tr><td>Tracked</td><td>${s.stats_count || 0} backend/model pairs</td></tr>`;
      html += '</table>';
      el.innerHTML = html;
    } catch (err) { el.classList.add('stale'); }
  }

  // ---- Aliases Panel ----
  async function updateAliases() {
    const el = document.getElementById('aliases-content');
    try {
      const data = await fetchJSON('/debug/models');
      if (!data) { el.innerHTML = '<p>No data</p>'; return; }
      let html = '';
      if (data.aliases && data.aliases.length > 0) {
        html += '<table><thead><tr><th>Name</th><th>Primary</th><th>Backups</th></tr></thead><tbody>';
        for (const a of data.aliases) {
          html += `<tr>
            <td>${a.name || '-'}</td>
            <td>${a.primary_model || '-'}</td>
            <td>${(a.backup_models || []).join(', ') || '-'}</td>
          </tr>`;
        }
        html += '</tbody></table>';
      }
      el.innerHTML = html || '<p>No aliases</p>';
    } catch (err) { el.classList.add('stale'); }
  }

  // ---- Models Panel (native) ----
  async function updateModels() {
    const el = document.getElementById('models-content');
    try {
      const data = await fetchJSON('/debug/models');
      if (!data) { el.innerHTML = '<p>No data</p>'; return; }
      if (data.native_models && data.native_models.length > 0) {
        el.innerHTML = '<p>' + data.native_models.join(', ') + '</p>';
      } else {
        el.innerHTML = '<p>No native models</p>';
      }
    } catch (err) { el.classList.add('stale'); }
  }

  // ---- Learned Stats Panel ----
  async function updateStats() {
    const el = document.getElementById('stats-content');
    try {
      const data = await fetchJSON('/debug/scheduler');
      if (!data || !data.scheduler) { el.innerHTML = '<p>No data</p>'; return; }
      const s = data.scheduler;
      if (!s.stats || Object.keys(s.stats).length === 0) {
        el.innerHTML = '<p>No learned stats yet — send requests to populate.</p>';
        return;
      }

      const formatTime = (t) => {
        if (!t) return '-';
        const d = new Date(t);
        return d.toLocaleTimeString();
      };

      // Only show pairs with samples > 0 (learned).
      const learned = {};
      for (const [key, v] of Object.entries(s.stats)) {
        if (v.samples > 0) {
          learned[key] = v;
        }
      }

      if (Object.keys(learned).length === 0) {
        el.innerHTML = '<p>No learned stats yet — send requests to populate.</p>';
        return;
      }

      let html = '<table><thead><tr><th>Backend</th><th>Model</th><th>Samples</th><th>TPS</th><th>Cold Load</th><th>Last OK</th></tr></thead><tbody>';
      for (const [key, v] of Object.entries(learned)) {
        const parts = key.split('/');
        const backend = parts[0] || key;
        const model = parts.slice(1).join('/') || '-';
        const tps = (v.avg_tokens_per_second || 0).toFixed(1);
        const cold = (v.avg_cold_load_time || 0).toFixed(2) + 's';
        html += `<tr>
          <td>${backend}</td>
          <td>${model}</td>
          <td>${v.samples || 0}</td>
          <td>${tps}</td>
          <td>${cold}</td>
          <td>${formatTime(v.last_success)}</td>
        </tr>`;
      }
      html += '</tbody></table>';
      el.innerHTML = html;
    } catch (err) { el.classList.add('stale'); }
  }

  // ---- Failures Panel ----
  async function updateFailures() {
    const el = document.getElementById('failures-content');
    try {
      const data = await fetchJSON('/debug/scheduler');
      if (!data || !data.scheduler) { el.innerHTML = '<p>No data</p>'; return; }
      const s = data.scheduler;
      if (!s.stats || Object.keys(s.stats).length === 0) {
        el.innerHTML = '<p>No failures</p>';
        return;
      }

      const formatTime = (t) => {
        if (!t) return '-';
        const d = new Date(t);
        return d.toLocaleTimeString();
      };

      // Collect pairs with failures (even if they have samples).
      const failing = {};
      for (const [key, v] of Object.entries(s.stats)) {
        if (v.consecutive_failures > 0) {
          failing[key] = v;
        }
      }

      if (Object.keys(failing).length === 0) {
        el.innerHTML = '<p>No failures</p>';
        return;
      }

      let html = '<table><thead><tr><th>Backend</th><th>Model</th><th>Failures</th><th>Last Failure</th></tr></thead><tbody>';
      for (const [key, v] of Object.entries(failing)) {
        const parts = key.split('/');
        const backend = parts[0] || key;
        const model = parts.slice(1).join('/') || '-';
        html += `<tr>
          <td>${backend}</td>
          <td>${model}</td>
          <td style="color:#e74c3c;font-weight:bold">${v.consecutive_failures || 0}</td>
          <td>${formatTime(v.last_failure)}</td>
        </tr>`;
      }
      html += '</tbody></table>';
      el.innerHTML = html;
    } catch (err) { el.classList.add('stale'); }
  }

  function updateTimestamp() {
    document.getElementById('last-update').textContent = 'Updated: ' + new Date().toLocaleTimeString();
  }

  // Polling loop
  function poll() {
    updateBackends();
    updateHosts();
    updateAliases();
    updateModels();
    updateQueue();
    updateScheduler();
    updateStats();
    updateFailures();
    updateTimestamp();
  }

  // Initial load then poll
  poll();
  setInterval(updateBackends, POLL_INTERVALS.backends);
  setInterval(updateHosts, POLL_INTERVALS.hosts);
  setInterval(updateAliases, POLL_INTERVALS.models);
  setInterval(updateModels, POLL_INTERVALS.models);
  setInterval(updateQueue, POLL_INTERVALS.queue);
  setInterval(updateScheduler, POLL_INTERVALS.scheduler);
  setInterval(updateStats, POLL_INTERVALS.config);
  setInterval(updateFailures, POLL_INTERVALS.config);
  setInterval(updateTimestamp, 1000);
})();
