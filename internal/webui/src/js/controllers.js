  function setRunActive(active, ownerSessionID) {
    if (active) {
      activeRun = true;
      activeRunSessionID = ownerSessionID || activeRunSessionID || sessionID || null;
    } else if (!ownerSessionID || !activeRunSessionID || ownerSessionID === activeRunSessionID) {
      activeRun = false;
      activeRunSessionID = null;
    }
    updateRunControls();
  }

  function selectedSessionHasActiveRun() {
    return !!(activeRun && activeRunSessionID && sessionID && activeRunSessionID === sessionID);
  }

  function updateRunControls() {
    const selectedActive = selectedSessionHasActiveRun();
    abortBtn.disabled = !connected || !selectedActive;
    compactBtn.disabled = !connected || !sessionID || selectedActive;
    clearBtn.disabled = !connected || !sessionID || selectedActive;
  }

  function setSidebarOpen(open) {
    document.body.classList.toggle('sidebar-open', !!open);
    mobileMenuBtn.setAttribute('aria-expanded', open ? 'true' : 'false');
    mobileMenuBtn.setAttribute('aria-label', open ? 'Close navigation' : 'Open navigation');
  }

  function closeSidebarOnMobile() {
    if (window.matchMedia && window.matchMedia('(max-width: 900px)').matches) setSidebarOpen(false);
  }

  function showThinking() {
    hideThinking();
    thinkingEl = document.createElement('div');
    thinkingEl.className = 'msg agent thinking';
    thinkingEl.innerHTML = '<span></span><span></span><span></span>';
    msgs.appendChild(thinkingEl);
    msgs.scrollTop = msgs.scrollHeight;
  }

  function hideThinking() {
    if (thinkingEl) { thinkingEl.remove(); thinkingEl = null; }
  }

  let typingIndicatorEl = null;
  let typingIndicatorTimer = null;
  function showTypingIndicator(who, typing) {
    if (typingIndicatorTimer) { clearTimeout(typingIndicatorTimer); typingIndicatorTimer = null; }
    if (!typing) {
      if (typingIndicatorEl) { typingIndicatorEl.remove(); typingIndicatorEl = null; }
      return;
    }
    if (!typingIndicatorEl) {
      typingIndicatorEl = document.createElement('div');
      typingIndicatorEl.className = 'sub';
      msgs.appendChild(typingIndicatorEl);
    }
    typingIndicatorEl.textContent = `${who} is typing…`;
    msgs.scrollTop = msgs.scrollHeight;
    typingIndicatorTimer = setTimeout(() => showTypingIndicator(who, false), 4000);
  }

  let lastTypingSentAt = 0;
  function sendTypingSignal(typing) {
    if (!connected || !sessionID) return;
    const now = Date.now();
    if (typing && now - lastTypingSentAt < 1500) return;
    lastTypingSentAt = typing ? now : 0;
    callSafe('session.typing', { sessionKey: sessionID, sessionId: sessionID, typing });
  }

  function setConnected(ok, label) {
    dot.className = 'status-dot' + (ok ? ' connected' : ok === false ? ' error' : '');
    connLabel.textContent = label;
    connected = !!ok;
    input.disabled = !ok;
    updateComposerState();
    updateRunControls();
    if (ok) input.focus();
  }

  // ── Streaming bubble management ───────────────────────────────────────────
  function startStreaming() {
    hideThinking();
    streamingText = '';
    streamingBubble = document.createElement('div');
    streamingBubble.className = 'msg agent streaming';
    streamingBubble.textContent = '';
    msgs.appendChild(streamingBubble);
    msgs.scrollTop = msgs.scrollHeight;
  }

  function appendChunk(text) {
    if (!streamingBubble) startStreaming();
    streamingText += text;
    streamingBubble.textContent = streamingText;
    msgs.scrollTop = msgs.scrollHeight;
  }

  function replaceStreaming(text) {
    if (!streamingBubble) startStreaming();
    streamingText = text || '';
    streamingBubble.textContent = streamingText;
    msgs.scrollTop = msgs.scrollHeight;
  }

  function chatMessageText(message) {
    if (!message) return '';
    if (typeof message.text === 'string') return message.text;
    if (!Array.isArray(message.content)) return '';
    return message.content.filter(part => part && part.type === 'text' && typeof part.text === 'string').map(part => part.text).join('');
  }

  function finalizeStreaming() {
    if (streamingBubble) {
      streamingBubble.classList.remove('streaming');
      // Keep bubble as a normal agent message.
      if (!streamingText.trim()) {
        streamingBubble.remove();
      } else {
        setMessageContent(streamingBubble, streamingText, 'agent');
      }
      streamingBubble = null;
      streamingText = '';
    }
    hideThinking();
  }

