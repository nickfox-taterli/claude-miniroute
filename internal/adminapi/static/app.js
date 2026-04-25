const API = '';
let refreshInterval = null;
let requestsRefreshInterval = null;
let endpointsRefreshInterval = null;

/* ---------- Theme ---------- */
function initTheme() {
  const saved = localStorage.getItem('miniroute-theme');
  const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
  const theme = saved || (prefersDark ? 'dark' : 'light');
  setTheme(theme);
}

function setTheme(theme) {
  document.documentElement.setAttribute('data-theme', theme);
  const btn = document.getElementById('theme-toggle');
  if (btn) btn.textContent = theme === 'light' ? '🌙' : '☀️';
  localStorage.setItem('miniroute-theme', theme);
}

function toggleTheme() {
  const current = document.documentElement.getAttribute('data-theme') || 'dark';
  setTheme(current === 'light' ? 'dark' : 'light');
}

/* ---------- Formatters ---------- */
function fmtDuration(ms) {
  if (!ms) return '-';
  if (ms < 1000) return ms + 'ms';
  return (ms / 1000).toFixed(1) + 's';
}

function fmtTime(ts) {
  if (!ts) return '-';
  return new Date(ts).toLocaleTimeString('zh-CN');
}

function fmtDateTime(ts) {
  if (!ts) return '-';
  return new Date(ts).toLocaleString('zh-CN');
}

function fmtUptime(sec) {
  if (sec < 60) return sec + '秒';
  if (sec < 3600) return Math.floor(sec / 60) + '分' + (sec % 60) + '秒';
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  return h + '小时' + m + '分';
}

function fmtNumber(n) {
  const v = Number(n || 0);
  return v.toLocaleString('zh-CN');
}

function fmtMillion(n) {
  const v = Number(n || 0) / 1_000_000;
  return v.toFixed(3) + 'M';
}

function renderTokenUsage(usage) {
  const inTok = fmtMillion(usage?.input);
  const outTok = fmtMillion(usage?.output);
  const totalTok = fmtMillion(usage?.total);
  return `<span>输入 ${inTok}</span><span>输出 ${outTok}</span><span>总计 ${totalTok}</span>`;
}

function fmtTokenRate(outputTokens, latencyMS) {
  const out = Number(outputTokens || 0);
  const ms = Number(latencyMS || 0);
  if (out <= 0 || ms <= 0) return '-';
  return (out / (ms / 1000)).toFixed(1);
}

function formatRank(ep, isPeak) {
  const rank = ep.active_rank || ep.rank;
  if (isPeak && ep.alt_rank > 0) {
    return `<span class="badge yellow">高峰 ${rank}</span> <span style="color:var(--muted);font-size:0.85em">(普通 ${ep.rank})</span>`;
  }
  return `<span class="badge green">普通 ${rank}</span>` + (ep.alt_rank > 0 ? ` <span style="color:var(--muted);font-size:0.85em">(高峰 ${ep.alt_rank})</span>` : '');
}

function escHTML(s) {
  return String(s)
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}

function parseSummary(raw) {
  if (!raw) return {};
  if (typeof raw === 'object') return raw;
  try { return JSON.parse(raw); } catch (_) { return {}; }
}

function statusBadge(success, statusCode) {
  if (success) return '<span class="badge green">成功</span>';
  if (!statusCode) return '<span class="badge blue">进行中</span>';
  if (statusCode === 429) return '<span class="badge yellow">限流</span>';
  if (statusCode >= 500) return '<span class="badge red">' + statusCode + '</span>';
  return '<span class="badge red">' + (statusCode || '错误') + '</span>';
}

function availBadge(available, cooldownLeft) {
  if (available) return '<span class="badge green">可用</span>';
  return '<span class="badge yellow">冷却中 ' + (cooldownLeft || '') + '</span>';
}

async function fetchJSON(url) {
  const resp = await fetch(API + url);
  return resp.json();
}

/* ---------- Overview Page ---------- */
async function loadOverview() {
  try {
    const data = await fetchJSON('/api/dashboard');
    const s = data.status;
    document.getElementById('uptime').textContent = fmtUptime(s.uptime_sec);
    document.getElementById('requests-1h').textContent = s.requests_last_1h;
    document.getElementById('success-1h').textContent = s.success_last_1h;
    const rate = s.requests_last_1h > 0 ? ((s.success_last_1h / s.requests_last_1h) * 100).toFixed(1) + '%' : '-';
    document.getElementById('success-rate').textContent = rate;
    document.getElementById('inflight').textContent = s.proxy_inflight;
    const token1h = document.getElementById('token-1h');
    const token5h = document.getElementById('token-5h');
    const tokenToday = document.getElementById('token-today');
    const tokenMonth = document.getElementById('token-month');
    const tokenTotal = document.getElementById('token-total');
    token1h.innerHTML = renderTokenUsage(data.token_usage_1h);
    token5h.innerHTML = renderTokenUsage(data.token_usage_5h);
    tokenToday.innerHTML = renderTokenUsage(data.token_usage_today);
    tokenMonth.innerHTML = renderTokenUsage(data.token_usage_month);
    tokenTotal.innerHTML = renderTokenUsage(data.token_usage_total);

    const peakBadge = document.getElementById('peak-badge');
    if (peakBadge) peakBadge.style.display = data.is_peak ? '' : 'none';

    const tbody = document.getElementById('endpoints-tbody');
    tbody.innerHTML = data.endpoints.map(ep => `<tr>
      <td class="monospace">${ep.name}</td>
      <td>${ep.provider}</td>
      <td>${formatRank(ep, data.is_peak)}</td>
      <td>${availBadge(ep.available, ep.cooldown_left)}</td>
      <td>${ep.total_1h}</td>
      <td>${ep.total_1h > 0 ? (ep.success_rate_1h * 100).toFixed(1) + '%' : '-'}</td>
    </tr>`).join('');
  } catch (e) { console.error('overview load failed', e); }
}

function startAutoRefresh() {
  if (refreshInterval) clearInterval(refreshInterval);
  refreshInterval = setInterval(loadOverview, 1000);
}

function startRequestsAutoRefresh() {
  if (requestsRefreshInterval) clearInterval(requestsRefreshInterval);
  requestsRefreshInterval = setInterval(() => loadRequests(false, false), 1000);
}

function startEndpointsAutoRefresh() {
  if (endpointsRefreshInterval) clearInterval(endpointsRefreshInterval);
  endpointsRefreshInterval = setInterval(loadEndpoints, 1000);
}

/* ---------- Requests Page ---------- */
let requestsCache = [];
const expandedIds = new Set();

async function loadRequests(append, resetCache) {
  try {
    const model = document.getElementById('filter-model')?.value || '';
    const status = document.getElementById('filter-status')?.value || '';

    let url = '/api/requests?limit=200';
    if (model) url += `&model=${encodeURIComponent(model)}`;
    const data = await fetchJSON(url);
    let items = data.items || [];

    items = items.filter(r => {
      if (status === 'success' && !r.success) return false;
      if (status === 'fail' && r.success) return false;
      if (model && !r.resolved_model.includes(model)) return false;
      return true;
    });

    // Keep expanded state stable across refreshes/filtering.
    if (resetCache) {
      requestsCache = [];
    }
    renderRequestStream(items);
  } catch (e) { console.error('requests load failed', e); }
}

function createReqCard(item, animate) {
  const id = item.request_id;
  const div = document.createElement('div');
  div.className = 'req-card' + (animate ? ' new' : '');
  div.id = 'req-card-' + id;
  div.dataset.id = id;

  const resultText = item.error_type ? item.error_type.split('_').join(' ') : fmtDuration(item.latency_ms);

  div.innerHTML = `
    <div class="req-header" onclick="toggleReqDetail('${id}')">
      <div class="req-col req-col-id"><span class="label">请求ID</span><span class="val monospace">${escHTML(id.slice(0,18))}...</span></div>
      <div class="req-col req-col-model"><span class="label">模型</span><span class="val">${escHTML(item.resolved_model || '-')}</span></div>
      <div class="req-col req-col-result"><span class="label">结果</span><span class="val">${escHTML(resultText)}</span></div>
      <div class="req-col req-col-status"><span class="label">状态</span><span class="val">${statusBadge(item.success, item.status_code)}</span></div>
      <div class="req-col req-col-latency"><span class="label">首字延迟</span><span class="val latency">${fmtDuration(item.ttft_ms)}</span></div>
      <div class="req-col req-col-in-tok"><span class="label">输入Tok</span><span class="val">${fmtNumber(item.input_tokens)}</span></div>
      <div class="req-col req-col-out-tok"><span class="label">输出Tok</span><span class="val">${fmtNumber(item.output_tokens)}</span></div>
      <div class="req-col req-col-tps"><span class="label">Tok/s</span><span class="val">${fmtTokenRate(item.output_tokens, item.latency_ms)}</span></div>
      <div class="req-col req-col-time"><span class="label">时间</span><span class="val time">${fmtTime(item.start_ts)}</span></div>
    </div>
    <div class="req-detail" id="detail-${id}" style="display:none">
      <div id="detail-content-${id}">加载中...</div>
    </div>
  `;

  if (animate) {
    div.addEventListener('animationend', () => {
      div.classList.remove('new');
    }, { once: true });
  }
  return div;
}

function updateReqCard(el, item) {
  const resultText = item.error_type ? item.error_type.split('_').join(' ') : fmtDuration(item.latency_ms);
  const cols = el.querySelectorAll('.req-col');
  if (cols[1]) {
    const v = cols[1].querySelector('.val');
    if (v) v.textContent = item.resolved_model || '-';
  }
  if (cols[2]) {
    const v = cols[2].querySelector('.val');
    if (v) v.textContent = resultText;
  }
  if (cols[3]) {
    const v = cols[3].querySelector('.val');
    if (v) v.innerHTML = statusBadge(item.success, item.status_code);
  }
  if (cols[4]) {
    const v = cols[4].querySelector('.val');
    if (v) v.textContent = fmtDuration(item.ttft_ms);
  }
  if (cols[5]) {
    const v = cols[5].querySelector('.val');
    if (v) v.textContent = fmtNumber(item.input_tokens);
  }
  if (cols[6]) {
    const v = cols[6].querySelector('.val');
    if (v) v.textContent = fmtNumber(item.output_tokens);
  }
  if (cols[7]) {
    const v = cols[7].querySelector('.val');
    if (v) v.textContent = fmtTokenRate(item.output_tokens, item.latency_ms);
  }
  if (cols[8]) {
    const v = cols[8].querySelector('.val');
    if (v) v.textContent = fmtTime(item.start_ts);
  }
}

function syncExpandedState(items) {
  const currentIds = new Set(items.map(r => r.request_id));
  for (const id of Array.from(expandedIds)) {
    if (!currentIds.has(id)) {
      expandedIds.delete(id);
      continue;
    }
    const detail = document.getElementById('detail-' + id);
    if (detail) detail.style.display = '';
  }
}

function renderRequestStream(items) {
  const container = document.getElementById('requests-container');
  if (!container) return;

  const oldIds = new Set(requestsCache.map(r => r.request_id));
  const newIds = new Set(items.map(r => r.request_id));

  for (const el of Array.from(container.children)) {
    const id = el.dataset.id;
    if (id && !newIds.has(id)) {
      container.removeChild(el);
      expandedIds.delete(id);
    }
  }

  const frag = document.createDocumentFragment();
  for (const item of items) {
    const id = item.request_id;
    let el = document.getElementById('req-card-' + id);
    if (!el) {
      el = createReqCard(item, !oldIds.has(id));
    } else {
      updateReqCard(el, item);
    }
    frag.appendChild(el);
  }

  container.replaceChildren(frag);
  requestsCache = items.slice();
  syncExpandedState(items);
}

async function toggleReqDetail(id) {
  const detail = document.getElementById('detail-' + id);
  if (!detail) return;

  if (detail.style.display !== 'none') {
    detail.style.display = 'none';
    expandedIds.delete(id);
    return;
  }

  detail.style.display = '';
  expandedIds.add(id);
  const content = document.getElementById('detail-content-' + id);
  if (!content || content.dataset.loaded === '1') return;

  content.textContent = '加载中...';
  try {
    const data = await fetchJSON('/api/requests/' + id);
    const r = data.request || {};
    const reqSummary = parseSummary(r.request_summary);
    const respSummary = parseSummary(r.response_summary);
    const method = r.method || '-';
    const path = r.path || '-';
    const endpoint = r.selected_endpoint || '-';
    const model = r.resolved_model || '-';
    let html = `<div style="margin-bottom:0.5rem"><b>路径:</b> ${escHTML(method)} ${escHTML(path)} &nbsp; <b>节点:</b> ${escHTML(endpoint)} &nbsp; <b>模型:</b> ${escHTML(model)}</div>`;
    if (r.error_type || r.error_message) {
      html += `<div class="attempt" style="margin-bottom:0.5rem"><b>请求错误:</b> ${escHTML(r.error_type || '-')} ${r.error_message ? '&middot; ' + escHTML(r.error_message) : ''}</div>`;
    }
    html += '<div class="payload-block">';
    html += `<div class="payload-title">请求 Payload</div><pre class="payload-content">${escHTML(reqSummary.payload_preview || '-')}</pre>`;
    html += '</div>';
    html += '<div class="payload-block">';
    html += `<div class="payload-title">响应 Payload</div><pre class="payload-content">${escHTML(respSummary.payload_preview || '-')}</pre>`;
    html += '</div>';
    html += `<div class="attempt"><a class="btn btn-sm" href="/api/requests/${encodeURIComponent(id)}/download">下载当前请求信息包</a></div>`;
    if (data.attempts && data.attempts.length > 0) {
      html += '<div class="attempts">';
      data.attempts.forEach(a => {
        const attemptNo = a.attempt_no ?? '-';
        const endpointName = a.endpoint_name || '-';
        const status = a.status_code ?? '-';
        const retryTag = a.was_retry ? ' (重试)' : '';
        const fallbackTag = a.was_fallback ? ' (兜底)' : '';
        const errInfo = a.error_type || a.error_message
          ? ` 错误=${escHTML(a.error_type || '-')}${a.error_message ? ' (' + escHTML(a.error_message) + ')' : ''}`
          : '';
        html += `<div class="attempt">#${attemptNo} <b>${escHTML(endpointName)}</b> 状态=${status} 首字延迟=${fmtDuration(a.ttft_ms)} 延迟=${fmtDuration(a.latency_ms)}${retryTag}${fallbackTag}${errInfo}</div>`;
      });
      html += '</div>';
    }
    content.innerHTML = html;
    content.dataset.loaded = '1';
  } catch (e) { content.textContent = '加载失败: ' + e; }
}

/* ---------- Endpoints Page ---------- */
let endpointsPeak = false;

async function loadEndpoints() {
  try {
    const data = await fetchJSON('/api/endpoints');
    const items = data.items || [];
    endpointsPeak = !!data.is_peak;
    const container = document.getElementById('endpoints-container');
    container.innerHTML = items.map(ep => `<div class="ep-card">
      <div style="display:flex;justify-content:space-between;align-items:center">
        <span class="ep-name">${ep.name}</span>
        ${availBadge(ep.available, ep.cooldown_left)}
      </div>
      <div class="ep-meta">
        <div><span class="meta-label">配置名称:</span>${ep.provider}</div>
        <div><span class="meta-label">模型支持:</span>${ep.model}</div>
        <div><span class="meta-label">优先级:</span>${formatRank(ep, endpointsPeak)}</div>
      </div>
      <div class="ep-stats">
        <div><span class="stat-label">1小时请求</span><div class="stat-val">${ep.total_1h}</div></div>
        <div><span class="stat-label">1小时错误</span><div class="stat-val">${ep.recent_errors_1h}</div></div>
        <div><span class="stat-label">成功率</span><div class="stat-val">${ep.total_1h > 0 ? (ep.success_rate_1h * 100).toFixed(1) + '%' : '-'}</div></div>
        <div><span class="stat-label">连续错误</span><div class="stat-val">${ep.consec_errors}</div></div>
      </div>
    </div>`).join('');
  } catch (e) { console.error('endpoints load failed', e); }
}
