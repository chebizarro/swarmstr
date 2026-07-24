  function beginManagementView(title, subtitle) {
    finalizeStreaming();
    toolCards = {};
    msgs.innerHTML = '';
    const root = document.createElement('div');
    root.className = 'management-view';
    const h = document.createElement('h2');
    h.textContent = title;
    const sub = document.createElement('div');
    sub.className = 'management-subtitle';
    sub.textContent = subtitle || '';
    const grid = document.createElement('div');
    grid.className = 'mgmt-grid';
    root.appendChild(h);
    root.appendChild(sub);
    root.appendChild(grid);
    msgs.appendChild(root);
    return { root, grid };
  }

  function addMgmtCard(grid, title) {
    const card = document.createElement('section');
    card.className = 'mgmt-card';
    const h = document.createElement('h3');
    h.textContent = title;
    card.appendChild(h);
    grid.appendChild(card);
    return card;
  }

  function addKV(card, rows) {
    const dl = document.createElement('dl');
    dl.className = 'kv';
    rows.forEach(([label, value]) => {
      const dt = document.createElement('dt');
      const dd = document.createElement('dd');
      dt.textContent = label;
      dd.textContent = value === undefined || value === null || value === '' ? '—' : (typeof value === 'object' ? formatValue(value) : String(value));
      dl.appendChild(dt);
      dl.appendChild(dd);
    });
    card.appendChild(dl);
  }

  function addRows(card, items, render) {
    const list = document.createElement('div');
    list.className = 'mgmt-list';
    if (!items || !items.length) {
      const empty = document.createElement('div');
      empty.className = 'sidebar-empty';
      empty.textContent = 'No items';
      card.appendChild(empty);
      return;
    }
    items.forEach(item => {
      const row = document.createElement('div');
      row.className = 'mgmt-row';
      render(row, item);
      list.appendChild(row);
    });
    card.appendChild(list);
  }

  async function callSafe(method, params) {
    try { return await callMethod(method, params || {}); }
    catch (err) { return { error: err && err.message ? err.message : 'request failed' }; }
  }

  function resultItems(res, key) {
    if (!res || res.error) return [];
    return res[key] || res.items || [];
  }

  function isViewCurrent(view, token) {
    return mainView === view && viewSeq === token;
  }

  async function showDashboardView(token) {
    const { grid } = beginManagementView('Dashboard', 'Connection, inventory, health, and recent daemon activity.');
    const loading = addMgmtCard(grid, 'Loading');
    loading.textContent = 'Loading dashboard…';
    const [status, sessions, agents, channels, skills, cron, logs] = await Promise.all([
      callSafe('status.get', { verbose: true }),
      callSafe('sessions.list', { limit: 20 }),
      callSafe('agents.list', {}),
      callSafe('channels.list', {}),
      callSafe('skills.status', {}),
      callSafe('cron.status', {}),
      callSafe('logs.tail', { lines: 8 }),
    ]);
    if (!isViewCurrent('dashboard', token)) return;
    const view = beginManagementView('Dashboard', 'Connection, inventory, health, and recent daemon activity.');
    addKV(addMgmtCard(view.grid, 'Gateway'), [
      ['Connection', connected ? 'connected' : 'offline'],
      ['Version', status.version],
      ['Uptime ms', status.uptime_ms || status.uptimeMs],
      ['Agent', activeAgentID],
      ['Status error', status.error],
    ]);
    addKV(addMgmtCard(view.grid, 'Inventory'), [
      ['Sessions', resultItems(sessions, 'sessions').length],
      ['Agents', resultItems(agents, 'agents').length],
      ['Channels', resultItems(channels, 'channels').length],
      ['Skills', (skills.skills || []).length],
    ]);
    addKV(addMgmtCard(view.grid, 'Schedulers'), [
      ['Cron enabled', cron.enabled],
      ['Cron jobs', Array.isArray(cron.jobs) ? cron.jobs.length : (cron.job ? 1 : 0)],
      ['Cron error', cron.error],
    ]);
    const logCard = addMgmtCard(view.grid, 'Recent logs');
    addRows(logCard, logs.lines || [], (row, line) => { row.textContent = line; });
  }


  function addActionButton(container, label, method, paramsFn, refreshFn) {
    const btn = document.createElement('button');
    btn.className = 'mini-btn';
    btn.textContent = label;
    btn.addEventListener('click', async () => {
      btn.disabled = true;
      const old = btn.textContent;
      btn.textContent = 'Working…';
      try { await callMethod(method, paramsFn ? paramsFn() : {}); btn.textContent = 'Done'; if (refreshFn) setTimeout(refreshFn, 250); }
      catch (err) { btn.textContent = (err && err.message) || 'Failed'; }
      finally { setTimeout(() => { btn.disabled = false; btn.textContent = old; }, 1200); }
    });
    container.appendChild(btn);
    return btn;
  }

  function sessionIDOf(s) { return s && (s.session_id || s.id || s.key); }
  function nodeIDOf(n) { return n && (n.id || n.node_id || n.device_id || n.name); }
  function serverIDOf(m) { return m && (m.server || m.name || m.id); }
  function skillKeyOf(k) { return k && (k.skill_key || k.key || k.name || k.id); }

  async function showAgentsView(selected, token) {
    const { grid } = beginManagementView('Agents', 'Configured agents with status, files, tools, and skills panels.');
    const [res, skillsRes] = await Promise.all([callSafe('agents.list', {}), callSafe('skills.status', {})]);
    if (token && !isViewCurrent('agents', token)) return;
    const agents = resultItems(res, 'agents');
    const agent = selected || agents.find(a => (a.id || a.agent_id) === activeAgentID) || agents[0] || {};
    addRows(addMgmtCard(grid, 'Agent roster'), agents, (row, a) => {
      row.innerHTML = `<strong>${escapeHTML(a.id || a.agent_id || 'agent')}</strong><div class="sub">model: ${escapeHTML(a.model || '—')} · tools: ${escapeHTML(a.tool_profile || a.toolProfile || 'default')}</div>`;
    });
    addKV(addMgmtCard(grid, 'Status'), [['ID', agent.id || agent.agent_id], ['Name', agent.name], ['Model', agent.model], ['Runtime', agent.runtime], ['Status', agent.status], ['Error', res.error]]);
    addRows(addMgmtCard(grid, 'Files'), agent.files || agent.open_files || agent.workspace_files || [], (row, f) => { row.textContent = typeof f === 'string' ? f : formatValue(f); });
    addRows(addMgmtCard(grid, 'Tools'), agent.tools || agent.tool_names || agent.enabled_tools || [], (row, tool) => { row.innerHTML = `<strong>${escapeHTML(tool.name || tool.id || tool)}</strong><div class="sub">${escapeHTML(tool.description || tool.source || '')}</div>`; });
    addRows(addMgmtCard(grid, 'Skills'), agent.skills || skillsRes.skills || [], (row, sk) => { row.innerHTML = `<strong>${escapeHTML(skillKeyOf(sk) || sk)}</strong><div class="sub">${escapeHTML(sk.status || sk.enabled || sk.source || '')}</div>`; });
  }

  async function showChannelsView(selected, token) {
    const { grid } = beginManagementView('Channels', 'Channel config, status actions, and Nostr relay/profile fields.');
    const [listRes, statusRes] = await Promise.all([callSafe('channels.list', {}), callSafe('channels.status', {})]);
    if (token && !isViewCurrent('channels', token)) return;
    const channels = resultItems(listRes, 'channels');
    addRows(addMgmtCard(grid, 'Connected channels'), channels, (row, c) => {
      row.innerHTML = `<strong>${escapeHTML(c.id || c.name || 'channel')}</strong><div class="sub">type: ${escapeHTML(c.type || c.kind || '—')} · status: ${escapeHTML(c.status || '—')}</div>`;
    });
    const c = selected || channels[0] || {};
    const detail = addMgmtCard(grid, 'Selected channel config/status');
    addKV(detail, [['ID', c.id || c.name], ['Type', c.type || c.kind], ['Status', c.status || statusRes.status], ['Relay URLs', c.relays || c.relay_urls || c.relayURLs], ['Nostr pubkey', c.pubkey || c.nostr_pubkey], ['Profile', c.profile || c.nostr_profile], ['Raw status', statusRes], ['Error', listRes.error || statusRes.error]]);
    const actions = document.createElement('div'); actions.className = 'action-row'; detail.appendChild(actions);
    addActionButton(actions, 'Refresh status', 'channels.status', () => ({ channel_id: c.id || c.name }), () => showChannelsView(c, ++viewSeq));
  }

  async function showSessionFilesPanel(grid, sid) {
    const previous = document.getElementById('session-files-panel');
    if (previous) previous.remove();
    const card = addMgmtCard(grid, 'Workspace files');
    card.id = 'session-files-panel';
    card.innerHTML += `<div class="sub">${escapeHTML(sid)}</div>`;
    const status = document.createElement('div'); status.className = 'sub'; status.textContent = 'Loading files…'; card.appendChild(status);
    const res = await callSafe('sessions.files.list', { sessionKey: sid });
    status.textContent = res.error ? `Files unavailable: ${res.error}` : (res.root || 'Session workspace');
    if (res.error) return;
    const browser = res.browser || {};
    const files = Array.isArray(browser.entries) ? browser.entries : (res.files || []);
    addRows(card, files, (row, file) => {
      const filePath = file.workspacePath || file.path;
      row.innerHTML = `<strong>${escapeHTML(file.name || filePath || 'file')}</strong><div class="sub">${escapeHTML(file.kind || '')}${file.sessionKind ? ` · ${escapeHTML(file.sessionKind)}` : ''}</div>`;
      if ((file.kind || '') === 'directory' || file.missing) return;
      row.style.cursor = 'pointer';
      row.addEventListener('click', async () => {
        const loaded = await callSafe('sessions.files.get', { sessionKey: sid, path: filePath });
        if (loaded.error || !loaded.file) { status.textContent = loaded.error || 'Could not read file'; return; }
        let editor = card.querySelector('.session-file-editor');
        if (editor) editor.remove();
        editor = document.createElement('div'); editor.className = 'session-file-editor';
        const textarea = document.createElement('textarea'); textarea.className = 'config-editor'; textarea.value = loaded.file.content || '';
        const actions = document.createElement('div'); actions.className = 'action-row';
        const save = document.createElement('button'); save.className = 'mini-btn'; save.textContent = 'Save'; save.disabled = !loaded.file.hash;
        save.addEventListener('click', async () => {
          save.disabled = true;
          try {
            const updated = await callMethod('sessions.files.set', { sessionKey: sid, path: filePath, content: textarea.value, expectedHash: loaded.file.hash });
            loaded.file.hash = updated.file && updated.file.hash;
            status.textContent = `Saved ${filePath}`;
          } catch (err) { status.textContent = `Save failed: ${err && err.message ? err.message : 'file changed; reload it'}`; }
          finally { save.disabled = !loaded.file.hash; }
        });
        actions.appendChild(save); editor.append(textarea, actions); card.appendChild(editor);
      });
    });
    const controls = document.createElement('div'); controls.className = 'action-row'; card.appendChild(controls);
    const reveal = document.createElement('button'); reveal.className = 'mini-btn'; reveal.textContent = 'Reveal workspace';
    reveal.addEventListener('click', async () => { const result = await callSafe('sessions.files.reveal', { key: sid }); if (result.error || result.ok === false) status.textContent = result.error || 'Could not reveal workspace'; });
    controls.appendChild(reveal);
  }

  async function showSessionSharingPanel(grid, sid) {
    const previous = document.getElementById('session-sharing-panel');
    if (previous) previous.remove();
    const card = addMgmtCard(grid, 'Sharing');
    card.id = 'session-sharing-panel';
    card.innerHTML += `<div class="sub">${escapeHTML(sid)}</div>`;
    const status = document.createElement('div'); status.className = 'sub'; status.textContent = 'Loading sharing…'; card.appendChild(status);
    const res = await callSafe('session.members.list', { sessionKey: sid });
    if (res.error) { status.textContent = `Sharing unavailable: ${res.error}`; return; }
    const ownerLabel = res.owner ? (res.owner.label || res.owner.id) : 'unassigned';
    status.textContent = `Role: ${res.role || 'owner'} · Owner: ${ownerLabel}`;
    const visRow = document.createElement('div'); visRow.className = 'action-row'; card.appendChild(visRow);
    const visibility = document.createElement('select'); visibility.className = 'mini-input';
    for (const value of (res.allowedVisibilities || ['shared', 'read-only', 'suggest', 'draft'])) {
      const option = document.createElement('option'); option.value = value; option.textContent = value; visibility.appendChild(option);
    }
    visRow.appendChild(visibility);
    const applyVisibility = document.createElement('button'); applyVisibility.className = 'mini-btn'; applyVisibility.textContent = 'Set visibility';
    applyVisibility.addEventListener('click', async () => {
      const result = await callSafe('session.visibility.set', { sessionKey: sid, visibility: visibility.value });
      status.textContent = result.error ? `Visibility failed: ${result.error}` : `Visibility set to ${result.visibility}`;
    });
    visRow.appendChild(applyVisibility);
    addRows(card, res.members || [], (row, member) => {
      row.innerHTML = `<strong>${escapeHTML(member.identityId)}</strong><div class="sub">added by ${escapeHTML(member.addedBy || 'unknown')}</div>`;
      const actions = document.createElement('div'); actions.className = 'action-row'; row.appendChild(actions);
      addActionButton(actions, 'Remove', 'session.members.remove', () => ({ sessionKey: sid, identityId: member.identityId }), () => showSessionSharingPanel(grid, sid));
    });
    const addRow = document.createElement('div'); addRow.className = 'action-row'; card.appendChild(addRow);
    const identity = document.createElement('input'); identity.className = 'mini-input'; identity.placeholder = 'identity id'; addRow.appendChild(identity);
    const addMember = document.createElement('button'); addMember.className = 'mini-btn'; addMember.textContent = 'Add member';
    addMember.addEventListener('click', async () => {
      if (!identity.value.trim()) return;
      const result = await callSafe('session.members.add', { sessionKey: sid, identityId: identity.value.trim() });
      if (result.error) { status.textContent = `Add failed: ${result.error}`; return; }
      showSessionSharingPanel(grid, sid);
    });
    addRow.appendChild(addMember);
    const suggestionsRes = await callSafe('session.suggestions.list', { sessionKey: sid });
    const suggestHeader = document.createElement('div'); suggestHeader.className = 'sub'; card.appendChild(suggestHeader);
    if (suggestionsRes.error) {
      suggestHeader.textContent = `Suggestions unavailable: ${suggestionsRes.error}`;
    } else {
      const suggestions = suggestionsRes.suggestions || [];
      suggestHeader.textContent = `Suggestions (${suggestions.length})`;
      addRows(card, suggestions, (row, suggestion) => {
        row.innerHTML = `<strong>${escapeHTML(truncate(suggestion.text, 80))}</strong><div class="sub">${escapeHTML(suggestion.author && (suggestion.author.label || suggestion.author.id) || '')} · ${escapeHTML(suggestion.state)}</div>`;
        if (suggestion.state !== 'pending') return;
        const rowActions = document.createElement('div'); rowActions.className = 'action-row'; row.appendChild(rowActions);
        addActionButton(rowActions, 'Send', 'session.suggestions.resolve', () => ({ sessionKey: sid, id: suggestion.id, resolution: 'send' }), () => showSessionSharingPanel(grid, sid));
        addActionButton(rowActions, 'Dismiss', 'session.suggestions.resolve', () => ({ sessionKey: sid, id: suggestion.id, resolution: 'dismiss' }), () => showSessionSharingPanel(grid, sid));
      });
    }
    const suggestRow = document.createElement('div'); suggestRow.className = 'action-row'; card.appendChild(suggestRow);
    const suggestionText = document.createElement('input'); suggestionText.className = 'mini-input'; suggestionText.placeholder = 'suggest a message'; suggestRow.appendChild(suggestionText);
    const addSuggestion = document.createElement('button'); addSuggestion.className = 'mini-btn'; addSuggestion.textContent = 'Suggest';
    addSuggestion.addEventListener('click', async () => {
      if (!suggestionText.value.trim()) return;
      const result = await callSafe('session.suggestions.add', { sessionKey: sid, text: suggestionText.value.trim() });
      if (result.error) { status.textContent = `Suggestion failed: ${result.error}`; return; }
      showSessionSharingPanel(grid, sid);
    });
    suggestRow.appendChild(addSuggestion);
    const discussion = await callSafe('session.discussion.info', { sessionKey: sid });
    const discussionRow = document.createElement('div'); discussionRow.className = 'action-row'; card.appendChild(discussionRow);
    const discussionLabel = document.createElement('span'); discussionLabel.className = 'sub';
    discussionLabel.textContent = discussion.error ? `Discussion unavailable: ${discussion.error}` : `Discussion: ${discussion.state || 'none'}`;
    discussionRow.appendChild(discussionLabel);
    if (!discussion.error && discussion.state && discussion.state !== 'none') {
      addActionButton(discussionRow, 'Open discussion', 'session.discussion.open', () => ({ sessionKey: sid }), () => showSessionSharingPanel(grid, sid));
    }
    const askRow = document.createElement('div'); askRow.className = 'action-row'; card.appendChild(askRow);
    const askInput = document.createElement('input'); askInput.className = 'mini-input'; askInput.placeholder = 'ask the session observer'; askRow.appendChild(askInput);
    const askButton = document.createElement('button'); askButton.className = 'mini-btn'; askButton.textContent = 'Ask observer';
    askButton.addEventListener('click', async () => {
      const question = askInput.value.trim();
      if (!question) return;
      askButton.disabled = true;
      const result = await callSafe('sessions.observer.ask', { sessionKey: sid, question });
      askButton.disabled = false;
      status.textContent = result.error ? `Observer: ${result.error}` : `Observer: ${result.answer || ''}`;
    });
    askRow.appendChild(askButton);
  }

  async function showSessionsView(token) {
    const { grid } = beginManagementView('Sessions', 'List, detail, delete, export, reset, compact, and prune sessions.');
    const res = await callSafe('sessions.list', { limit: 100 });
    if (!isViewCurrent('sessions', token)) return;
    const sessions = resultItems(res, 'sessions');
    const card = addMgmtCard(grid, 'Sessions');
    addRows(card, sessions, (row, sess) => {
      const sid = sessionIDOf(sess);
      row.innerHTML = `<strong>${escapeHTML(truncate(sid, 42))}</strong><div class="sub">${escapeHTML(sess.preview || sess.title || sess.updated_at || '')}</div>`;
      const actions = document.createElement('div'); actions.className = 'action-row'; row.appendChild(actions);
      addActionButton(actions, 'Open', 'sessions.preview', () => ({ session_id: sid }), async () => { sessionID = sid; localStorage.setItem('metiq_session', sid); mainView='chat'; loadSessionHistory(sid); });
      const filesButton = document.createElement('button'); filesButton.className = 'mini-btn'; filesButton.textContent = 'Files'; filesButton.addEventListener('click', () => showSessionFilesPanel(grid, sid)); actions.appendChild(filesButton);
      const sharingButton = document.createElement('button'); sharingButton.className = 'mini-btn'; sharingButton.textContent = 'Sharing'; sharingButton.addEventListener('click', () => showSessionSharingPanel(grid, sid)); actions.appendChild(sharingButton);
      addActionButton(actions, 'Export', 'sessions.export', () => ({ session_id: sid }));
      addActionButton(actions, 'Reset', 'sessions.reset', () => ({ session_id: sid }), () => showSessionsView(++viewSeq));
      addActionButton(actions, 'Delete', 'sessions.delete', () => ({ session_id: sid }), () => { loadSessions(); showSessionsView(++viewSeq); });
    });
    const prune = addMgmtCard(grid, 'Prune'); const actions = document.createElement('div'); actions.className='action-row'; prune.appendChild(actions);
    addActionButton(actions, 'Prune all empty/old', 'sessions.prune', () => ({ all: true }), () => { loadSessions(); showSessionsView(++viewSeq); });
  }

  async function showCronView(token) {
    const { grid } = beginManagementView('Cron', 'Scheduled jobs: list, add, run, and remove.');
    const [listRes, runsRes] = await Promise.all([callSafe('cron.list', {}), callSafe('cron.runs', {})]);
    if (!isViewCurrent('cron', token)) return;
    const jobs = resultItems(listRes, 'jobs');
    const add = addMgmtCard(grid, 'Add job');
    const schedule = document.createElement('input'); schedule.className='mini-input'; schedule.placeholder='*/15 * * * *';
    const method = document.createElement('input'); method.className='mini-input'; method.placeholder='status.get';
    const actions = document.createElement('div'); actions.className='action-row'; actions.append(schedule, method); add.appendChild(actions);
    addActionButton(actions, 'Add', 'cron.add', () => ({ schedule: schedule.value, method: method.value, params: {} }), () => showCronView(++viewSeq));
    addRows(addMgmtCard(grid, 'Jobs'), jobs, (row, job) => {
      const id = job.id || job.job_id; row.innerHTML = `<strong>${escapeHTML(id || 'job')}</strong><div class="sub">${escapeHTML(job.schedule || '')} → ${escapeHTML(job.method || '')}</div>`;
      const a=document.createElement('div'); a.className='action-row'; row.appendChild(a);
      addActionButton(a, 'Run now', 'cron.run', () => ({ id }), () => showCronView(++viewSeq));
      addActionButton(a, 'Remove', 'cron.remove', () => ({ id }), () => showCronView(++viewSeq));
    });
    addRows(addMgmtCard(grid, 'Recent runs'), resultItems(runsRes, 'runs'), (row, run) => { row.textContent = formatValue(run); });
  }

  function renderNodeInvokeProgress() {
    const card = document.getElementById('node-invoke-progress');
    if (!card) return;
    card.innerHTML = '';
    const entries = Array.from(nodeInvokeProgress.values()).slice(-20);
    addRows(card, entries, (row, progress) => {
      row.innerHTML = `<strong>${escapeHTML(progress.nodeID || 'node')}</strong><div class="sub">#${progress.seq}: ${escapeHTML(truncate(progress.text, 180))}</div>`;
    });
  }

  async function showNodesView(token) {
    const { grid } = beginManagementView('Nodes', 'Node status, invoke, and rename operations.');
    const listRes = await callSafe('node.list', {});
    if (!isViewCurrent('nodes', token)) return;
    const nodes = resultItems(listRes, 'nodes');
    const firstNodeID = nodeIDOf(nodes[0]);
    const statusRes = firstNodeID ? await callSafe('node.describe', { node_id: firstNodeID }) : {};
    if (!isViewCurrent('nodes', token)) return;
    addRows(addMgmtCard(grid, 'Nodes'), nodes, (row, n) => {
      const id=nodeIDOf(n); row.innerHTML = `<strong>${escapeHTML(id || 'node')}</strong><div class="sub">${escapeHTML(n.status || n.kind || '')}</div>`;
      const a=document.createElement('div'); a.className='action-row'; row.appendChild(a);
      addActionButton(a, 'Status', 'node.describe', () => ({ node_id: id }));
      addActionButton(a, 'Invoke status', 'node.invoke', () => ({ node_id: id, command: 'status.get', args: {} }));
      addActionButton(a, 'Rename', 'node.rename', () => ({ node_id: id, name: prompt('New node name', n.name || id) || n.name || id }), () => showNodesView(++viewSeq));
      addActionButton(a, 'Remove', 'node.pair.remove', () => ({ node_id: id }), () => showNodesView(++viewSeq));
    });
    addKV(addMgmtCard(grid, 'Status'), Object.keys(statusRes).sort().map(k => [k, statusRes[k]]));
    const progress = addMgmtCard(grid, 'Invocation progress'); progress.id = 'node-invoke-progress'; renderNodeInvokeProgress();
  }

  async function showMCPView(token) {
    const { grid } = beginManagementView('MCP', 'MCP servers: list, test, auth, and reconnect.');
    const res = await callSafe('mcp.list', {});
    if (!isViewCurrent('mcp', token)) return;
    addRows(addMgmtCard(grid, 'Servers'), resultItems(res, 'servers'), (row, m) => {
      const server=serverIDOf(m); row.innerHTML = `<strong>${escapeHTML(server || 'server')}</strong><div class="sub">${escapeHTML(m.status || m.command || m.url || '')}</div>`;
      const a=document.createElement('div'); a.className='action-row'; row.appendChild(a);
      addActionButton(a, 'Test', 'mcp.test', () => ({ server }));
      addActionButton(a, 'Auth', 'mcp.auth.start', () => ({ server }));
      addActionButton(a, 'Reconnect', 'mcp.reconnect', () => ({ server }), () => showMCPView(++viewSeq));
    });
  }

  async function showSkillsView(token) {
    const { grid } = beginManagementView('Skills', 'Skills: list, check status, install, enable, and disable.');
    const [statusRes, binsRes] = await Promise.all([callSafe('skills.status', {}), callSafe('skills.bins', {})]);
    if (!isViewCurrent('skills', token)) return;
    addRows(addMgmtCard(grid, 'Installed skills'), statusRes.skills || statusRes.items || [], (row, sk) => {
      const key=skillKeyOf(sk); row.innerHTML = `<strong>${escapeHTML(key || 'skill')}</strong><div class="sub">${escapeHTML(sk.status || sk.enabled || sk.description || '')}</div>`;
      const a=document.createElement('div'); a.className='action-row'; row.appendChild(a);
      addActionButton(a, 'Check', 'skills.status', () => ({ skill_key: key }));
      addActionButton(a, 'Enable', 'skills.update', () => ({ skill_key: key, enabled: true }), () => showSkillsView(++viewSeq));
      addActionButton(a, 'Disable', 'skills.update', () => ({ skill_key: key, enabled: false }), () => showSkillsView(++viewSeq));
    });
    const install=addMgmtCard(grid, 'Install'); const name=document.createElement('input'); name.className='mini-input'; name.placeholder='skill name'; const a=document.createElement('div'); a.className='action-row'; a.appendChild(name); install.appendChild(a);
    addActionButton(a, 'Install', 'skills.install', () => ({ name: name.value, install_id: 'webui-' + Date.now() }), () => showSkillsView(++viewSeq));
    addRows(addMgmtCard(grid, 'Available bins/catalog'), binsRes.bins || binsRes.skills || [], (row, b) => { row.textContent = formatValue(b); });
  }

  async function showConfigView(token) {
    const { grid } = beginManagementView('Config', 'Schema-guided configuration view with guarded JSON save.');
    const [cfgRes, schemaRes] = await Promise.all([callSafe('config.get', {}), callSafe('config.schema', {})]);
    if (!isViewCurrent('config', token)) return;
    const editorCard = addMgmtCard(grid, 'Config JSON');
    const editor = document.createElement('textarea');
    editor.className = 'config-editor';
    const configHash = cfgRes.base_hash || cfgRes.hash;
    const canSaveConfig = !!(cfgRes && !cfgRes.error && cfgRes.config && configHash);
    editor.value = canSaveConfig ? formatValue(cfgRes.config) : (cfgRes.error ? 'Could not load config: ' + cfgRes.error : 'Config unavailable; save disabled.');
    editor.readOnly = !canSaveConfig;
    const status = document.createElement('div');
    if (!canSaveConfig) {
      status.className = 'mgmt-error';
      status.textContent = 'Config save disabled until config.get returns a config and base hash.';
    }
    const actions = document.createElement('div');
    actions.className = 'action-row';
    const save = document.createElement('button');
    save.className = 'mini-btn';
    save.textContent = 'Save config';
    save.disabled = !canSaveConfig;
    save.addEventListener('click', async () => {
      status.className = '';
      status.textContent = 'Saving…';
      try {
        if (!canSaveConfig) throw new Error('config.get did not return a saveable config');
        const parsed = JSON.parse(editor.value || '{}');
        const res = await callMethod('config.put', { config: parsed, baseHash: configHash });
        status.className = 'mgmt-ok';
        status.textContent = res.restart_pending ? 'Saved. Restart pending.' : 'Saved.';
      } catch (err) {
        status.className = 'mgmt-error';
        status.textContent = err && err.message ? err.message : 'Could not save config';
      }
    });
    actions.appendChild(save);
    editorCard.appendChild(editor);
    editorCard.appendChild(actions);
    editorCard.appendChild(status);
    const schemaCard = addMgmtCard(grid, 'Schema fields');
    addRows(schemaCard, (schemaRes.fields || []).slice(0, 80), (row, field) => { row.textContent = field; });
  }

  async function showUsageView(token) {
    const { grid } = beginManagementView('Usage', 'Token, tool, delegation, and cost metrics.');
    const [status, cost] = await Promise.all([callSafe('usage.status', {}), callSafe('usage.cost', {})]);
    if (!isViewCurrent('usage', token)) return;
    addKV(addMgmtCard(grid, 'Usage status'), Object.keys(status).sort().map(k => [k, status[k]]));
    addKV(addMgmtCard(grid, 'Cost'), Object.keys(cost).sort().map(k => [k, cost[k]]));
  }

  function openManagementView(view) {
    closeSidebarOnMobile();
    mainView = view;
    const token = ++viewSeq;
    if (view === 'sessions') return showSessionsView(token);
    if (view === 'cron') return showCronView(token);
    if (view === 'nodes') return showNodesView(token);
    if (view === 'mcp') return showMCPView(token);
    if (view === 'skills') return showSkillsView(token);
    if (view === 'dashboard') return showDashboardView(token);
    if (view === 'agents') return showAgentsView(null, token);
    if (view === 'channels') return showChannelsView(null, token);
    if (view === 'config') return showConfigView(token);
    if (view === 'usage') return showUsageView(token);
  }

