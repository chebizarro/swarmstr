  // ── Question queue (agent pose-and-block flow) ────────────────────────────
  // Mirrors the exec-approval queue UX: question.requested enqueues, the
  // wizard modal answers via question.resolve, and question.list reconciles
  // the queue on every (re)connect.
  function enqueueQuestion(record) {
    if (!record || !record.id) return;
    if (record.status && record.status !== 'pending') return;
    if (questionQueue.some(q => q.id === record.id)) return;
    questionQueue.push(record);
    updateQuestionBadge();
    if (!currentQuestionID) showNextQuestion();
  }

  function dropQuestion(id) {
    questionQueue = questionQueue.filter(q => q.id !== id);
    if (currentQuestionID === id) {
      currentQuestionID = null;
      showNextQuestion();
    }
    updateQuestionBadge();
  }

  async function reconcilePendingQuestions() {
    const res = await callSafe('question.list', {});
    if (res.error) return;
    const now = Date.now();
    questionQueue = (res.questions || [])
      .filter(q => q && q.id && q.status === 'pending' && (!q.expiresAtMs || q.expiresAtMs > now))
      .sort((a, b) => ((a.createdAtMs || 0) - (b.createdAtMs || 0)) || String(a.id).localeCompare(String(b.id)));
    if (currentQuestionID && !questionQueue.some(q => q.id === currentQuestionID)) currentQuestionID = null;
    updateQuestionBadge();
    showNextQuestion();
  }

  function updateQuestionBadge() {
    questionsBadge.style.display = questionQueue.length ? '' : 'none';
    questionsBadge.textContent = questionQueue.length > 1
      ? `? ${questionQueue.length} questions pending`
      : '? question pending';
  }

  function showNextQuestion() {
    if (questionCountdownTimer) {
      clearInterval(questionCountdownTimer);
      questionCountdownTimer = null;
    }
    if (!questionQueue.length) {
      currentQuestionID = null;
      questionModal.classList.remove('visible');
      updateQuestionBadge();
      return;
    }
    renderQuestionModal(questionQueue[0]);
  }

  function openQuestion(id) {
    const idx = questionQueue.findIndex(q => q.id === id);
    if (idx < 0) return;
    if (idx > 0) questionQueue.unshift(questionQueue.splice(idx, 1)[0]);
    showNextQuestion();
  }

  function updateQuestionCountdown(record) {
    if (!record.expiresAtMs) {
      questionCountdown.textContent = 'expires: —';
      return;
    }
    const remaining = Math.max(0, record.expiresAtMs - Date.now());
    questionCountdown.textContent = 'expires: ' + Math.ceil(remaining / 1000) + 's';
    if (remaining <= 0) dropQuestion(record.id);
  }

  function renderQuestionModal(record) {
    currentQuestionID = record.id;
    questionError.classList.remove('visible');
    questionError.textContent = '';
    questionQueueCount.textContent = `queue: ${questionQueue.length}`;
    questionIDLabel.textContent = 'id: ' + truncate(record.id, 28);
    const origin = [record.agentId ? 'agent ' + record.agentId : '', record.sessionKey ? 'session ' + truncate(record.sessionKey, 32) : ''].filter(Boolean).join(' · ');
    questionSubtitle.textContent = origin ? 'Asked by ' + origin + ':' : 'An agent is blocked on your answer:';
    questionBody.innerHTML = '';
    (record.questions || []).forEach(q => {
      const fieldset = document.createElement('fieldset');
      fieldset.className = 'question-fieldset';
      fieldset.dataset.questionId = q.questionId;
      fieldset.dataset.multiSelect = q.multiSelect ? '1' : '';
      const legend = document.createElement('legend');
      legend.textContent = q.header || q.questionId;
      fieldset.appendChild(legend);
      const text = document.createElement('div');
      text.className = 'question-text';
      text.textContent = q.question;
      fieldset.appendChild(text);
      const options = q.options || [];
      options.forEach((option, i) => {
        const label = document.createElement('label');
        label.className = 'question-option';
        const inputEl = document.createElement('input');
        inputEl.type = q.multiSelect ? 'checkbox' : 'radio';
        inputEl.name = 'question-' + record.id + '-' + q.questionId;
        inputEl.value = option.label;
        if (!q.multiSelect && i === 0) inputEl.checked = false;
        label.appendChild(inputEl);
        const span = document.createElement('span');
        span.textContent = option.label + (option.description ? ' — ' + option.description : '');
        label.appendChild(span);
        fieldset.appendChild(label);
      });
      if (!options.length || q.isOther) {
        const other = document.createElement('input');
        other.type = 'text';
        other.className = 'mini-input question-other';
        other.placeholder = options.length ? 'Other…' : 'Type your answer…';
        fieldset.appendChild(other);
      }
      questionBody.appendChild(fieldset);
    });
    updateQuestionCountdown(record);
    if (record.expiresAtMs) {
      questionCountdownTimer = setInterval(() => updateQuestionCountdown(record), 1000);
    }
    questionModal.classList.add('visible');
  }

  function collectQuestionAnswers() {
    const answers = {};
    let missing = null;
    questionBody.querySelectorAll('fieldset.question-fieldset').forEach(fieldset => {
      const qid = fieldset.dataset.questionId;
      const values = [];
      fieldset.querySelectorAll('input[type=radio]:checked, input[type=checkbox]:checked').forEach(inputEl => values.push(inputEl.value));
      const other = fieldset.querySelector('.question-other');
      if (other && other.value.trim()) values.push(other.value.trim());
      if (!values.length && !missing) missing = qid;
      answers[qid] = values;
    });
    return { answers, missing };
  }

  function setQuestionBusy(busy) {
    [questionSubmitBtn, questionCancelBtn, questionLaterBtn].forEach(btn => { btn.disabled = busy; });
  }

  async function resolveCurrentQuestion(cancel) {
    if (!currentQuestionID) return;
    const id = currentQuestionID;
    const params = { id };
    if (cancel) {
      params.cancel = true;
    } else {
      const collected = collectQuestionAnswers();
      if (collected.missing) {
        questionError.textContent = 'Answer "' + collected.missing + '" before submitting.';
        questionError.classList.add('visible');
        return;
      }
      params.answers = { answers: collected.answers };
    }
    setQuestionBusy(true);
    questionError.classList.remove('visible');
    try {
      await callMethod('question.resolve', params);
      dropQuestion(id);
    } catch (e) {
      questionError.textContent = 'Could not resolve question: ' + (e && e.message ? e.message : 'request failed');
      questionError.classList.add('visible');
    } finally {
      setQuestionBusy(false);
    }
  }

  questionSubmitBtn.addEventListener('click', () => resolveCurrentQuestion(false));
  questionCancelBtn.addEventListener('click', () => resolveCurrentQuestion(true));
  questionLaterBtn.addEventListener('click', () => {
    currentQuestionID = null;
    questionModal.classList.remove('visible');
  });
  questionsBadge.addEventListener('click', showNextQuestion);

  // ── Tasks view: suggestions, pending questions, attach grants ────────────
  function handleTaskSuggestionEvent(payload) {
    if (!payload) return;
    if (mainView === 'tasks') loadTaskSuggestionsCard();
  }

  async function loadTaskSuggestionsCard() {
    const card = $('task-suggestions-card');
    if (!card) return;
    const host = card.querySelector('.mgmt-list-host');
    if (!host) return;
    const res = await callSafe('taskSuggestions.list', {});
    host.innerHTML = '';
    if (res.error) { host.innerHTML = `<div class="sidebar-empty">${escapeHTML(res.error)}</div>`; return; }
    const suggestions = res.suggestions || [];
    if (!suggestions.length) { host.innerHTML = '<div class="sidebar-empty">No pending suggestions</div>'; return; }
    suggestions.forEach(suggestion => {
      const row = document.createElement('div');
      row.className = 'mgmt-row';
      row.innerHTML = `<strong>${escapeHTML(truncate(suggestion.title, 80))}</strong><div class="sub">${escapeHTML(truncate(suggestion.tldr || suggestion.prompt || '', 160))}</div>`;
      const actions = document.createElement('div');
      actions.className = 'action-row';
      const accept = document.createElement('button');
      accept.className = 'mini-btn';
      accept.textContent = 'Accept';
      accept.addEventListener('click', async () => {
        accept.disabled = true;
        const result = await callSafe('taskSuggestions.accept', { task_id: suggestion.id });
        if (result.error) { accept.textContent = result.error; accept.disabled = false; return; }
        addMsg('Accepted task suggestion → session ' + (result.key || '?'), 'system');
        loadTaskSuggestionsCard();
        loadSessions();
      });
      actions.appendChild(accept);
      const dismiss = document.createElement('button');
      dismiss.className = 'mini-btn';
      dismiss.textContent = 'Dismiss';
      dismiss.addEventListener('click', async () => {
        dismiss.disabled = true;
        await callSafe('taskSuggestions.dismiss', { task_id: suggestion.id });
        loadTaskSuggestionsCard();
      });
      actions.appendChild(dismiss);
      row.appendChild(actions);
      host.appendChild(row);
    });
  }

  async function loadPendingQuestionsCard() {
    const card = $('pending-questions-card');
    if (!card) return;
    const host = card.querySelector('.mgmt-list-host');
    if (!host) return;
    await reconcilePendingQuestions();
    host.innerHTML = '';
    if (!questionQueue.length) { host.innerHTML = '<div class="sidebar-empty">No pending questions</div>'; return; }
    questionQueue.forEach(record => {
      const row = document.createElement('div');
      row.className = 'mgmt-row';
      const first = (record.questions && record.questions[0]) || {};
      row.innerHTML = `<strong>${escapeHTML(truncate(first.question || record.id, 90))}</strong><div class="sub">${escapeHTML(record.agentId || '')} ${escapeHTML(record.sessionKey ? '· ' + truncate(record.sessionKey, 40) : '')}</div>`;
      const actions = document.createElement('div');
      actions.className = 'action-row';
      const answer = document.createElement('button');
      answer.className = 'mini-btn';
      answer.textContent = 'Answer';
      answer.addEventListener('click', () => openQuestion(record.id));
      actions.appendChild(answer);
      row.appendChild(actions);
      host.appendChild(row);
    });
  }

  function renderAttachGrantRow(host, grant) {
    const row = document.createElement('div');
    row.className = 'mgmt-row';
    row.innerHTML = `<strong>${escapeHTML(truncate(grant.token, 30))}</strong><div class="sub">session ${escapeHTML(truncate(grant.sessionKey, 40))} · expires ${new Date(grant.expiresAtMs).toLocaleTimeString()}</div>`;
    const actions = document.createElement('div');
    actions.className = 'action-row';
    const revoke = document.createElement('button');
    revoke.className = 'mini-btn';
    revoke.textContent = 'Revoke';
    revoke.addEventListener('click', async () => {
      revoke.disabled = true;
      const res = await callSafe('attach.revoke', { token: grant.token });
      revoke.textContent = res.error ? res.error : (res.revoked ? 'Revoked' : 'Already gone');
      setTimeout(() => row.remove(), 900);
      mintedAttachGrants = mintedAttachGrants.filter(g => g.token !== grant.token);
    });
    actions.appendChild(revoke);
    row.appendChild(actions);
    host.appendChild(row);
  }

  async function showTasksView(token) {
    const { grid } = beginManagementView('Tasks & Ops', 'Model-suggested follow-up tasks, pending agent questions, and MCP attach grants.');
    if (!isViewCurrent('tasks', token)) return;

    const suggestionsCard = addMgmtCard(grid, 'Task suggestions');
    suggestionsCard.id = 'task-suggestions-card';
    const sHost = document.createElement('div');
    sHost.className = 'mgmt-list-host';
    suggestionsCard.appendChild(sHost);

    const questionsCard = addMgmtCard(grid, 'Pending questions');
    questionsCard.id = 'pending-questions-card';
    const qHost = document.createElement('div');
    qHost.className = 'mgmt-list-host';
    questionsCard.appendChild(qHost);

    const grantsCard = addMgmtCard(grid, 'Attach grants (MCP loopback)');
    const grantStatus = document.createElement('div');
    grantStatus.className = 'sub';
    grantStatus.textContent = 'Mint a scoped bearer token so an external MCP client can attach to one session.';
    grantsCard.appendChild(grantStatus);
    const mintRow = document.createElement('div');
    mintRow.className = 'action-row';
    const grantSession = document.createElement('input');
    grantSession.className = 'mini-input';
    grantSession.placeholder = 'session key';
    if (sessionID) grantSession.value = sessionID;
    mintRow.appendChild(grantSession);
    const grantTTL = document.createElement('input');
    grantTTL.className = 'mini-input';
    grantTTL.placeholder = 'TTL minutes (default 60)';
    mintRow.appendChild(grantTTL);
    const mint = document.createElement('button');
    mint.className = 'mini-btn';
    mint.textContent = 'Mint grant';
    mintRow.appendChild(mint);
    grantsCard.appendChild(mintRow);
    const grantsHost = document.createElement('div');
    grantsHost.className = 'mgmt-list-host';
    grantsCard.appendChild(grantsHost);
    const grantDetail = document.createElement('pre');
    grantDetail.className = 'grant-detail';
    grantDetail.style.display = 'none';
    grantsCard.appendChild(grantDetail);
    mint.addEventListener('click', async () => {
      const key = grantSession.value.trim();
      if (!key) { grantStatus.textContent = 'A session key is required to mint a grant.'; return; }
      mint.disabled = true;
      const minutes = parseInt(grantTTL.value, 10);
      const params = { sessionKey: key };
      if (minutes > 0) params.ttl_ms = minutes * 60 * 1000;
      const res = await callSafe('attach.grant', params);
      mint.disabled = false;
      if (res.error) { grantStatus.textContent = 'Mint failed: ' + res.error; return; }
      grantStatus.textContent = 'Grant minted; hand METIQ_MCP_TOKEN and the MCP config below to the attaching client.';
      mintedAttachGrants.push(res);
      renderAttachGrantRow(grantsHost, res);
      grantDetail.style.display = '';
      grantDetail.textContent = formatValue({ token: res.token, expiresAtMs: res.expiresAtMs, env: res.env, mcpConfig: res.mcpConfig });
    });
    mintedAttachGrants.forEach(grant => renderAttachGrantRow(grantsHost, grant));

    await Promise.all([loadTaskSuggestionsCard(), loadPendingQuestionsCard()]);
  }

  // ── Boards view: widget frame host ────────────────────────────────────────
  function handleBoardChanged(payload) {
    if (!payload || mainView !== 'boards') return;
    if (boardsSessionKey && payload.sessionKey !== boardsSessionKey) return;
    if (boardsRefreshTimer) clearTimeout(boardsRefreshTimer);
    boardsRefreshTimer = setTimeout(() => { if (mainView === 'boards') loadBoardSnapshot(); }, 400);
  }

  async function loadBoardSnapshot() {
    const card = $('board-snapshot-card');
    if (!card || !boardsSessionKey) return;
    const host = card.querySelector('.mgmt-list-host');
    if (!host) return;
    const res = await callSafe('board.get', { sessionKey: boardsSessionKey });
    host.innerHTML = '';
    if (res.error) { host.innerHTML = `<div class="sidebar-empty">${escapeHTML(res.error)}</div>`; return; }
    const header = document.createElement('div');
    header.className = 'sub';
    header.textContent = 'Revision ' + (res.revision || 0) + ' · ' + ((res.tabs || []).length) + ' tab(s) · ' + ((res.widgets || []).length) + ' widget(s)';
    host.appendChild(header);
    const widgets = res.widgets || [];
    if (!widgets.length) {
      const empty = document.createElement('div');
      empty.className = 'sidebar-empty';
      empty.textContent = 'No widgets pinned to this session board yet.';
      host.appendChild(empty);
      return;
    }
    widgets.forEach(widget => {
      const row = document.createElement('div');
      row.className = 'board-widget';
      const title = document.createElement('div');
      title.innerHTML = `<strong>${escapeHTML(widget.title || widget.name)}</strong> <span class="sub">${escapeHTML(widget.contentKind || '')} · grant: ${escapeHTML(widget.grantState || 'none')}</span>`;
      row.appendChild(title);
      if (widget.frameUrl) {
        const frame = document.createElement('iframe');
        frame.className = 'board-frame';
        frame.setAttribute('sandbox', 'allow-scripts');
        frame.src = widget.frameUrl;
        frame.title = widget.title || widget.name;
        row.appendChild(frame);
      } else {
        const note = document.createElement('div');
        note.className = 'sub';
        note.textContent = 'No renderable view for this widget kind.';
        row.appendChild(note);
      }
      host.appendChild(row);
    });
  }

  async function showBoardsView(token) {
    const { grid } = beginManagementView('Boards', 'Session boards: pinned widgets rendered through the sandboxed frame host.');
    const sessionsRes = await callSafe('sessions.list', { limit: 100 });
    if (!isViewCurrent('boards', token)) return;
    const pickerCard = addMgmtCard(grid, 'Session');
    const pickRow = document.createElement('div');
    pickRow.className = 'action-row';
    const select = document.createElement('select');
    select.className = 'mini-input';
    const sessions = resultItems(sessionsRes, 'sessions');
    sessions.forEach(sess => {
      const sid = sessionIDOf(sess);
      if (!sid) return;
      const option = document.createElement('option');
      option.value = sid;
      option.textContent = truncate(sid, 48);
      select.appendChild(option);
    });
    if (!boardsSessionKey && sessionID) boardsSessionKey = sessionID;
    if (!boardsSessionKey && sessions.length) boardsSessionKey = sessionIDOf(sessions[0]);
    if (boardsSessionKey) select.value = boardsSessionKey;
    pickRow.appendChild(select);
    const load = document.createElement('button');
    load.className = 'mini-btn';
    load.textContent = 'Load board';
    load.addEventListener('click', () => { boardsSessionKey = select.value; loadBoardSnapshot(); });
    pickRow.appendChild(load);
    pickerCard.appendChild(pickRow);

    const boardCard = addMgmtCard(grid, 'Board');
    boardCard.id = 'board-snapshot-card';
    boardCard.classList.add('board-card');
    const host = document.createElement('div');
    host.className = 'mgmt-list-host';
    boardCard.appendChild(host);
    if (boardsSessionKey) await loadBoardSnapshot();
  }
