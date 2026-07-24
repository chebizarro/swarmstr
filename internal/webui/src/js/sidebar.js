  async function loadSidebar() {
    // Load sessions, channels, agents in parallel.
    await Promise.all([loadSessions(), loadChannels(), loadAgents()]);
  }

  async function loadSessions() {
    try {
      const [res, groupsRes] = await Promise.all([
        callMethod('sessions.list', { limit: 40 }),
        callSafe('sessions.groups.list', {})
      ]);
      const sessions = res && (res.sessions || res.items || []);
      if (!sessions || !sessions.length) {
        sessionsList.innerHTML = `<div class="sidebar-empty">${escapeHTML(t('noSessions'))}</div>`;
        return;
      }
      const catalog = resultItems(groupsRes, 'groups').map(g => typeof g === 'string' ? g : g.name).filter(Boolean);
      const buckets = new Map(catalog.map(name => [name, []]));
      buckets.set('', []);
      sessions.forEach(s => {
        const group = s.group || '';
        if (!buckets.has(group)) buckets.set(group, []);
        buckets.get(group).push(s);
      });
      sessionsList.innerHTML = '';
      buckets.forEach((items, group) => {
        if (!items.length) return;
        if (group) {
          const heading = document.createElement('div');
          heading.className = 'sidebar-section';
          heading.textContent = group;
          sessionsList.appendChild(heading);
        }
        items.forEach(s => {
          const sid = s.session_id || s.sessionId || s.key;
          const el = document.createElement('div');
          el.className = 'sidebar-item' + (sid === sessionID ? ' active' : '');
          el.dataset.sid = sid;
          el.innerHTML = `<div>${truncate(sid, 22)}</div><div class="sub">${s.preview || ''}</div>`;
          el.addEventListener('click', () => {
            switchSession(sid);
            closeSidebarOnMobile();
          });
          sessionsList.appendChild(el);
        });
      });
    } catch { sessionsList.innerHTML = '<div class="sidebar-empty">—</div>'; }
  }

  async function loadChannels() {
    try {
      const res = await callMethod('channels.list', {});
      const chans = res && (res.channels || res.items || []);
      if (!chans || !chans.length) {
        channelsList.innerHTML = `<div class="sidebar-empty">${escapeHTML(t('noChannels'))}</div>`;
        return;
      }
      channelsList.innerHTML = '';
      chans.forEach(c => {
        const el = document.createElement('div');
        el.className = 'sidebar-item';
        el.innerHTML = `<span class="channel-dot"></span>${c.id || '—'}<div class="sub">${c.type || ''}</div>`;
        el.addEventListener('click', () => {
          mainView = 'channels';
          showChannelsView(c, ++viewSeq);
          closeSidebarOnMobile();
        });
        channelsList.appendChild(el);
      });
    } catch { channelsList.innerHTML = '<div class="sidebar-empty">—</div>'; }
  }

  async function loadAgents() {
    try {
      const res = await callMethod('agents.list', {});
      const ags = res && (res.agents || res.items || []);
      if (!ags || !ags.length) {
        agentsList.innerHTML = `<div class="sidebar-empty">${escapeHTML(t('noAgents'))}</div>`;
        return;
      }
      agentsList.innerHTML = '';
      ags.forEach(a => {
        const el = document.createElement('div');
        el.className = 'sidebar-item' + (a.id === activeAgentID ? ' active' : '');
        el.innerHTML = `<div>${a.id || a.agent_id}</div><div class="sub">${a.model || ''}</div>`;
        el.addEventListener('click', () => {
          mainView = 'agents';
          showAgentsView(a, ++viewSeq);
          closeSidebarOnMobile();
        });
        agentsList.appendChild(el);
      });
    } catch { agentsList.innerHTML = '<div class="sidebar-empty">—</div>'; }
  }

  function truncate(s, n) { return s && s.length > n ? s.slice(0, n) + '…' : (s || ''); }

  async function loadSessionHistory(sid) {
    historyLoadingSessionID = sid;
    bufferedLiveEvents = bufferedLiveEvents.filter(item => !(item.payload && item.payload.session_id === sid));
    try {
      const res = await callMethod('chat.history', { session_id: sid, limit: 200 });
      if (sid !== sessionID) return;
      const entries = res && (res.entries || res.items || res.transcript || []);
      msgs.innerHTML = '';
      toolCards = {};
      if (!entries || !entries.length) {
        addMsg(t('noMessages'), 'system');
        return;
      }
      entries.forEach(renderHistoryEntry);
    } catch (err) {
      if (sid !== sessionID) return;
      msgs.innerHTML = '';
      addMsg('⚠️ ' + t('errorLoadHistory') + ': ' + (err && err.message ? err.message : 'request failed'), 'error-msg');
    } finally {
      if (historyLoadingSessionID === sid) {
        historyLoadingSessionID = null;
        const replay = [];
        bufferedLiveEvents = bufferedLiveEvents.filter(item => {
          if (item.payload && item.payload.session_id === sid) {
            replay.push(item);
            return false;
          }
          return true;
        });
        replay.forEach(item => onEvent(item.event, item.payload));
      }
    }
  }

  function switchSession(sid) {
    sessionID = sid;
    localStorage.setItem('metiq_session', sid);
    mainView = 'chat';
    viewSeq++;
    updateRunControls();
    finalizeStreaming();
    toolCards = {};
    msgs.innerHTML = '';
    addMsg(t('loading') + ' session history…', 'system');
    // Update active state in sidebar.
    document.querySelectorAll('#sessions-list .sidebar-item').forEach(el => {
      el.classList.toggle('active', el.dataset.sid === sid);
    });
    loadSessionHistory(sid);
  }

