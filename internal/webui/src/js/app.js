  // ── Sending messages ──────────────────────────────────────────────────────
  async function sendMessage() {
    const text = input.value.trim();
    if (attachmentReadsPending > 0) { composerError('Attachments are still loading.'); return; }
    if ((!text && !pendingAttachments.length) || !connected) return;
    const attachments = pendingAttachments.slice();
    attachmentReadGeneration++;
    pendingAttachments = [];
    renderAttachments();
    saveInputHistory(text);
    input.value = '';
    input.style.height = '';
    addMsg(text || `[${attachments.length} attachment(s)]`, 'user');
    sendBtn.disabled = true;
    setRunActive(true, sessionID);

    // Show thinking indicator; streaming chunks will replace it.
    showThinking();

    try {
      const activityBeforeSend = chatActivityVersion;
      const result = await callMethod('sessions.send', { key: sessionID || undefined, message: text, attachments });
      if (result && result.session_id && result.session_id !== sessionID) {
        sessionID = result.session_id;
        localStorage.setItem('metiq_session', sessionID);
        if (activeRun && !activeRunSessionID) activeRunSessionID = sessionID;
        updateRunControls();
      }
      // If no streaming chunks arrived, render the final response from result.text.
      if (chatActivityVersion === activityBeforeSend && !streamingBubble && result && result.text) {
        hideThinking();
        addMsg(result.text, 'agent');
        setRunActive(false, sessionID);
        loadSessions();
      } else if (result && (result.done || result.completed || result.status === 'done')) {
        setRunActive(false, sessionID);
      }
    } catch (err) {
      finalizeStreaming();
      hideThinking();
      setRunActive(false, activeRunSessionID || sessionID);
      addMsg('⚠️ ' + (err && err.message ? err.message : 'Request failed'), 'error-msg');
    } finally {
      updateComposerState();
      input.focus();
    }
  }

  // ── Event listeners ───────────────────────────────────────────────────────
  sendBtn.addEventListener('click', sendMessage);
  attachBtn.addEventListener('click', () => attachInput.click());
  attachInput.addEventListener('change', () => { addFiles(attachInput.files); attachInput.value = ''; });
  themeToggle.addEventListener('click', () => applyTheme(document.body.classList.contains('theme-light') ? 'dark' : 'light'));
  localeSelect.addEventListener('change', () => {
    locale = localeSelect.value || 'en';
    localStorage.setItem('metiq_locale', locale);
    applyLocale();
  });
  searchToggle.addEventListener('click', () => {
    searchBox.classList.toggle('visible');
    if (searchBox.classList.contains('visible')) searchBox.focus();
  });
  searchBox.addEventListener('keydown', e => {
    if (e.key === 'Enter') searchChat(searchBox.value);
    if (e.key === 'Escape') searchBox.classList.remove('visible');
  });
  exportChatBtn.addEventListener('click', exportChatMarkdown);
  mobileMenuBtn.addEventListener('click', () => setSidebarOpen(!document.body.classList.contains('sidebar-open')));
  sidebarBackdrop.addEventListener('click', () => setSidebarOpen(false));
  window.addEventListener('keydown', e => {
    if (e.key === 'Escape') setSidebarOpen(false);
  });
  window.addEventListener('resize', () => {
    if (window.innerWidth > 900) setSidebarOpen(false);
  });
  abortBtn.addEventListener('click', abortCurrentRun);
  compactBtn.addEventListener('click', compactCurrentSession);
  clearBtn.addEventListener('click', clearCurrentSession);
  input.addEventListener('keydown', e => {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); slashMenu.classList.remove('visible'); sendMessage(); }
    if (e.key === 'ArrowUp' && inputHistory.length && (!input.value || historyBrowsing)) {
      e.preventDefault();
      if (!historyBrowsing) inputHistoryIndex = inputHistory.length;
      historyBrowsing = true;
      inputHistoryIndex = Math.max(0, inputHistoryIndex - 1);
      input.value = inputHistory[inputHistoryIndex] || '';
    }
    if (e.key === 'ArrowDown' && historyBrowsing) {
      e.preventDefault();
      inputHistoryIndex = Math.min(inputHistory.length, inputHistoryIndex + 1);
      input.value = inputHistory[inputHistoryIndex] || '';
      if (inputHistoryIndex >= inputHistory.length) historyBrowsing = false;
    }
    if (e.key === 'Escape') slashMenu.classList.remove('visible');
  });
  input.addEventListener('input', () => {
    historyBrowsing = false;
    input.style.height = 'auto';
    input.style.height = input.scrollHeight + 'px';
    renderSlashMenu();
    sendTypingSignal(input.value.trim().length > 0);
  });
  document.querySelector('footer').addEventListener('dragover', e => { e.preventDefault(); e.currentTarget.classList.add('drag-over'); });
  document.querySelector('footer').addEventListener('dragleave', e => { e.currentTarget.classList.remove('drag-over'); });
  document.querySelector('footer').addEventListener('drop', e => {
    e.preventDefault();
    e.currentTarget.classList.remove('drag-over');
    addFiles(e.dataTransfer && e.dataTransfer.files);
  });
  document.addEventListener('paste', e => {
    if (document.activeElement !== input) return;
    const files = e.clipboardData && e.clipboardData.files;
    if (files && files.length) addFiles(files);
  });

  // ── Boot ──────────────────────────────────────────────────────────────────
  applyTheme(localStorage.getItem('metiq_theme') || 'dark');
  applyLocale();
  setConnected(null, 'connecting…');
  loadCommandCatalog();
  connect();
})();
