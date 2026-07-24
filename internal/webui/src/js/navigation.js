  // ── Tab switching ─────────────────────────────────────────────────────────
  document.querySelectorAll('.tab-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      document.querySelectorAll('.tab-btn').forEach(b => b.classList.remove('active'));
      document.querySelectorAll('.tab-panel').forEach(p => p.classList.remove('active'));
      btn.classList.add('active');
      $('tab-' + btn.dataset.tab).classList.add('active');
      if (btn.dataset.view === 'sessions') {
        mainView = 'sessions';
        openManagementView('sessions');
      } else if (btn.dataset.view && btn.dataset.view !== 'chat') {
        openManagementView(btn.dataset.view);
      } else {
        mainView = 'chat';
        viewSeq++;
        closeSidebarOnMobile();
        if (sessionID) loadSessionHistory(sessionID);
        else if (!msgs.textContent.trim()) addMsg('New conversation started.', 'system');
      }
    });
  });

  $('new-session-btn').addEventListener('click', () => {
    sessionID = null;
    localStorage.removeItem('metiq_session');
    mainView = 'chat';
    viewSeq++;
    finalizeStreaming();
    toolCards = {};
    msgs.innerHTML = '';
    addMsg('New conversation started.', 'system');
    document.querySelectorAll('#sessions-list .sidebar-item').forEach(el => el.classList.remove('active'));
    updateRunControls();
    input.focus();
  });

  async function abortCurrentRun() {
    if (!connected || !selectedSessionHasActiveRun()) return;
    const runSessionID = activeRunSessionID;
    abortBtn.disabled = true;
    try {
      await callMethod('sessions.abort', { key: runSessionID });
      finalizeStreaming();
      setRunActive(false, runSessionID);
      addMsg('Run stopped.', 'system');
    } catch (err) {
      abortBtn.disabled = false;
      addMsg('⚠️ Could not stop run: ' + (err && err.message ? err.message : 'request failed'), 'error-msg');
    }
  }

  async function clearCurrentSession() {
    if (!sessionID || !connected || selectedSessionHasActiveRun()) return;
    if (!confirm('Clear this session transcript?')) return;
    const sid = sessionID;
    clearBtn.disabled = true;
    try {
      await callMethod('sessions.reset', { session_id: sid });
      if (sid !== sessionID) return;
      finalizeStreaming();
      toolCards = {};
      msgs.innerHTML = '';
      addMsg('Session cleared.', 'system');
      loadSessions();
    } catch (err) {
      addMsg('⚠️ Could not clear session: ' + (err && err.message ? err.message : 'request failed'), 'error-msg');
    } finally {
      updateRunControls();
    }
  }

  async function compactCurrentSession() {
    if (!sessionID || !connected || selectedSessionHasActiveRun()) return;
    compactBtn.disabled = true;
    try {
      const res = await callMethod('sessions.compact', { session_id: sessionID });
      const dropped = res && (res.dropped || res.dropped_count || res.droppedCount);
      addMsg(dropped ? `Session compacted; dropped ${dropped} entries.` : 'Session compacted.', 'system');
      loadSessionHistory(sessionID);
      loadSessions();
    } catch (err) {
      addMsg('⚠️ Could not compact session: ' + (err && err.message ? err.message : 'request failed'), 'error-msg');
    } finally {
      updateRunControls();
    }
  }

