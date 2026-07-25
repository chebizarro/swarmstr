  // ── Terminal panel ────────────────────────────────────────────────────────
  // Dependency-free PTY viewer: a <pre>-based renderer fed by terminal.data
  // events (seq-ordered UTF-16 offsets), with replay reconciliation on
  // terminal.attach, keyboard input, resize, and drag-drop terminal.upload.
  let termSessionID = null;
  let termSeq = 0;
  let termLines = [''];
  let termCol = 0;
  let termPendingEsc = '';
  let termDetached = false;
  let termPreviewMode = false;
  let termStatusText = 'No terminal session.';
  let termResizeTimer = null;
  const TERM_MAX_LINES = 2000;
  const TERM_UPLOAD_MAX_BYTES = 16 * 1024 * 1024;
  const TERM_KEYS = {
    Enter: '\r', Backspace: '\x7f', Tab: '\t', Escape: '\x1b',
    ArrowUp: '\x1b[A', ArrowDown: '\x1b[B', ArrowRight: '\x1b[C', ArrowLeft: '\x1b[D',
    Home: '\x1b[H', End: '\x1b[F', Delete: '\x1b[3~', PageUp: '\x1b[5~', PageDown: '\x1b[6~'
  };

  function termSetStatus(text) {
    termStatusText = text;
    const el = $('terminal-status');
    if (el) el.textContent = text;
  }

  function termStripSequences(data) {
    return data
      .replace(/\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)/g, '')
      .replace(/\x1b\[[0-9;?<=>!"'$ ]*[@-~]/g, '')
      .replace(/\x1b[()#][@-Z0-9]/g, '')
      .replace(/\x1b[@-Z\\^_]/g, '');
  }

  // Holds back a trailing incomplete escape sequence so a chunk boundary
  // cannot leak half a CSI/OSC sequence into the rendered text.
  function termSplitPendingEscape(data) {
    const idx = data.lastIndexOf('\x1b');
    if (idx === -1) return { text: data, pending: '' };
    const tail = data.slice(idx);
    if (tail.length > 64) return { text: data, pending: '' };
    let complete = true;
    if (tail === '\x1b') complete = false;
    else if (tail[1] === '[') complete = /^\x1b\[[0-9;?<=>!"'$ ]*[@-~]/.test(tail);
    else if (tail[1] === ']') complete = /(?:\x07|\x1b\\)/.test(tail);
    else if (tail[1] === '(' || tail[1] === ')' || tail[1] === '#') complete = tail.length >= 3;
    if (complete) return { text: data, pending: '' };
    return { text: data.slice(0, idx), pending: tail };
  }

  function termFeed(data) {
    if (!data) return;
    const split = termSplitPendingEscape(termPendingEsc + data);
    termPendingEsc = split.pending;
    const text = termStripSequences(split.text);
    let line = termLines[termLines.length - 1];
    for (const ch of text) {
      if (ch === '\n') {
        termLines[termLines.length - 1] = line;
        termLines.push('');
        line = '';
        termCol = 0;
      } else if (ch === '\r') {
        termCol = 0;
      } else if (ch === '\b') {
        if (termCol > 0) termCol--;
      } else if (ch === '\t') {
        const next = (termCol + 8) - ((termCol + 8) % 8);
        while (termCol < next) { line = termWriteChar(line, ' '); }
      } else if (ch >= ' ' || ch.charCodeAt(0) > 126) {
        line = termWriteChar(line, ch);
      }
    }
    termLines[termLines.length - 1] = line;
    if (termLines.length > TERM_MAX_LINES) termLines.splice(0, termLines.length - TERM_MAX_LINES);
  }

  function termWriteChar(line, ch) {
    if (termCol < line.length) line = line.slice(0, termCol) + ch + line.slice(termCol + 1);
    else line = line + ' '.repeat(termCol - line.length) + ch;
    termCol++;
    return line;
  }

  function termRender() {
    const pre = $('terminal-screen');
    if (!pre) return;
    pre.textContent = termLines.join('\n');
    pre.scrollTop = pre.scrollHeight;
  }

  function termReset(buffer, seq) {
    termLines = [''];
    termCol = 0;
    termPendingEsc = '';
    termFeed(buffer || '');
    termSeq = seq || 0;
    termRender();
  }

  function handleTerminalData(payload) {
    if (!payload || payload.sessionId !== termSessionID || termPreviewMode) return;
    if (typeof payload.data !== 'string' || typeof payload.seq !== 'number') return;
    if (payload.seq <= termSeq) return; // fully covered by the replay buffer
    const start = payload.seq - payload.data.length;
    const fresh = start < termSeq ? payload.data.slice(termSeq - start) : payload.data;
    termFeed(fresh);
    termSeq = payload.seq;
    termRender();
  }

  function handleTerminalExit(payload) {
    if (!payload || !payload.sessionId) return;
    if (payload.sessionId !== termSessionID) {
      if (mainView === 'terminal') loadTerminalSessions();
      return;
    }
    if (payload.reason === 'detached') {
      // The session is still alive server-side; another client took it over.
      termDetached = true;
      termSetStatus('Another client attached this session — output paused here. Re-attach to resume.');
      if (mainView === 'terminal') loadTerminalSessions();
      return;
    }
    const code = payload.exitCode !== undefined && payload.exitCode !== null ? ', exit code ' + payload.exitCode : '';
    termSetStatus('Terminal session ended (' + (payload.reason || 'exit') + code + ').');
    termSessionID = null;
    termDetached = false;
    if (mainView === 'terminal') loadTerminalSessions();
  }

  function handleTerminalDisconnect() {
    if (!termSessionID) return;
    termSessionID = null;
    termDetached = false;
    termSetStatus('Gateway disconnected — the terminal session was closed.');
  }

  function terminalKeyData(e) {
    if (e.metaKey) return null;
    if (e.ctrlKey) {
      if (e.key.length === 1) {
        const code = e.key.toLowerCase().charCodeAt(0);
        if (code >= 97 && code <= 122) return String.fromCharCode(code - 96);
      }
      return null;
    }
    if (TERM_KEYS[e.key]) return TERM_KEYS[e.key];
    if (e.key.length === 1 && !e.altKey) return e.key;
    return null;
  }

  function termSendInput(data) {
    if (!termSessionID || termDetached || termPreviewMode || !data) return;
    callSafe('terminal.input', { session_id: termSessionID, data });
  }

  function termMeasureGrid(pre) {
    const probe = document.createElement('span');
    probe.textContent = 'W'.repeat(20);
    probe.style.position = 'absolute';
    probe.style.visibility = 'hidden';
    pre.appendChild(probe);
    const charW = probe.getBoundingClientRect().width / 20 || 8;
    const charH = probe.getBoundingClientRect().height || 16;
    probe.remove();
    const cols = Math.min(2000, Math.max(20, Math.floor((pre.clientWidth - 12) / charW)));
    const rows = Math.min(2000, Math.max(5, Math.floor((pre.clientHeight - 12) / charH)));
    return { cols, rows };
  }

  function termSendResize() {
    const pre = $('terminal-screen');
    if (!pre || !termSessionID || termDetached || termPreviewMode) return;
    const grid = termMeasureGrid(pre);
    callSafe('terminal.resize', { session_id: termSessionID, cols: grid.cols, rows: grid.rows });
  }

  window.addEventListener('resize', () => {
    if (termResizeTimer) clearTimeout(termResizeTimer);
    termResizeTimer = setTimeout(termSendResize, 300);
  });

  async function termAttach(sid) {
    const res = await callSafe('terminal.attach', { session_id: sid });
    if (res.error) { termSetStatus('Attach failed: ' + res.error); return; }
    termSessionID = res.sessionId;
    termDetached = false;
    termPreviewMode = false;
    termReset(res.buffer || '', res.seq || 0);
    termSetStatus('Attached to ' + res.sessionId + ' · ' + (res.shell || 'shell') + ' · ' + (res.cwd || ''));
    termSendResize();
    focusTerminal();
    loadTerminalSessions();
  }

  async function termOpenNew() {
    const pre = $('terminal-screen');
    const grid = pre ? termMeasureGrid(pre) : { cols: 80, rows: 24 };
    const res = await callSafe('terminal.open', { cols: grid.cols, rows: grid.rows });
    if (res.error) { termSetStatus('Open failed: ' + res.error); return; }
    termSessionID = res.sessionId;
    termDetached = false;
    termPreviewMode = false;
    termReset('', 0);
    termSetStatus('Opened ' + res.sessionId + ' · ' + (res.shell || 'shell') + ' · ' + (res.cwd || ''));
    focusTerminal();
    loadTerminalSessions();
  }

  async function termPreview(sid) {
    const res = await callSafe('terminal.text', { session_id: sid });
    if (res.error) { termSetStatus('Preview failed: ' + res.error); return; }
    termSessionID = null;
    termDetached = false;
    termPreviewMode = true;
    termLines = String(res.text || '').split('\n');
    if (termLines.length > TERM_MAX_LINES) termLines.splice(0, termLines.length - TERM_MAX_LINES);
    termCol = 0;
    termPendingEsc = '';
    termRender();
    termSetStatus('Read-only text preview of ' + sid + ' (not attached).');
  }

  function focusTerminal() {
    const pre = $('terminal-screen');
    if (pre) pre.focus();
  }

  async function termUploadFiles(files) {
    if (!termSessionID || termDetached) { termSetStatus('Attach a session before uploading.'); return; }
    for (const file of Array.from(files || [])) {
      if (file.size > TERM_UPLOAD_MAX_BYTES) {
        termSetStatus('Upload rejected: ' + file.name + ' exceeds the 16 MiB terminal upload cap.');
        continue;
      }
      termSetStatus('Uploading ' + file.name + '…');
      const contentBase64 = await new Promise((resolve, reject) => {
        const reader = new FileReader();
        // readAsDataURL yields canonical padded base64 after the comma.
        reader.onload = () => resolve(String(reader.result || '').split(',')[1] || '');
        reader.onerror = () => reject(new Error('could not read file'));
        reader.readAsDataURL(file);
      }).catch(() => null);
      if (contentBase64 === null) { termSetStatus('Upload failed: could not read ' + file.name); continue; }
      const res = await callSafe('terminal.upload', { session_id: termSessionID, name: file.name, contentBase64 });
      if (res.error) termSetStatus('Upload failed: ' + res.error);
      else termSetStatus('Staged ' + (res.path || file.name) + ' (' + (res.size || file.size) + ' bytes).');
    }
  }

  async function loadTerminalSessions() {
    const card = $('terminal-sessions');
    if (!card) return;
    const res = await callSafe('terminal.list', {});
    const listHost = card.querySelector('.mgmt-list-host');
    if (!listHost) return;
    listHost.innerHTML = '';
    if (res.error) {
      listHost.innerHTML = `<div class="sidebar-empty">${escapeHTML(res.error)}</div>`;
      return;
    }
    const sessions = res.sessions || [];
    if (!sessions.length) {
      listHost.innerHTML = '<div class="sidebar-empty">No live terminal sessions</div>';
      return;
    }
    sessions.forEach(sess => {
      const row = document.createElement('div');
      row.className = 'mgmt-row';
      const current = sess.sessionId === termSessionID ? ' · attached here' : '';
      row.innerHTML = `<strong>${escapeHTML(truncate(sess.sessionId, 30))}</strong><div class="sub">${escapeHTML(sess.shell || '')} · ${escapeHTML(truncate(sess.cwd || '', 60))}${escapeHTML(current)}</div>`;
      const actions = document.createElement('div');
      actions.className = 'action-row';
      const attachBtnEl = document.createElement('button');
      attachBtnEl.className = 'mini-btn';
      attachBtnEl.textContent = sess.sessionId === termSessionID && !termDetached ? 'Re-attach' : 'Attach';
      attachBtnEl.addEventListener('click', () => termAttach(sess.sessionId));
      actions.appendChild(attachBtnEl);
      const previewBtn = document.createElement('button');
      previewBtn.className = 'mini-btn';
      previewBtn.textContent = 'Text';
      previewBtn.addEventListener('click', () => termPreview(sess.sessionId));
      actions.appendChild(previewBtn);
      if (sess.sessionId === termSessionID) {
        const closeBtnEl = document.createElement('button');
        closeBtnEl.className = 'mini-btn';
        closeBtnEl.textContent = 'Close';
        closeBtnEl.addEventListener('click', async () => {
          await callSafe('terminal.close', { session_id: sess.sessionId });
          loadTerminalSessions();
        });
        actions.appendChild(closeBtnEl);
      }
      row.appendChild(actions);
      listHost.appendChild(row);
    });
  }

  async function showTerminalView(token) {
    const { grid } = beginManagementView('Terminal', 'PTY sessions: open, attach with replay, type, resize, and drag-drop uploads.');
    if (!isViewCurrent('terminal', token)) return;

    const sessionsCard = addMgmtCard(grid, 'Sessions');
    sessionsCard.id = 'terminal-sessions';
    const sessActions = document.createElement('div');
    sessActions.className = 'action-row';
    const openBtn = document.createElement('button');
    openBtn.className = 'mini-btn';
    openBtn.textContent = 'New terminal';
    openBtn.addEventListener('click', termOpenNew);
    sessActions.appendChild(openBtn);
    const refreshBtn = document.createElement('button');
    refreshBtn.className = 'mini-btn';
    refreshBtn.textContent = 'Refresh';
    refreshBtn.addEventListener('click', loadTerminalSessions);
    sessActions.appendChild(refreshBtn);
    sessionsCard.appendChild(sessActions);
    const listHost = document.createElement('div');
    listHost.className = 'mgmt-list-host';
    sessionsCard.appendChild(listHost);

    const termCard = addMgmtCard(grid, 'Screen');
    termCard.className += ' terminal-card';
    const status = document.createElement('div');
    status.className = 'sub';
    status.id = 'terminal-status';
    status.textContent = termStatusText;
    termCard.appendChild(status);
    const pre = document.createElement('pre');
    pre.id = 'terminal-screen';
    pre.className = 'terminal-screen';
    pre.tabIndex = 0;
    pre.setAttribute('aria-label', 'Terminal output');
    termCard.appendChild(pre);
    const hint = document.createElement('div');
    hint.className = 'sub';
    hint.textContent = 'Click the screen to type. Drop a file on it to stage it in the session cwd (16 MiB max).';
    termCard.appendChild(hint);

    pre.addEventListener('keydown', e => {
      const data = terminalKeyData(e);
      if (data === null) return;
      e.preventDefault();
      termSendInput(data);
    });
    pre.addEventListener('paste', e => {
      e.preventDefault();
      const text = e.clipboardData && e.clipboardData.getData('text');
      if (text) termSendInput(text);
    });
    pre.addEventListener('dragover', e => { e.preventDefault(); pre.classList.add('drag-over'); });
    pre.addEventListener('dragleave', () => pre.classList.remove('drag-over'));
    pre.addEventListener('drop', e => {
      e.preventDefault();
      pre.classList.remove('drag-over');
      termUploadFiles(e.dataTransfer && e.dataTransfer.files);
    });

    termRender();
    await loadTerminalSessions();
  }
