  // ── Helpers ────────────────────────────────────────────────────────────────
  function nextID() { return 'ui-' + (++seq); }

  function t(key) {
    return (I18N[locale] && I18N[locale][key]) || I18N.en[key] || key;
  }

  function applyLocale() {
    localeSelect.value = locale;
    input.placeholder = t('placeholder');
    searchBox.placeholder = t('search');
    sendBtn.textContent = t('send');
    abortBtn.textContent = t('stopRun');
    compactBtn.textContent = t('compact');
    clearBtn.textContent = t('clear');
    exportChatBtn.textContent = t('export');
    themeToggle.textContent = t('theme');
    const hint = document.querySelector('footer .hint');
    if (hint) hint.textContent = t('tip');
    const tabLabels = { sessions: 'sessions', cron: 'cron', nodes: 'nodes', mcp: 'mcp', skills: 'skills', channels: 'channels', agents: 'agents', dashboard: 'dashboard', config: 'config', usage: 'usage' };
    document.querySelectorAll('.tab-btn').forEach(btn => {
      const key = tabLabels[btn.dataset.tab];
      if (key) btn.textContent = t(key);
    });
    const newSession = $('new-session-btn');
    if (newSession) newSession.textContent = t('newSession');
    if (connected) connLabel.textContent = t('connected');
  }

  function applyTheme(theme) {
    document.body.classList.toggle('theme-light', theme === 'light');
    localStorage.setItem('metiq_theme', theme);
  }

  function attachmentType(file) {
    if ((file.type || '').startsWith('image/')) return 'image';
    if ((file.type || '').startsWith('audio/')) return 'audio';
    if (file.type === 'application/pdf' || /\.pdf$/i.test(file.name)) return 'pdf';
    return 'file';
  }

  function composerError(message) {
    if (mainView === 'chat') addMsg('⚠️ ' + message, 'error-msg');
  }

  function updateComposerState() {
    sendBtn.disabled = !connected || attachmentReadsPending > 0;
  }

  function renderAttachments() {
    attachmentTray.innerHTML = '';
    if (attachmentReadsPending > 0) {
      const chip = document.createElement('span');
      chip.className = 'attachment-chip';
      chip.textContent = `loading ${attachmentReadsPending}…`;
      attachmentTray.appendChild(chip);
    }
    pendingAttachments.forEach((att, idx) => {
      const chip = document.createElement('button');
      chip.type = 'button';
      chip.className = 'attachment-chip';
      chip.textContent = `${att.filename} ×`;
      chip.addEventListener('click', () => {
        pendingAttachments.splice(idx, 1);
        renderAttachments();
      });
      attachmentTray.appendChild(chip);
    });
  }

  function addFiles(files) {
    const batch = Array.from(files || []);
    if (!batch.length) return;
    const currentBytes = pendingAttachments.reduce((sum, att) => sum + (att.size || 0), 0);
    let acceptedBytes = 0;
    batch.forEach(file => {
      const type = attachmentType(file);
      if (type === 'file') { composerError(`Unsupported attachment type: ${file.name}`); return; }
      if (pendingAttachments.length + attachmentReadsPending >= maxAttachments) { composerError(`Attachment limit is ${maxAttachments} files.`); return; }
      if (file.size > maxAttachmentBytes || currentBytes + acceptedBytes + file.size > maxAttachmentTotalBytes) { composerError(`Attachment too large: ${file.name}`); return; }
      acceptedBytes += file.size;
      const generation = attachmentReadGeneration;
      attachmentReadsPending++;
      updateComposerState();
      renderAttachments();
      const reader = new FileReader();
      const finish = () => { attachmentReadsPending = Math.max(0, attachmentReadsPending - 1); updateComposerState(); renderAttachments(); };
      reader.onload = () => {
        if (generation !== attachmentReadGeneration) { finish(); return; }
        const dataURL = String(reader.result || '');
        const base64 = dataURL.includes(',') ? dataURL.split(',').pop() : dataURL;
        pendingAttachments.push({ type, base64, mime_type: file.type, filename: file.name, size: file.size });
        finish();
      };
      reader.onerror = () => { composerError(`Could not read attachment: ${file.name}`); finish(); };
      reader.onabort = () => { composerError(`Attachment read cancelled: ${file.name}`); finish(); };
      reader.readAsDataURL(file);
    });
  }

  function saveInputHistory(text) {
    if (!text) return;
    inputHistory = inputHistory.filter(item => item !== text).concat(text).slice(-50);
    inputHistoryIndex = inputHistory.length;
    localStorage.setItem('metiq_input_history', JSON.stringify(inputHistory));
  }

  async function loadCommandCatalog() {
    const fallback = [
      { command: '/help', text: '/help', desc: 'Show available commands', source: 'gateway' },
      { command: '/status', text: '/status', desc: 'Show gateway status', source: 'gateway' },
    ];
    try {
      const res = await fetch('/command-catalog.json').then(r => r.json());
      const items = res.commands || res.items || res.slash_commands || fallback;
      slashCommands = items.map(item => ({
        cmd: item.cmd || item.command || item.name,
        text: item.text || item.insert || item.command || item.cmd || item.name,
        desc: item.desc || item.description || item.summary || item.source || '',
        source: item.source || item.provider || 'catalog',
      })).filter(item => item.cmd && item.cmd.startsWith('/'));
    } catch { slashCommands = fallback.map(item => ({ cmd: item.command, text: item.text, desc: item.desc, source: item.source })); }
  }

  function renderSlashMenu() {
    const q = input.value.trim().toLowerCase();
    if (!q.startsWith('/')) { slashMenu.classList.remove('visible'); return; }
    const matches = slashCommands.filter(item => item.cmd.startsWith(q) || item.desc.toLowerCase().includes(q.slice(1))).slice(0, 6);
    slashMenu.innerHTML = '';
    matches.forEach(item => {
      const el = document.createElement('div');
      el.className = 'slash-item';
      el.textContent = `${item.cmd} — ${item.desc}${item.source ? ' [' + item.source + ']' : ''}`;
      el.addEventListener('click', () => {
        input.value = item.text + ' ';
        slashMenu.classList.remove('visible');
        input.focus();
      });
      slashMenu.appendChild(el);
    });
    slashMenu.classList.toggle('visible', matches.length > 0);
  }

  function searchChat(query) {
    const q = String(query || '').trim().toLowerCase();
    if (!q) return;
    const hit = Array.from(msgs.querySelectorAll('.msg, .tool-card')).find(el => (el.textContent || '').toLowerCase().includes(q));
    if (hit) hit.scrollIntoView({ behavior: 'smooth', block: 'center' });
  }

  function exportChatMarkdown() {
    const lines = Array.from(msgs.querySelectorAll('.msg, .tool-card')).map(el => {
      const role = el.classList.contains('user') ? 'User' : el.classList.contains('system') ? 'System' : el.classList.contains('tool-card') ? 'Tool' : 'Agent';
      return `## ${role}\n\n${(el.textContent || '').trim()}`;
    }).filter(Boolean).join('\n\n');
    const blob = new Blob([lines || '# Metiq chat export\n'], { type: 'text/markdown' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `metiq-chat-${sessionID || 'new'}.md`;
    a.click();
    URL.revokeObjectURL(url);
  }

  function escapeHTML(text) {
    return String(text || '').replace(/[&<>"']/g, ch => ({
      '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'
    }[ch]));
  }

  function renderMarkdown(text) {
    if (!window.marked || !window.DOMPurify) return escapeHTML(text).replace(/\n/g, '<br>');
    marked.setOptions({
      gfm: true,
      breaks: true,
      highlight: (code, lang) => {
        if (!window.hljs) return escapeHTML(code);
        const language = hljs.getLanguage(lang) ? lang : 'plaintext';
        return hljs.highlight(code, { language }).value;
      }
    });
    return DOMPurify.sanitize(marked.parse(text || ''), {
      ADD_ATTR: ['target', 'rel']
    });
  }

  function decorateMarkdown(el) {
    el.querySelectorAll('a[href]').forEach(a => {
      a.target = '_blank';
      a.rel = 'noopener noreferrer';
    });
    el.querySelectorAll('pre').forEach(pre => {
      const code = pre.querySelector('code');
      if (code && window.hljs) hljs.highlightElement(code);
      if (pre.querySelector('.copy-code-btn')) return;
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'copy-code-btn';
      btn.textContent = 'Copy';
      pre.appendChild(btn);
    });
  }

  function setMessageContent(el, text, role) {
    if (role === 'agent') {
      el.classList.add('markdown');
      el.innerHTML = renderMarkdown(text);
      decorateMarkdown(el);
      return;
    }
    el.textContent = text;
  }

  function cloneComponentTemplate(id, fallbackClass) {
    const tpl = $(id);
    if (tpl && tpl.content && tpl.content.firstElementChild) {
      return tpl.content.firstElementChild.cloneNode(true);
    }
    const el = document.createElement('div');
    el.className = fallbackClass || '';
    return el;
  }

  function addMsg(text, role) {
    const el = cloneComponentTemplate('chat-message-template', 'msg');
    el.className = 'msg ' + role;
    setMessageContent(el, text, role);
    msgs.appendChild(el);
    msgs.scrollTop = msgs.scrollHeight;
    return el;
  }

  function formatValue(value) {
    if (value === undefined || value === null || value === '') return '';
    if (typeof value === 'string') {
      const trimmed = value.trim();
      if ((trimmed.startsWith('{') && trimmed.endsWith('}')) || (trimmed.startsWith('[') && trimmed.endsWith(']'))) {
        try { return JSON.stringify(JSON.parse(trimmed), null, 2); } catch { /* keep original */ }
      }
      return value;
    }
    try { return JSON.stringify(value, null, 2); } catch { return String(value); }
  }

  function firstPresent(...values) {
    return values.find(v => v !== undefined && v !== null && v !== '');
  }

  function toolKey(payload) {
    return payload.tool_call_id || payload.toolCallID || payload.call_id || payload.id ||
      [payload.tool_name || payload.name || 'tool', payload.turn_id || payload.ts_ms || Date.now()].join(':');
  }

  function toolName(payload) {
    return payload.tool_name || payload.toolName || payload.name || payload.tool || 'tool';
  }

  function toolSessionMatches(payload) {
    return !payload || !payload.session_id || !sessionID || payload.session_id === sessionID;
  }

  function addToolSection(body, label, value) {
    const rendered = formatValue(value);
    if (!rendered) return;
    const section = document.createElement('div');
    section.className = 'tool-section';
    const title = document.createElement('div');
    title.className = 'tool-label';
    title.textContent = label;
    const pre = document.createElement('pre');
    pre.className = 'tool-pre';
    pre.textContent = rendered;
    section.appendChild(title);
    section.appendChild(pre);
    body.appendChild(section);
  }

  function renderToolActivity(payload, status) {
    if (!payload || !toolSessionMatches(payload)) return;
    hideThinking();
    const key = toolKey(payload);
    let state = toolCards[key];
    if (!state) {
      const el = cloneComponentTemplate('tool-card-template', 'tool-card');
      el.className = 'tool-card';
      msgs.appendChild(el);
      state = toolCards[key] = { el, payload: {}, progress: [], status: 'started' };
    }
    state.payload = Object.assign({}, state.payload, payload);
    state.status = status || state.status;
    if (status === 'progress') {
      const progress = firstPresent(payload.result, payload.message, payload.text, payload.progress, payload.data);
      if (progress !== undefined && progress !== null && progress !== '') state.progress.push(progress);
    }
    updateToolCard(state);
    msgs.scrollTop = msgs.scrollHeight;
  }

  function updateToolCard(state) {
    const payload = state.payload || {};
    const status = state.status || 'started';
    const isRunning = status === 'started' || status === 'progress';
    const isError = status === 'error' || !!payload.error;
    const displayStatus = isError ? 'error' : (status === 'result' ? 'done' : (isRunning ? 'running' : status));
    const el = state.el;
    el.className = 'tool-card' + (isRunning ? ' running' : '') + (isError ? ' error' : '');
    el.innerHTML = '';

    const details = document.createElement('details');
    details.open = isRunning || isError;
    const summary = document.createElement('summary');
    summary.innerHTML = `<span class="tool-name">${escapeHTML(toolName(payload))}</span>` +
      `<span class="tool-status ${escapeHTML(displayStatus)}">${escapeHTML(displayStatus)}</span>` +
      `<span class="tool-meta-line">${escapeHTML(truncate(payload.tool_call_id || payload.id || payload.turn_id || '', 34))}</span>`;
    details.appendChild(summary);

    const body = document.createElement('div');
    body.className = 'tool-body';
    const args = firstPresent(payload.args, payload.arguments, payload.tool_args, payload.toolArgs, payload.input,
      (state.status === 'started' ? payload.data : undefined));
    addToolSection(body, 'Input', args);
    if (state.progress && state.progress.length) addToolSection(body, 'Progress', state.progress);
    addToolSection(body, 'Result', payload.result);
    addToolSection(body, 'Error', payload.error);
    addToolSection(body, 'Raw event', payload);
    details.appendChild(body);
    el.appendChild(details);
  }

  function renderHistoryEntry(entry) {
    const meta = entry.meta || entry.metadata || {};
    const text = entry.text || entry.content || entry.message || '';
    const messageKind = meta.message_kind || entry.message_kind;
    const toolCalls = meta.tool_calls || entry.tool_calls || entry.toolCalls;
    const toolCallID = meta.tool_call_id || entry.tool_call_id || entry.toolCallID;

    if (Array.isArray(toolCalls) && toolCalls.length) {
      toolCalls.forEach(call => renderToolActivity({
        tool_call_id: call.id || call.tool_call_id,
        tool_name: call.name || call.tool_name || call.function_name,
        args: call.args || call.arguments || call.args_json || call.input,
        session_id: entry.session_id || sessionID,
        turn_id: entry.turn_id || entry.entry_id,
      }, 'result'));
      if (text && messageKind !== 'tool_call') addMsg(text, entry.role === 'system' ? 'system' : 'agent');
      return;
    }

    if (toolCallID || entry.role === 'tool' || messageKind === 'tool_result') {
      renderToolActivity({
        tool_call_id: toolCallID || entry.entry_id,
        tool_name: meta.tool_name || entry.tool_name || 'tool result',
        result: text || meta.tool_result,
        error: meta.tool_error || entry.error,
        session_id: entry.session_id || sessionID,
        turn_id: entry.turn_id || entry.entry_id,
      }, meta.tool_error || entry.error ? 'error' : 'result');
      return;
    }

    const role = entry.role === 'user' ? 'user' : (entry.role === 'system' ? 'system' : 'agent');
    if (text) addMsg(text, role);
  }

