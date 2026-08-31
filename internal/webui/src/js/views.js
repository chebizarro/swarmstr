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

  function gatewayMethodAdvertised(method) {
    return gatewayMethodDescriptors.has(method);
  }

  function gatewayEventAdvertised(event) {
    return gatewayAdvertisedEvents.has(event);
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

  function historyMutationError(err) {
    const message = err && err.message ? err.message : 'request failed';
    if (/revision|conflict|stale|compare.?and.?swap|\bcas\b/i.test(message)) {
      return 'Revision conflict: the session changed before this operation committed. Refresh the timeline and choose again. Gateway detail: ' + message;
    }
    return message;
  }

  async function reloadSessionHistorySurface(grid, sid) {
    await loadSessions();
    if (mainView === 'sessions' && document.body.contains(grid)) {
      // The panel refetches both chat.history and sessions.branches.list, so the
      // transcript and active branch update together without leaving this view.
      await showSessionHistoryPanel(grid, sid);
    } else if (sid === sessionID) {
      await loadSessionHistory(sid);
    }
  }

  async function runHistoryMutation(grid, sid, status, method, params, confirmation) {
    if (confirmation && !confirm(confirmation)) return;
    status.className = 'sub';
    status.textContent = 'Working…';
    try {
      const result = await callSafe(method, params);
      if (result.error) throw { message: result.error };
      status.className = 'mgmt-ok';
      status.textContent = 'Completed ' + method + (result && result.key && result.key !== sid ? ' → ' + result.key : '') + '.';
      await reloadSessionHistorySurface(grid, sid);
    } catch (err) {
      status.className = 'mgmt-error';
      status.textContent = historyMutationError(err);
    }
  }

  function appendTimelineMeta(row, text) {
    const meta = document.createElement('div');
    meta.className = 'sub';
    meta.textContent = text;
    row.appendChild(meta);
  }

  async function showSessionHistoryPanel(grid, sid) {
    const previous = document.getElementById('session-history-panel');
    if (previous) previous.remove();
    const card = addMgmtCard(grid, 'Checkpoint / rewind timeline');
    card.id = 'session-history-panel';
    card.classList.add('mgmt-card-wide');
    const status = document.createElement('div');
    status.className = 'sub';
    status.textContent = 'Loading checkpoints, branches, and transcript…';
    card.appendChild(status);

    const canCheckpoints = gatewayMethodAdvertised('sessions.compaction.list');
    const canBranches = gatewayMethodAdvertised('sessions.branches.list');
    const [checkpointRes, branchRes, historyRes] = await Promise.all([
      canCheckpoints ? callSafe('sessions.compaction.list', { key: sid }) : Promise.resolve({ error: 'sessions.compaction.list is not advertised' }),
      canBranches ? callSafe('sessions.branches.list', { sessionKey: sid }) : Promise.resolve({ error: 'sessions.branches.list is not advertised' }),
      callSafe('chat.history', { session_id: sid, limit: 200 }),
    ]);
    if (!document.body.contains(card)) return;
    status.textContent = sid;

    const checkpoints = checkpointRes.checkpoints || [];
    const checkpointSection = document.createElement('div');
    checkpointSection.className = 'timeline-section';
    const checkpointTitle = document.createElement('h4');
    checkpointTitle.textContent = `Compaction checkpoints (${checkpoints.length})`;
    checkpointSection.appendChild(checkpointTitle);
    if (checkpointRes.error) {
      const error = document.createElement('div'); error.className = 'mgmt-error'; error.textContent = checkpointRes.error; checkpointSection.appendChild(error);
    } else if (!checkpoints.length) {
      const empty = document.createElement('div'); empty.className = 'sidebar-empty'; empty.textContent = 'No persisted checkpoints for this session.'; checkpointSection.appendChild(empty);
    }
    checkpoints.forEach(checkpoint => {
      const checkpointID = checkpoint.checkpointId || checkpoint.checkpoint_id;
      const row = document.createElement('div');
      row.className = 'timeline-node checkpoint-node';
      const title = document.createElement('strong');
      title.textContent = checkpoint.reason || 'Compaction checkpoint';
      row.appendChild(title);
      const created = checkpoint.createdAt || checkpoint.created_at;
      appendTimelineMeta(row, [created ? new Date(created).toLocaleString() : '', checkpointID, checkpoint.tokensBefore ? `${checkpoint.tokensBefore} → ${checkpoint.tokensAfter || '?'} tokens` : ''].filter(Boolean).join(' · '));
      if (checkpoint.summary) appendTimelineMeta(row, truncate(checkpoint.summary, 220));
      const actions = document.createElement('div'); actions.className = 'action-row'; row.appendChild(actions);
      if (gatewayMethodAdvertised('sessions.compaction.get')) {
        const details = document.createElement('button'); details.className = 'mini-btn'; details.textContent = 'Details';
        details.addEventListener('click', async () => {
          const got = await callSafe('sessions.compaction.get', { key: sid, checkpointId: checkpointID });
          let pre = row.querySelector('pre');
          if (!pre) { pre = document.createElement('pre'); pre.className = 'grant-detail'; row.appendChild(pre); }
          pre.textContent = got.error ? got.error : formatValue(got.checkpoint || got);
        });
        actions.appendChild(details);
      }
      if (gatewayMethodAdvertised('sessions.compaction.branch')) {
        const branch = document.createElement('button'); branch.className = 'mini-btn'; branch.textContent = 'Branch here';
        branch.addEventListener('click', () => runHistoryMutation(grid, sid, status, 'sessions.compaction.branch', { key: sid, checkpointId: checkpointID }));
        actions.appendChild(branch);
      }
      if (gatewayMethodAdvertised('sessions.compaction.restore')) {
        const restore = document.createElement('button'); restore.className = 'mini-btn danger'; restore.textContent = 'Restore';
        restore.addEventListener('click', () => runHistoryMutation(grid, sid, status, 'sessions.compaction.restore', { key: sid, checkpointId: checkpointID }, 'Restore this checkpoint? The active transcript path will change.'));
        actions.appendChild(restore);
      }
      checkpointSection.appendChild(row);
    });
    card.appendChild(checkpointSection);

    const branches = branchRes.branches || [];
    const branchSection = document.createElement('div'); branchSection.className = 'timeline-section';
    const branchTitle = document.createElement('h4'); branchTitle.textContent = `Branches (${branches.length})`; branchSection.appendChild(branchTitle);
    if (branchRes.error) {
      const error = document.createElement('div'); error.className = 'mgmt-error'; error.textContent = branchRes.error; branchSection.appendChild(error);
    }
    branches.forEach(branch => {
      const leafID = branch.leafEntryId || branch.leaf_entry_id;
      const row = document.createElement('div'); row.className = 'timeline-node branch-node' + (branch.active ? ' active' : '');
      const title = document.createElement('strong'); title.textContent = (branch.active ? 'Active · ' : '') + (branch.headline || 'Empty branch'); row.appendChild(title);
      appendTimelineMeta(row, [leafID, branch.messageCount !== undefined ? `${branch.messageCount} messages` : '', branch.updatedAt || ''].filter(Boolean).join(' · '));
      if (!branch.active && gatewayMethodAdvertised('sessions.branches.switch')) {
        const actions = document.createElement('div'); actions.className = 'action-row';
        const activate = document.createElement('button'); activate.className = 'mini-btn'; activate.textContent = 'Switch branch';
        activate.addEventListener('click', () => runHistoryMutation(grid, sid, status, 'sessions.branches.switch', { sessionKey: sid, leafEntryId: leafID }));
        actions.appendChild(activate); row.appendChild(actions);
      }
      branchSection.appendChild(row);
    });
    card.appendChild(branchSection);

    const entries = historyRes.entries || historyRes.items || historyRes.transcript || [];
    const transcriptSection = document.createElement('div'); transcriptSection.className = 'timeline-section';
    const transcriptTitle = document.createElement('h4'); transcriptTitle.textContent = `Active transcript (${entries.length} entries)`; transcriptSection.appendChild(transcriptTitle);
    if (historyRes.error) {
      const error = document.createElement('div'); error.className = 'mgmt-error'; error.textContent = historyRes.error; transcriptSection.appendChild(error);
    }
    entries.forEach(entry => {
      const entryID = entry.entry_id || entry.entryId || entry.id;
      const text = entry.text || entry.content || entry.message || '';
      const row = document.createElement('div'); row.className = 'timeline-node transcript-node ' + (entry.role || 'unknown');
      const title = document.createElement('strong'); title.textContent = `${entry.role || 'entry'} · ${truncate(text, 140) || '(no text)'}`; row.appendChild(title);
      appendTimelineMeta(row, [entryID, entry.parent_entry_id || entry.parentEntryId, entry.unix || entry.created_at || entry.createdAt].filter(Boolean).join(' · '));
      if (entry.role === 'user' && entryID && gatewayMethodAdvertised('sessions.rewind')) {
        const actions = document.createElement('div'); actions.className = 'action-row';
        const rewind = document.createElement('button'); rewind.className = 'mini-btn danger'; rewind.textContent = 'Rewind before message';
        rewind.addEventListener('click', () => runHistoryMutation(grid, sid, status, 'sessions.rewind', { sessionKey: sid, entryId: entryID }, 'Rewind before this user message? The active transcript path will change and the message will return to the editor.'));
        actions.appendChild(rewind); row.appendChild(actions);
      }
      transcriptSection.appendChild(row);
    });
    card.appendChild(transcriptSection);
  }

  async function showSessionOwnershipPanel(grid, sid) {
    const previous = document.getElementById('session-ownership-panel');
    if (previous) previous.remove();
    const card = addMgmtCard(grid, 'Ownership / recovery');
    card.id = 'session-ownership-panel';
    card.classList.add('mgmt-card-wide');
    const supported = ['sessions.get', 'sessions.resolve', 'sessions.dispatch', 'sessions.reclaim', 'sessions.recover'].filter(gatewayMethodAdvertised);
    const status = document.createElement('div'); status.className = 'sub';
    status.textContent = supported.length ? `Advertised methods: ${supported.join(', ')}` : 'This gateway does not advertise ownership/recovery methods.';
    card.appendChild(status);
    if (!supported.length) return;
    const detail = document.createElement('pre'); detail.className = 'grant-detail'; detail.style.display = 'none'; card.appendChild(detail);
    const showResult = (label, result) => {
      detail.style.display = '';
      detail.textContent = label + '\n' + formatValue(result);
    };
    if (gatewayMethodAdvertised('sessions.get')) {
      const result = await callSafe('sessions.get', { key: sid, limit: 20 });
      showResult(result.error ? 'Session details unavailable' : 'Session details', result);
    }
    const actions = document.createElement('div'); actions.className = 'action-row'; card.appendChild(actions);
    if (gatewayMethodAdvertised('sessions.resolve')) {
      const resolve = document.createElement('button'); resolve.className = 'mini-btn'; resolve.textContent = 'Resolve identity';
      resolve.addEventListener('click', async () => { resolve.disabled = true; const result = await callSafe('sessions.resolve', { key: sid, allowMissing: true }); resolve.disabled = false; showResult(result.error ? 'Resolve failed' : 'Resolved session', result); });
      actions.appendChild(resolve);
    }
    if (gatewayMethodAdvertised('sessions.recover')) {
      const recover = document.createElement('button'); recover.className = 'mini-btn'; recover.textContent = 'Recover';
      recover.addEventListener('click', async () => { recover.disabled = true; const result = await callSafe('sessions.recover', { key: sid }); recover.disabled = false; showResult(result.error ? 'Recovery failed' : 'Recovery result', result); if (!result.error) await loadSessions(); });
      actions.appendChild(recover);
    }
    if (gatewayMethodAdvertised('sessions.reclaim')) {
      const reclaim = document.createElement('button'); reclaim.className = 'mini-btn danger'; reclaim.textContent = 'Reclaim locally';
      reclaim.addEventListener('click', async () => { if (!confirm('Force-reclaim ownership of this session for the current connection?')) return; reclaim.disabled = true; const result = await callSafe('sessions.reclaim', { key: sid }); reclaim.disabled = false; showResult(result.error ? 'Reclaim failed' : 'Reclaim result', result); if (!result.error) await loadSessions(); });
      actions.appendChild(reclaim);
    }
    if (gatewayMethodAdvertised('sessions.dispatch')) {
      const backend = document.createElement('input'); backend.className = 'mini-input'; backend.placeholder = 'backend / profile id'; actions.appendChild(backend);
      const dispatch = document.createElement('button'); dispatch.className = 'mini-btn'; dispatch.textContent = 'Dispatch';
      dispatch.addEventListener('click', async () => {
        const target = backend.value.trim();
        if (!target) { status.className = 'mgmt-error'; status.textContent = 'A backend or profile id is required.'; return; }
        dispatch.disabled = true; const result = await callSafe('sessions.dispatch', { key: sid, backend: target }); dispatch.disabled = false;
        showResult(result.error ? 'Dispatch failed' : 'Dispatch result', result); if (!result.error) await loadSessions();
      });
      actions.appendChild(dispatch);
    }
  }

  function handleSessionOrchestrationEvent(event, payload) {
    if (event === 'session.tool') return;
    loadSessions();
    if (mainView === 'sessions') showSessionsView(++viewSeq);
  }

  async function showSessionsView(token) {
    const { grid } = beginManagementView('Sessions', 'Session inventory, checkpoint DAG/rewind controls, and feature-detected ownership recovery.');
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
      const historyButton = document.createElement('button'); historyButton.className = 'mini-btn'; historyButton.textContent = 'Timeline'; historyButton.addEventListener('click', () => showSessionHistoryPanel(grid, sid)); actions.appendChild(historyButton);
      const ownershipButton = document.createElement('button'); ownershipButton.className = 'mini-btn'; ownershipButton.textContent = 'Ownership'; ownershipButton.addEventListener('click', () => showSessionOwnershipPanel(grid, sid)); actions.appendChild(ownershipButton);
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

  // ── Conversations view: channel conversation targets, send + blocking turn ─
  async function showConversationsView(token) {
    const { grid } = beginManagementView('Conversations', 'Channel conversation targets: list, send a message, or run a blocking turn.');
    if (!isViewCurrent('conversations', token)) return;
    const filters = addMgmtCard(grid, 'Filters');
    const filterRow = document.createElement('div'); filterRow.className = 'action-row';
    const agentInput = document.createElement('input'); agentInput.className = 'mini-input'; agentInput.placeholder = 'agent id'; agentInput.value = activeAgentID || 'main';
    const channelInput = document.createElement('input'); channelInput.className = 'mini-input'; channelInput.placeholder = 'channel filter (optional)';
    const refreshBtn = document.createElement('button'); refreshBtn.className = 'mini-btn'; refreshBtn.textContent = 'Refresh';
    filterRow.append(agentInput, channelInput, refreshBtn); filters.appendChild(filterRow);

    const listCard = addMgmtCard(grid, 'Conversations');
    const listHost = document.createElement('div'); listHost.className = 'mgmt-list-host'; listCard.appendChild(listHost);

    const composeCard = addMgmtCard(grid, 'Send / Turn');
    const refInput = document.createElement('input'); refInput.className = 'mini-input'; refInput.placeholder = 'conversationRef';
    const message = document.createElement('textarea'); message.className = 'config-editor'; message.placeholder = 'message';
    const timeoutInput = document.createElement('input'); timeoutInput.className = 'mini-input'; timeoutInput.placeholder = 'turn timeout ms (default 30000)';
    const composeActions = document.createElement('div'); composeActions.className = 'action-row';
    const status = document.createElement('div'); status.className = 'sub';
    composeCard.append(refInput, message, timeoutInput, composeActions, status);

    async function loadConversations() {
      listHost.innerHTML = '<div class="sidebar-empty">Loading…</div>';
      const params = { agent_id: agentInput.value.trim() || 'main', limit: 100 };
      const channel = channelInput.value.trim(); if (channel) params.channel = channel;
      const res = await callSafe('conversations.list', params);
      listHost.innerHTML = '';
      if (res.error) { listHost.innerHTML = `<div class="sidebar-empty">${escapeHTML(res.error)}</div>`; return; }
      const conversations = res.conversations || [];
      if (!conversations.length) { listHost.innerHTML = '<div class="sidebar-empty">No conversations observed yet</div>'; return; }
      conversations.forEach(conv => {
        const row = document.createElement('div'); row.className = 'mgmt-row';
        row.innerHTML = `<strong>${escapeHTML(conv.label || conv.target || conv.conversationRef)}</strong><div class="sub">${escapeHTML(conv.channel || '')} · ${escapeHTML(conv.kind || '')} · ${escapeHTML(truncate(conv.conversationRef, 40))}</div>`;
        const actions = document.createElement('div'); actions.className = 'action-row'; row.appendChild(actions);
        const select = document.createElement('button'); select.className = 'mini-btn'; select.textContent = 'Select';
        select.addEventListener('click', () => { refInput.value = conv.conversationRef; status.textContent = 'Selected ' + truncate(conv.conversationRef, 48); });
        actions.appendChild(select);
        listHost.appendChild(row);
      });
    }

    const sendBtn = document.createElement('button'); sendBtn.className = 'mini-btn'; sendBtn.textContent = 'Send';
    sendBtn.addEventListener('click', async () => {
      const ref = refInput.value.trim(); const msg = message.value.trim();
      if (!ref || !msg) { status.textContent = 'conversationRef and message are required'; return; }
      sendBtn.disabled = true; status.textContent = 'Sending…';
      const res = await callSafe('conversations.send', { agent_id: agentInput.value.trim() || 'main', operationId: 'webui-send-' + Date.now(), conversationRef: ref, message: msg });
      sendBtn.disabled = false;
      status.textContent = res.error ? ('Send failed: ' + res.error) : ('Sent · ' + (res.status || 'ok') + (res.messageId ? ' · ' + res.messageId : ''));
    });
    const turnBtn = document.createElement('button'); turnBtn.className = 'mini-btn'; turnBtn.textContent = 'Turn (blocking)';
    turnBtn.addEventListener('click', async () => {
      const ref = refInput.value.trim(); const msg = message.value.trim();
      if (!ref || !msg) { status.textContent = 'conversationRef and message are required'; return; }
      let ms = parseInt(timeoutInput.value, 10); if (!(ms > 0)) ms = 30000; ms = Math.min(ms, 300000);
      turnBtn.disabled = true; status.textContent = 'Waiting for reply…';
      const res = await callSafe('conversations.turn', { agent_id: agentInput.value.trim() || 'main', turnId: 'webui-turn-' + Date.now(), conversationRef: ref, message: msg, timeout_ms: ms });
      turnBtn.disabled = false;
      if (res.error) { status.textContent = 'Turn failed: ' + res.error; return; }
      const reply = res.reply && res.reply.text ? res.reply.text : '';
      status.textContent = 'Turn ' + (res.status || '') + (reply ? (' · reply: ' + truncate(reply, 200)) : '');
    });
    composeActions.append(sendBtn, turnBtn);

    refreshBtn.addEventListener('click', loadConversations);
    await loadConversations();
  }

  // ── Artifacts view: workspace content-addressed store + blob download ──────
  function base64ToBytes(b64) {
    const binary = atob(b64 || '');
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
    return bytes;
  }

  function triggerBrowserDownload(filename, mimeType, bytes) {
    const blob = new Blob([bytes], { type: mimeType || 'application/octet-stream' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url; a.download = filename || 'artifact';
    document.body.appendChild(a); a.click(); a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 4000);
  }

  async function showArtifactsView(token) {
    const { grid } = beginManagementView('Artifacts', 'Workspace artifacts: list, inspect metadata, and download inline bytes via a blob URL.');
    if (!isViewCurrent('artifacts', token)) return;
    const filters = addMgmtCard(grid, 'Filters');
    const frow = document.createElement('div'); frow.className = 'action-row';
    const sessionInput = document.createElement('input'); sessionInput.className = 'mini-input'; sessionInput.placeholder = 'session key (optional)'; if (sessionID) sessionInput.value = sessionID;
    const refreshBtn = document.createElement('button'); refreshBtn.className = 'mini-btn'; refreshBtn.textContent = 'Refresh';
    frow.append(sessionInput, refreshBtn); filters.appendChild(frow);

    const listCard = addMgmtCard(grid, 'Artifacts');
    const host = document.createElement('div'); host.className = 'mgmt-list-host'; listCard.appendChild(host);
    const detail = document.createElement('pre'); detail.className = 'grant-detail'; detail.style.display = 'none'; listCard.appendChild(detail);

    function scopeParams(base) { const sk = sessionInput.value.trim(); if (sk) base.sessionKey = sk; return base; }

    async function loadArtifacts() {
      host.innerHTML = '<div class="sidebar-empty">Loading…</div>';
      const res = await callSafe('artifacts.list', scopeParams({}));
      host.innerHTML = '';
      if (res.error) { host.innerHTML = `<div class="sidebar-empty">${escapeHTML(res.error)}</div>`; return; }
      const artifacts = res.artifacts || [];
      if (!artifacts.length) { host.innerHTML = '<div class="sidebar-empty">No artifacts for this scope</div>'; return; }
      artifacts.forEach(art => {
        const row = document.createElement('div'); row.className = 'mgmt-row';
        const size = art.sizeBytes ? art.sizeBytes + ' B' : '—';
        row.innerHTML = `<strong>${escapeHTML(art.title || art.id)}</strong><div class="sub">${escapeHTML(art.type || '')} · ${escapeHTML(art.mimeType || '—')} · ${escapeHTML(size)}</div>`;
        const actions = document.createElement('div'); actions.className = 'action-row'; row.appendChild(actions);
        const info = document.createElement('button'); info.className = 'mini-btn'; info.textContent = 'Details';
        info.addEventListener('click', async () => {
          const got = await callSafe('artifacts.get', scopeParams({ artifactId: art.id }));
          detail.style.display = ''; detail.textContent = got.error ? got.error : formatValue(got.artifact || got);
        });
        actions.appendChild(info);
        const bytesMode = art.download && art.download.mode === 'bytes';
        const dl = document.createElement('button'); dl.className = 'mini-btn'; dl.textContent = 'Download'; dl.disabled = !bytesMode;
        if (!bytesMode) dl.title = 'Download unsupported for this artifact';
        dl.addEventListener('click', async () => {
          dl.disabled = true; const old = dl.textContent; dl.textContent = 'Downloading…';
          const got = await callSafe('artifacts.download', scopeParams({ artifactId: art.id }));
          if (got.error) { dl.textContent = truncate(got.error, 24); }
          else { try { triggerBrowserDownload(art.title || art.id, art.mimeType, base64ToBytes(got.data)); dl.textContent = 'Done'; } catch (e) { dl.textContent = 'Failed'; } }
          setTimeout(() => { dl.textContent = old; dl.disabled = false; }, 1500);
        });
        actions.appendChild(dl);
        host.appendChild(row);
      });
    }
    refreshBtn.addEventListener('click', loadArtifacts);
    await loadArtifacts();
  }

  // ── Environments view: isolated execution environments + profiles ─────────
  async function showEnvironmentsView(token) {
    const { grid } = beginManagementView('Environments', 'Isolated execution environments: list, status, create from a profile, and destroy.');
    const res = await callSafe('environments.list', {});
    if (!isViewCurrent('environments', token)) return;
    const environments = res.environments || [];
    const profiles = res.profiles || [];
    const listCard = addMgmtCard(grid, 'Environments');
    const detail = document.createElement('pre'); detail.className = 'grant-detail'; detail.style.display = 'none';
    if (res.error) { const e = document.createElement('div'); e.className = 'mgmt-error'; e.textContent = res.error; listCard.appendChild(e); }
    addRows(listCard, environments, (row, env) => {
      const caps = env.capabilities && env.capabilities.length ? ' · ' + env.capabilities.join(', ') : '';
      row.innerHTML = `<strong>${escapeHTML(env.label || env.id)}</strong><div class="sub">${escapeHTML(env.type || '')} · ${escapeHTML(env.status || '')}${escapeHTML(caps)}</div>`;
      const actions = document.createElement('div'); actions.className = 'action-row'; row.appendChild(actions);
      const st = document.createElement('button'); st.className = 'mini-btn'; st.textContent = 'Status';
      st.addEventListener('click', async () => { const r = await callSafe('environments.status', { environmentId: env.id }); detail.style.display = ''; detail.textContent = r.error ? r.error : formatValue(r); });
      actions.appendChild(st);
      if (env.id !== 'gateway') {
        addActionButton(actions, 'Destroy', 'environments.destroy', () => ({ environmentId: env.id }), () => showEnvironmentsView(++viewSeq));
      }
    });
    listCard.appendChild(detail);
    const profilesCard = addMgmtCard(grid, 'Profiles');
    if (!profiles.length) {
      const empty = document.createElement('div'); empty.className = 'sidebar-empty';
      empty.textContent = 'No environment profiles configured (extra.environments.profiles).';
      profilesCard.appendChild(empty);
    } else {
      addRows(profilesCard, profiles, (row, prof) => {
        row.innerHTML = `<strong>${escapeHTML(prof.id)}</strong><div class="sub">${escapeHTML(prof.providerId || '')}</div>`;
        const actions = document.createElement('div'); actions.className = 'action-row'; row.appendChild(actions);
        addActionButton(actions, 'Create', 'environments.create', () => ({ profileId: prof.id, idempotencyKey: 'webui-' + Date.now() }), () => showEnvironmentsView(++viewSeq));
      });
    }
  }

  function openManagementView(view) {
    closeSidebarOnMobile();
    mainView = view;
    const token = ++viewSeq;
    if (view === 'conversations') return showConversationsView(token);
    if (view === 'artifacts') return showArtifactsView(token);
    if (view === 'environments') return showEnvironmentsView(token);
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
    if (view === 'terminal') return showTerminalView(token);
    if (view === 'boards') return showBoardsView(token);
    if (view === 'tasks') return showTasksView(token);
  }

