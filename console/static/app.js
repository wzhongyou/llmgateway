// -- auth --
let authToken = localStorage.getItem('llmgate_admin_token') || '';

function setAuth() {
  const t = prompt('Admin token:');
  if (t !== null) {
    authToken = t;
    localStorage.setItem('llmgate_admin_token', t);
  }
}

async function api(path, method, body) {
  const headers = {};
  if (authToken) headers['Authorization'] = 'Bearer ' + authToken;
  if (body) headers['Content-Type'] = 'application/json';
  const res = await fetch('/admin/api' + path, {
    method: method || 'GET',
    headers,
    body: body ? JSON.stringify(body) : undefined,
  });
  if (res.status === 401) {
    setAuth();
    if (authToken) return api(path, method, body); // retry with new token
    throw new Error('unauthorized — try again');
  }
  if (res.status === 204) return null;
  if (!res.ok) {
    const err = await res.json().catch(() => ({}));
    throw new Error(err.error || res.statusText);
  }
  return res.json();
}

// -- tab switching --
document.querySelectorAll('.tab').forEach(btn => {
  btn.addEventListener('click', () => {
    document.querySelectorAll('.tab').forEach(b => b.classList.remove('active'));
    document.querySelectorAll('.tab-content').forEach(c => c.classList.remove('active'));
    btn.classList.add('active');
    document.getElementById('tab-' + btn.dataset.tab).classList.add('active');
    const init = window['init_' + btn.dataset.tab];
    if (init) init();
  });
});

// -- channels --
async function init_channels() {
  try {
    const channels = await api('/channels');
    renderChannels(channels);
  } catch (e) { showError('tab-channels', e); }
}

function renderChannels(channels) {
  const el = document.getElementById('tab-channels');
  if (!channels || !channels.length) {
    el.innerHTML = '<p class="empty">No channels configured. Add one to get started.</p>';
    return;
  }
  let html = '<div class="toolbar"><button class="primary" onclick="showChannelForm()">+ Add Channel</button></div>';
  html += '<table><thead><tr><th>Name</th><th>Models</th><th>Calls</th><th>Errors</th><th>Latency</th><th>Key</th><th>Actions</th></tr></thead><tbody>';
  for (const ch of channels) {
    html += `<tr>
      <td><strong>${esc(ch.name)}</strong></td>
      <td>${(ch.models||[]).length}</td>
      <td>${ch.total_calls}</td>
      <td>${ch.error_calls}</td>
      <td>${ch.avg_latency_ms.toFixed(0)}ms</td>
      <td><code>${esc(ch.key_ref)}</code></td>
      <td class="actions">
        <button onclick="testChannel('${esc(ch.name)}')">Test</button>
        <button onclick="editChannel('${esc(ch.name)}')">Edit</button>
        <button class="danger" onclick="deleteChannel('${esc(ch.name)}')">Del</button>
      </td></tr>`;
  }
  html += '</tbody></table>';
  el.innerHTML = html;
}

function showChannelForm(existing) {
  const def = existing || { name: '', key: '', base_url: '', default_model: '', protocol: '' };
  const html = `<div class="form-panel">
    <h3>${existing ? 'Edit' : 'Add'} Channel</h3>
    <label>Name: <input id="cf-name" value="${esc(def.name)}" ${existing ? 'readonly' : ''}></label>
    <label>Key: <input id="cf-key" value="${esc(def.key || '')}" placeholder="${'${ENV_VAR}'} or plaintext"></label>
    <label>Base URL: <input id="cf-url" value="${esc(def.base_url || '')}" placeholder="optional"></label>
    <label>Default Model: <input id="cf-model" value="${esc(def.default_model || '')}" placeholder="optional"></label>
    <label>Protocol: <select id="cf-proto">
      <option value="">builtin</option>
      <option value="openai-compat" ${def.protocol==='openai-compat'?'selected':''}>openai-compat</option>
    </select></label>
    <div class="form-actions">
      <button class="primary" onclick="saveChannel('${esc(def.name)}')">Save</button>
      <button onclick="init_channels()">Cancel</button>
    </div>
  </div>`;
  document.getElementById('tab-channels').innerHTML = html;
}

async function saveChannel(name) {
  const cfg = {
    key: document.getElementById('cf-key').value,
    base_url: document.getElementById('cf-url').value,
    default_model: document.getElementById('cf-model').value,
    protocol: document.getElementById('cf-proto').value,
  };
  try {
    await api('/channels/' + encodeURIComponent(name), 'PUT', cfg);
    init_channels();
  } catch (e) { alert(e); }
}

async function testChannel(name) {
  try {
    const r = await api('/channels/' + encodeURIComponent(name) + '/test', 'POST');
    alert(r.status === 'ok' ? `OK — ${r.model} ${r.latency_ms.toFixed(0)}ms` : 'FAIL: ' + r.error);
  } catch (e) { alert(e); }
}

async function editChannel(name) {
  const ch = await api('/channels/' + encodeURIComponent(name));
  showChannelForm({ name, key: ch.key_ref, base_url: '', default_model: ch.models[0] || '', protocol: '' });
}

async function deleteChannel(name) {
  if (!confirm('Delete ' + name + '?')) return;
  try {
    await api('/channels/' + encodeURIComponent(name), 'DELETE');
    init_channels();
  } catch (e) { alert(e); }
}

// -- playground --
async function init_playground() {
  if (document.getElementById('pg-provider')) return; // already built
  const channels = await api('/channels');
  const el = document.getElementById('tab-playground');
  const opts = channels.map(c => `<option value="${esc(c.name)}">${esc(c.name)} (${(c.models||[]).length} models)</option>`).join('');
  el.innerHTML = `<div class="playground-layout">
    <div class="pg-controls">
      <label>Provider: <select id="pg-provider" onchange="pgLoadModels()">${opts}</select></label>
      <label>Model: <select id="pg-model"></select></label>
      <label>Stream: <input type="checkbox" id="pg-stream"></label>
      <label>Temperature: <input type="number" id="pg-temp" value="0.7" step="0.1" min="0" max="2" style="width:60px"></label>
      <label>Max Tokens: <input type="number" id="pg-max-tok" value="1024" style="width:80px"></label>
    </div>
    <div class="pg-messages" id="pg-messages"></div>
    <div class="pg-input">
      <textarea id="pg-system" rows="2" placeholder="System prompt (optional)"></textarea>
      <textarea id="pg-user" rows="3" placeholder="User message"></textarea>
      <button class="primary" onclick="pgSend()">Send</button>
    </div>
    <div class="pg-meta" id="pg-meta"></div>
  </div>`;
  pgLoadModels();
}

async function pgLoadModels() {
  const name = document.getElementById('pg-provider').value;
  try {
    const ch = await api('/channels/' + encodeURIComponent(name));
    const sel = document.getElementById('pg-model');
    sel.innerHTML = ch.models.map(m => `<option>${esc(m)}</option>`).join('');
  } catch (e) {}
}

async function pgSend() {
  const provider = document.getElementById('pg-provider').value;
  const model = document.getElementById('pg-model').value;
  const stream = document.getElementById('pg-stream').checked;
  const system = document.getElementById('pg-system').value;
  const user = document.getElementById('pg-user').value;
  if (!user.trim()) return;

  const msgs = [];
  if (system.trim()) msgs.push({ role: 'system', content: system });
  msgs.push({ role: 'user', content: user });

  const req = {
    model: model,
    messages: msgs,
    temperature: parseFloat(document.getElementById('pg-temp').value) || 0.7,
    max_tokens: parseInt(document.getElementById('pg-max-tok').value) || 1024,
    stream: stream,
  };

  const msgEl = document.getElementById('pg-messages');
  msgEl.innerHTML += `<div class="msg user"><strong>You:</strong> ${esc(user)}</div>`;

  if (stream) {
    await pgSendStream(provider, req, msgEl);
  } else {
    await pgSendSync(provider, req, msgEl);
  }
}

async function pgSendSync(provider, req, msgEl) {
  try {
    const r = await api('/playground/chat?provider=' + encodeURIComponent(provider), 'POST', req);
    msgEl.innerHTML += `<div class="msg assistant"><strong>Assistant:</strong> ${esc(r.content)}</div>`;
    document.getElementById('pg-meta').innerHTML = `
      <span>Model: ${esc(r.model)}</span>
      <span>Provider: ${esc(r.provider)}</span>
      <span>Latency: ${r.latency_ms.toFixed(0)}ms</span>
      <span>Tokens: in=${r.usage?.input_tokens||0} out=${r.usage?.output_tokens||0} reasoning=${r.usage?.reasoning_tokens||0}</span>
      <span>Finish: ${esc(r.finish_reason)}</span>`;
  } catch (e) {
    msgEl.innerHTML += `<div class="msg error"><strong>Error:</strong> ${esc(e+'')}</div>`;
  }
}

async function pgSendStream(provider, req, msgEl) {
  const headers = {};
  if (authToken) headers['Authorization'] = 'Bearer ' + authToken;
  headers['Content-Type'] = 'application/json';
  const res = await fetch('/admin/api/playground/stream?provider=' + encodeURIComponent(provider), {
    method: 'POST',
    headers,
    body: JSON.stringify(req),
  });
  if (!res.ok) {
    msgEl.innerHTML += `<div class="msg error"><strong>Error:</strong> ${res.status}</div>`;
    return;
  }
  const div = document.createElement('div');
  div.className = 'msg assistant';
  div.innerHTML = '<strong>Assistant:</strong> <span class="stream-content"></span>';
  msgEl.appendChild(div);
  const span = div.querySelector('.stream-content');
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buf = '';
  let usage = null;
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    buf += decoder.decode(value, { stream: true });
    const lines = buf.split('\n');
    buf = lines.pop();
    for (const line of lines) {
      if (line.startsWith('data: ')) {
        const data = line.substring(6);
        if (data === '[DONE]') continue;
        try {
          const chunk = JSON.parse(data);
          if (chunk.content) span.textContent += chunk.content;
          if (chunk.usage) usage = chunk.usage;
        } catch (e) {}
      }
    }
  }
  if (usage) {
    document.getElementById('pg-meta').innerHTML = `
      <span>Tokens: in=${usage.input_tokens||0} out=${usage.output_tokens||0} reasoning=${usage.reasoning_tokens||0}</span>`;
  }
}

// -- mock --
async function init_mock() {
  try {
    const rules = await api('/mock/rules');
    renderMock(rules);
  } catch (e) { showError('tab-mock', e); }
}

function renderMock(rules) {
  const el = document.getElementById('tab-mock');
  let html = '<div class="toolbar">';
  html += '<button class="primary" onclick="createMockRule()">+ Add Rule</button>';
  html += '<button onclick="mockPreset(\'429\')">+ 429 Preset</button>';
  html += '<button onclick="mockPreset(\'500\')">+ 500 Preset</button>';
  html += '<button onclick="mockPreset(\'timeout\')">+ Timeout</button>';
  html += '<button onclick="mockPreset(\'empty\')">+ Empty</button>';
  html += '</div>';
  if (rules.length === 0) {
    html += '<p class="empty">No mock rules yet. Create one to start testing.</p>';
  } else {
    html += '<table><thead><tr><th>Enabled</th><th>Match Model</th><th>Action</th><th>Status</th><th>Content</th><th>Priority</th><th>Actions</th></tr></thead><tbody>';
    for (const r of rules) {
      html += `<tr>
        <td><input type="checkbox" ${r.enabled?'checked':''} onchange="toggleMock('${r.id}', this.checked)"></td>
        <td><code>${esc(r.match_model)}</code></td>
        <td>${esc(r.action)}</td>
        <td>${r.status_code||'-'}</td>
        <td class="preview">${esc((r.content||r.error_msg||'').substring(0, 60))}</td>
        <td>${r.priority}</td>
        <td class="actions">
          <button onclick="editMockRule('${r.id}')">Edit</button>
          <button class="danger" onclick="deleteMockRule('${r.id}')">Del</button>
        </td></tr>`;
    }
    html += '</tbody></table>';
  }
  el.innerHTML = html;
}

async function createMockRule() {
  const rule = {
    enabled: true,
    match_model: prompt('Match model name:'),
    action: prompt('Action (response/error/timeout/empty):', 'response'),
    priority: 1,
  };
  if (rule.action === 'error') {
    rule.status_code = parseInt(prompt('Status code:', '500'));
    rule.error_msg = prompt('Error message:', 'mock error');
  } else if (rule.action === 'response' || rule.action === 'timeout') {
    rule.content = prompt('Response content:', '{"message": "mock response"}');
  }
  if (rule.action === 'timeout') {
    rule.delay_ms = parseInt(prompt('Delay (ms):', '5000'));
  }
  if (!rule.match_model) return;
  try {
    await api('/mock/rules', 'POST', rule);
    init_mock();
  } catch (e) { alert(e); }
}

async function mockPreset(type) {
  const presets = {
    '429': { enabled: true, match_model: '', action: 'error', status_code: 429, error_msg: 'rate limit exceeded', priority: 1 },
    '500': { enabled: true, match_model: '', action: 'error', status_code: 500, error_msg: 'internal server error', priority: 1 },
    'timeout': { enabled: true, match_model: '', action: 'timeout', delay_ms: 10000, content: 'timeout response', priority: 1 },
    'empty': { enabled: true, match_model: '', action: 'empty', priority: 1 },
  };
  const rule = presets[type];
  if (!rule) return;
  rule.match_model = prompt('Match model (empty = all):', rule.match_model || '');
  try {
    await api('/mock/rules', 'POST', rule);
    init_mock();
  } catch (e) { alert(e); }
}

async function toggleMock(id, enabled) {
  try {
    await api('/mock/rules/' + id, 'PUT', { enabled });
    init_mock();
  } catch (e) { alert(e); }
}

async function editMockRule(id) {
  // simple inline edit not implemented in MVP — re-create pattern
  alert('Edit not yet supported in this view. Delete and re-create for now.');
}

async function deleteMockRule(id) {
  if (!confirm('Delete rule?')) return;
  try {
    await api('/mock/rules/' + id, 'DELETE');
    init_mock();
  } catch (e) { alert(e); }
}

// -- recent --
let recentTimer = null;

async function init_recent() {
  try {
    const entries = await api('/recent');
    renderRecent(entries);
  } catch (e) { showError('tab-recent', e); }
  if (recentTimer) clearInterval(recentTimer);
  recentTimer = setInterval(async () => {
    if (document.getElementById('tab-recent').classList.contains('active')) {
      try {
        const entries = await api('/recent');
        renderRecent(entries);
      } catch (e) {}
    }
  }, 5000);
}

function renderRecent(entries) {
  const el = document.getElementById('tab-recent');
  if (!entries || entries.length === 0) {
    el.innerHTML = '<p class="empty">No requests recorded yet. Send a request to /v1/chat to see it here.</p>';
    return;
  }
  let html = '<table><thead><tr><th>Time</th><th>Provider</th><th>Model</th><th>Status</th><th>Tokens</th><th>Latency</th><th></th></tr></thead><tbody>';
  for (const e of entries) {
    html += `<tr>
      <td>${new Date(e.time).toLocaleTimeString()}</td>
      <td>${esc(e.provider)}</td>
      <td>${esc(e.model)}</td>
      <td>${e.status}${e.error ? ' <span class="err">'+esc(e.error)+'</span>' : ''}</td>
      <td>in=${e.input_tokens} out=${e.output_tokens}</td>
      <td>${e.latency_ms.toFixed(0)}ms</td>
      <td><button onclick="viewRecent('${e.id}')">Detail</button></td></tr>`;
  }
  html += '</tbody></table>';
  el.innerHTML = html;
}

async function viewRecent(id) {
  try {
    const e = await api('/recent/' + id);
    const dlog = document.getElementById('detail-log') || document.createElement('div');
    dlog.id = 'detail-log';
    dlog.innerHTML = `<div class="detail-panel">
      <h3>Request Detail</h3>
      <button onclick="this.parentElement.remove()">Close</button>
      <div><strong>Request:</strong><pre>${esc(JSON.stringify(e.request, null, 2))}</pre></div>
      <div><strong>Response:</strong><pre>${esc(JSON.stringify(e.response, null, 2))}</pre></div>
    </div>`;
    document.body.appendChild(dlog);
  } catch (e) { alert(e); }
}

// -- config --
async function saveConfig() {
  try {
    const r = await api('/config/save', 'POST');
    alert(r.message);
  } catch (e) { alert(e); }
}

// -- util --
function esc(s) {
  if (!s) return '';
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

function showError(tabId, e) {
  document.getElementById(tabId).innerHTML = `<p class="error">${esc(e+'')}</p>`;
}

// boot: load channels tab by default
init_channels();
