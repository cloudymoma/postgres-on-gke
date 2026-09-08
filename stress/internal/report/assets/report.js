/* pgstress report: builds every table and chart from the embedded JSON. */
(function () {
  'use strict';
  const runs = JSON.parse(document.getElementById('pgstress-data').textContent);
  const multi = runs.length > 1;

  // Fixed categorical order (validated palette); never cycled.
  const SERIES = ['#2a78d6', '#eb6834', '#1baf7a', '#eda100', '#e87ba4', '#008300', '#4a3aa7', '#e34948'];
  const INK2 = '#52514e', MUTED = '#898781', GRID = '#e1e0d9', AXIS = '#c3c2b7';
  const color = i => SERIES[i % SERIES.length];

  const esc = v => String(v ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const fmt = (v, d = 1) => (v == null || Number.isNaN(v)) ? '–' : Number(v).toLocaleString(undefined, { maximumFractionDigits: d, minimumFractionDigits: d });
  const int = v => (v == null) ? '–' : Math.round(v).toLocaleString();
  const pct = v => fmt(v * 100, 2) + '%';
  const runLabel = (r, i) => multi ? `#${i + 1} ${r.scenario}` : r.scenario; // raw: used as chart labels (Chart.js draws text on canvas)
  const runHTML = (r, i) => esc(runLabel(r, i));
  const secs = s => s >= 3600 ? `${fmt(s / 3600)} h` : s >= 60 ? `${fmt(s / 60)} min` : `${fmt(s, 0)} s`;

  Chart.defaults.font.family = getComputedStyle(document.body).fontFamily;
  Chart.defaults.color = INK2;
  Chart.defaults.borderColor = GRID;

  /* ---------- header ---------- */
  const r0 = runs[0];
  document.getElementById('subtitle').textContent = multi
    ? runs.map((r, i) => `#${i + 1}: ${r.scenario}, ${new Date(r.startedAt).toLocaleString()}`).join(' · ') // textContent: safe
    : `${new Date(r0.startedAt).toLocaleString()} · ${secs(r0.durationSec)} · PostgreSQL ${r0.server.version || '?'} · ${r0.server.instances || '?'} instance(s) · stages: ${r0.stagePlan.map(s => s.workers).join(' → ')} workers`;

  if (!multi) {
    const o = r0.overall;
    const errRate = o.count + o.errors > 0 ? o.errors / (o.count + o.errors) : 0;
    const tiles = [
      ['Throughput', fmt(o.tps, 0), 'tx/s'], ['Transactions', int(o.count), ''],
      ['p50', fmt(o.p50, 2), 'ms'], ['p95', fmt(o.p95, 2), 'ms'], ['p99', fmt(o.p99, 2), 'ms'],
      ['Error rate', fmt(errRate * 100, 3), '%'],
    ];
    document.getElementById('tiles').innerHTML = tiles.map(([k, v, u]) =>
      `<div class="tile"><div class="k">${k}</div><div class="v">${v}<span class="u">${u}</span></div></div>`).join('');
  }

  /* ---------- tables ---------- */
  function table(id, head, rows) {
    document.getElementById(id).innerHTML =
      `<thead><tr>${head.map(h => `<th>${h}</th>`).join('')}</tr></thead><tbody>${rows.map(r => `<tr>${r.map((c, i) => `<td class="${i === 0 ? 'name' : ''}">${c}</td>`).join('')}</tr>`).join('')}</tbody>`;
  }
  const sw = i => `<span class="swatch" style="background:${color(i)}"></span>`;

  table('overall', ['Run', 'Duration', 'PostgreSQL', 'Instances', 'Max workers', 'tx/s', 'p50 ms', 'p95 ms', 'p99 ms', 'p99.9 ms', 'Errors'],
    runs.map((r, i) => [sw(i) + runHTML(r, i), secs(r.durationSec), esc(r.server.version || '–'), r.server.instances || '–',
      Math.max(...r.stagePlan.map(s => s.workers)), fmt(r.overall.tps, 0), fmt(r.overall.p50, 2), fmt(r.overall.p95, 2),
      fmt(r.overall.p99, 2), fmt(r.overall.p999, 2), int(r.overall.errors)]));

  const stageRows = [];
  runs.forEach((r, i) => r.stages.forEach(s => stageRows.push([
    (multi ? sw(i) + `#${i + 1} · ` : '') + `stage ${s.index + 1}`, s.workers, secs(s.durationSec), fmt(s.tps, 0),
    fmt(s.p50, 2), fmt(s.p95, 2), fmt(s.p99, 2), fmt(s.max, 1), int(s.errors)])));
  table('stages', ['Stage', 'Workers', 'Duration', 'tx/s', 'p50 ms', 'p95 ms', 'p99 ms', 'max ms', 'Errors'], stageRows);

  const stmtRows = [];
  runs.forEach((r, i) => r.statements.forEach(s => stmtRows.push([
    (multi ? sw(i) + `#${i + 1} · ` : '') + esc(s.name), int(s.count), fmt(s.tps, 0), fmt(s.mean, 2), fmt(s.p50, 2), fmt(s.p95, 2),
    fmt(s.p99, 2), fmt(s.p999, 2), fmt(s.max, 1), int(s.errors),
    s.errorsByCode ? Object.entries(s.errorsByCode).map(([k, v]) => `${esc(k)}: ${v}`).join(', ') : ''])));
  table('statements', ['Statement', 'Count', 'tx/s', 'mean ms', 'p50 ms', 'p95 ms', 'p99 ms', 'p99.9 ms', 'max ms', 'Errors', 'By code'], stmtRows);

  /* ---------- charts ---------- */
  // Shaded stage bands (from run #1) with worker count labels.
  const stageBands = {
    id: 'stageBands',
    beforeDraw(chart) {
      const series = r0.series; if (!series.length) return;
      const { ctx, chartArea: a, scales: { x } } = chart;
      const bounds = [];
      let cur = null;
      for (const b of series) {
        if (!cur || cur.stage !== b.stage) { cur = { stage: b.stage, workers: b.workers, from: b.t, to: b.t }; bounds.push(cur); }
        cur.to = b.t + 1;
      }
      ctx.save();
      bounds.forEach((s, i) => {
        const x0 = Math.max(a.left, x.getPixelForValue(s.from)), x1 = Math.min(a.right, x.getPixelForValue(s.to));
        if (x1 <= x0) return;
        if (i % 2 === 1) { ctx.fillStyle = 'rgba(11,11,11,0.035)'; ctx.fillRect(x0, a.top, x1 - x0, a.bottom - a.top); }
        ctx.fillStyle = MUTED; ctx.font = '11px ' + Chart.defaults.font.family; ctx.textAlign = 'left';
        ctx.fillText(`${s.workers} w`, x0 + 4, a.top + 12);
      });
      ctx.restore();
    },
  };

  function lineChart(id, datasets, yLabel, opts = {}) {
    const el = document.getElementById(id); if (!el) return;
    if (!datasets.length || datasets.every(d => !d.data.length)) {
      el.parentElement.innerHTML = '<p class="hint">No data.</p>'; return;
    }
    new Chart(el, {
      type: 'line',
      data: { datasets: datasets.map(d => ({ borderWidth: 2, pointRadius: 0, pointHoverRadius: 4, tension: 0.15, fill: false, ...d })) },
      options: {
        animation: false, responsive: true, maintainAspectRatio: false, parsing: false,
        interaction: { mode: 'nearest', axis: 'x', intersect: false },
        plugins: {
          legend: { display: datasets.length > 1, position: 'bottom', labels: { boxWidth: 10, boxHeight: 10, usePointStyle: false } },
          tooltip: { callbacks: { title: it => `t = ${it[0].parsed.x}s`, label: it => ` ${it.dataset.label}: ${fmt(it.parsed.y, opts.digits ?? 1)}` } },
        },
        scales: {
          x: { type: 'linear', title: { display: true, text: 'seconds', color: MUTED }, grid: { color: GRID }, border: { color: AXIS }, ticks: { color: MUTED } },
          y: { beginAtZero: true, min: opts.min, max: opts.max, title: { display: true, text: yLabel, color: MUTED }, grid: { color: GRID }, border: { color: AXIS }, ticks: { color: MUTED } },
        },
      },
      plugins: opts.bands === false ? [] : [stageBands],
    });
  }
  const pts = (arr, tf, yf) => arr.map(b => ({ x: tf(b), y: yf(b) })).filter(p => p.y != null && !Number.isNaN(p.y));
  const trimTail = s => { let n = s.length; while (n > 0 && s[n - 1].total.count === 0 && s[n - 1].total.errors === 0) n--; return s.slice(0, n); }; // drop empty buckets after workers stopped

  // Throughput
  lineChart('c-tps', runs.map((r, i) => ({ label: runLabel(r, i), borderColor: color(i), data: pts(trimTail(r.series), b => b.t, b => b.total.count) })), 'tx/s', { digits: 0 });

  // Latency: p50/p95/p99 for one run; p99 per run when comparing.
  lineChart('c-lat', multi
    ? runs.map((r, i) => ({ label: `${runLabel(r, i)} p99`, borderColor: color(i), data: pts(trimTail(r.series).filter(b => b.total.count), b => b.t, b => b.total.p99) }))
    : [['p50', 'p50', 0], ['p95', 'p95', 1], ['p99', 'p99', 2]].map(([k, l, c]) => ({ label: l, borderColor: color(c), data: pts(trimTail(r0.series).filter(b => b.total.count), b => b.t, b => b.total[k]) })),
    'ms', { digits: 2 });

  // Errors
  lineChart('c-err', runs.map((r, i) => ({ label: runLabel(r, i), borderColor: color(i), data: pts(trimTail(r.series), b => b.t, b => b.total.errors) })), 'errors/s', { digits: 0 });

  // Per-statement p99 bars (grouped by run)
  (function () {
    const el = document.getElementById('c-stmt');
    const names = [...new Set(runs.flatMap(r => r.statements.map(s => s.name)))].slice(0, 8);
    new Chart(el, {
      type: 'bar',
      data: {
        labels: names,
        datasets: runs.map((r, i) => ({
          label: runLabel(r, i), backgroundColor: color(i), borderRadius: 4, borderSkipped: 'start', maxBarThickness: 36,
          data: names.map(n => (r.statements.find(s => s.name === n) || {}).p99 ?? null),
        })),
      },
      options: {
        animation: false, responsive: true, maintainAspectRatio: false,
        plugins: { legend: { display: multi, position: 'bottom', labels: { boxWidth: 10, boxHeight: 10 } },
          tooltip: { callbacks: { label: it => ` ${it.dataset.label}: ${fmt(it.parsed.y, 2)} ms` } } },
        scales: { x: { grid: { display: false }, border: { color: AXIS }, ticks: { color: INK2 } },
          y: { beginAtZero: true, title: { display: true, text: 'p99 ms', color: MUTED }, grid: { color: GRID }, border: { color: AXIS }, ticks: { color: MUTED } } },
      },
    });
  })();

  /* ---------- server ---------- */
  const notes = [];
  runs.forEach((r, i) => Object.entries(r.samplerErrors || {}).forEach(([k, v]) => notes.push(`${multi ? `#${i + 1} ` : ''}${esc(k)}: ${esc(v)}`)));
  const noteEl = document.getElementById('server-note');
  if (notes.length) { noteEl.className = 'warn'; noteEl.innerHTML = notes.map(n => `<div>${n}</div>`).join(''); }
  else noteEl.textContent = `Sampled from pg_stat_* on the primary every ${multi ? 'few' : Math.max(1, (r0.samples[1]?.t ?? 5) - (r0.samples[0]?.t ?? 0))} seconds.`;

  const S = r => r.samples || [];
  if (multi) {
    lineChart('c-conn', runs.map((r, i) => ({ label: `${runLabel(r, i)} active`, borderColor: color(i), data: pts(S(r), s => s.t, s => s.active) })), 'connections', { digits: 0 });
    lineChart('c-xact', runs.map((r, i) => ({ label: `${runLabel(r, i)} commits`, borderColor: color(i), data: pts(S(r), s => s.t, s => s.commitsPerSec) })), 'per s', { digits: 0 });
  } else {
    lineChart('c-conn', [['active', 'active', 0], ['idle', 'idle', 1], ['idleInTx', 'idle in tx', 2], ['waiting', 'waiting on lock/IO', 3]]
      .map(([k, l, c]) => ({ label: l, borderColor: color(c), data: pts(S(r0), s => s.t, s => s[k]) })), 'connections', { digits: 0 });
    lineChart('c-xact', [['commitsPerSec', 'commits', 0], ['rollbacksPerSec', 'rollbacks', 1]]
      .map(([k, l, c]) => ({ label: l, borderColor: color(c), data: pts(S(r0), s => s.t, s => s[k]) })), 'per s', { digits: 0 });
  }
  lineChart('c-cache', runs.map((r, i) => ({ label: runLabel(r, i), borderColor: color(i), data: pts(S(r), s => s.t, s => s.cacheHitRatio == null ? null : s.cacheHitRatio * 100) })), '% hits', { digits: 2, min: 0, max: 100 });
  lineChart('c-wal', runs.map((r, i) => ({ label: runLabel(r, i), borderColor: color(i), data: pts(S(r), s => s.t, s => s.walBytesPerSec / 1048576) })), 'MiB/s', { digits: 2 });

  // Replication lag: one line per replica per run.
  const lag = [];
  runs.forEach((r, i) => {
    const names = [...new Set(S(r).flatMap(s => (s.replicas || []).map(x => x.name)))];
    names.forEach((n, j) => lag.push({
      label: `${multi ? runLabel(r, i) + ' ' : ''}${n}`, borderColor: color(multi ? i : j),
      borderDash: multi && j ? [4, 3] : [],
      data: pts(S(r), s => s.t, s => (s.replicas || []).find(x => x.name === n)?.lagMs),
    }));
  });
  lineChart('c-lag', lag, 'ms', { digits: 1 });

  document.getElementById('client-info').innerHTML = runs.map((r, i) => {
    const c = r.client || {};
    return `${multi ? `<div class="k">run</div><div>${runHTML(r, i)}</div>` : ''}
      <div class="k">CPUs visible</div><div>${c.numCpu ?? '–'} (GOMAXPROCS ${c.gomaxprocs ?? '–'})</div>
      <div class="k">CPU time used</div><div>${fmt(c.cpuSeconds, 1)} s</div>
      <div class="k">Client CPU utilisation</div><div>${pct(c.cpuUtilization || 0)}${(c.cpuUtilization || 0) > 0.8 ? ' ⚠ client may be the bottleneck' : ''}</div>`;
  }).join('');
})();
