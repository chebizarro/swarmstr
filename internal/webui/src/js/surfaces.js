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

  function taskIDOf(task) {
    return task && (task.task_id || task.taskId || task.id);
  }

  function renderTaskActivityCard() {
    const card = $('task-activity-card');
    if (!card) return;
    const host = card.querySelector('.mgmt-list-host');
    if (!host) return;
    host.innerHTML = '';
    if (!taskActivityEvents.length) {
      host.innerHTML = '<div class="sidebar-empty">No lifecycle activity observed on this connection yet.</div>';
      return;
    }
    taskActivityEvents.slice().reverse().forEach(item => {
      const payload = item.payload || {};
      const row = document.createElement('div'); row.className = 'mgmt-row activity-row';
      const correlation = payload.task_id || payload.taskId || payload.run_id || payload.runId || payload.session_id || payload.sessionKey || payload.session || '';
      const status = payload.status || payload.state || payload.action || payload.direction || '';
      row.innerHTML = `<strong>${escapeHTML(item.event)}</strong><div class="sub">${escapeHTML([status, correlation, new Date(item.at).toLocaleTimeString()].filter(Boolean).join(' · '))}</div>`;
      const details = document.createElement('details');
      const summary = document.createElement('summary'); summary.textContent = 'Payload'; details.appendChild(summary);
      const pre = document.createElement('pre'); pre.className = 'grant-detail'; pre.textContent = formatValue(payload); details.appendChild(pre); row.appendChild(details);
      host.appendChild(row);
    });
  }

  async function loadTaskDetail(taskID) {
    const card = $('task-detail-card');
    if (!card || !taskID) return;
    const host = card.querySelector('.mgmt-list-host');
    if (!host) return;
    host.innerHTML = '<div class="sidebar-empty">Loading task details…</div>';
    const calls = [];
    if (gatewayMethodAdvertised('tasks.get')) calls.push(callSafe('tasks.get', { task_id: taskID, runs_limit: 50 }).then(value => ['Task + runs', value]));
    if (gatewayMethodAdvertised('tasks.doctor')) calls.push(callSafe('tasks.doctor', { task_id: taskID }).then(value => ['Doctor', value]));
    if (gatewayMethodAdvertised('tasks.trace')) calls.push(callSafe('tasks.trace', { task_id: taskID, limit: 100 }).then(value => ['Trace', value]));
    const results = await Promise.all(calls);
    if (!document.body.contains(card)) return;
    host.innerHTML = '';
    if (!results.length) { host.innerHTML = '<div class="sidebar-empty">No task detail methods are advertised.</div>'; return; }
    results.forEach(([label, value]) => {
      const details = document.createElement('details'); details.open = label === 'Task + runs';
      const summary = document.createElement('summary'); summary.textContent = label; details.appendChild(summary);
      const pre = document.createElement('pre'); pre.className = 'grant-detail'; pre.textContent = formatValue(value); details.appendChild(pre); host.appendChild(details);
    });
  }

  async function loadTaskInventoryCard() {
    const card = $('task-inventory-card');
    if (!card) return;
    const host = card.querySelector('.mgmt-list-host');
    if (!host) return;
    if (!gatewayMethodAdvertised('tasks.list')) {
      host.innerHTML = '<div class="sidebar-empty">tasks.list is not advertised by this gateway.</div>';
      return;
    }
    host.innerHTML = '<div class="sidebar-empty">Loading task inventory…</div>';
    const res = await callSafe('tasks.list', { limit: 200 });
    if (!document.body.contains(card)) return;
    host.innerHTML = '';
    if (res.error) { host.innerHTML = `<div class="mgmt-error">${escapeHTML(res.error)}</div>`; return; }
    const tasks = res.tasks || res.items || [];
    if (!tasks.length) { host.innerHTML = '<div class="sidebar-empty">No tasks in the durable inventory.</div>'; return; }
    tasks.forEach(task => {
      const id = taskIDOf(task);
      const row = document.createElement('div'); row.className = 'mgmt-row task-row';
      const title = task.title || task.instructions || id || 'task';
      const status = task.status || 'unknown';
      const agent = task.assigned_agent || task.assignedAgent || '';
      const session = task.session_id || task.sessionId || '';
      const parent = task.parent_task_id || task.parentTaskId || '';
      row.innerHTML = `<div class="task-row-heading"><strong>${escapeHTML(truncate(title, 100))}</strong><span class="task-status ${escapeHTML(status)}">${escapeHTML(status)}</span></div><div class="sub">${escapeHTML([id, agent && 'agent ' + agent, session && 'session ' + truncate(session, 32), parent && 'child of ' + parent].filter(Boolean).join(' · '))}</div>`;
      if (id) {
        const actions = document.createElement('div'); actions.className = 'action-row';
        const details = document.createElement('button'); details.className = 'mini-btn'; details.textContent = 'Details'; details.addEventListener('click', () => loadTaskDetail(id)); actions.appendChild(details);
        row.appendChild(actions);
      }
      host.appendChild(row);
    });
  }

  function queueTaskInventoryRefresh() {
    if (mainView !== 'tasks') return;
    if (taskInventoryRefreshInFlight) { taskInventoryRefreshQueued = true; return; }
    taskInventoryRefreshInFlight = true;
    Promise.all([loadTaskInventoryCard(), Promise.resolve(renderTaskActivityCard())]).finally(() => {
      taskInventoryRefreshInFlight = false;
      if (taskInventoryRefreshQueued) {
        taskInventoryRefreshQueued = false;
        queueTaskInventoryRefresh();
      }
    });
  }

  function handleTaskLifecycleEvent(event, payload) {
    const relevant = ['task', 'task.suggestion', 'agent.status', 'chat', 'chat.message', 'tool.start', 'tool.progress', 'tool.result', 'tool.error', 'session.placement', 'sessions.changed', 'session.operation', 'session.tool'];
    if (!relevant.includes(event)) return;
    taskActivityEvents.push({ event, payload: payload || {}, at: Date.now() });
    if (taskActivityEvents.length > 60) taskActivityEvents = taskActivityEvents.slice(-60);
    renderTaskActivityCard();
    if (event !== 'tool.progress') queueTaskInventoryRefresh();
  }

  async function showTasksView(token) {
    const { grid } = beginManagementView('Tasks & Ops', 'Live task/subagent inventory, lifecycle activity, suggestions, pending questions, and attach grants.');
    if (!isViewCurrent('tasks', token)) return;

    const inventoryCard = addMgmtCard(grid, 'Task / subagent inventory');
    inventoryCard.id = 'task-inventory-card';
    const advertisedEvents = ['task', 'task.suggestion', 'agent.status', 'chat', 'chat.message', 'tool.start', 'tool.progress', 'tool.result', 'tool.error', 'session.placement', 'sessions.changed', 'session.operation', 'session.tool'].filter(gatewayEventAdvertised);
    const inventoryStatus = document.createElement('div'); inventoryStatus.className = 'sub';
    inventoryStatus.textContent = advertisedEvents.length ? `Live refresh events: ${advertisedEvents.join(', ')}` : 'No task/subagent lifecycle events are advertised; inventory is snapshot-only.';
    inventoryCard.appendChild(inventoryStatus);
    const inventoryHost = document.createElement('div'); inventoryHost.className = 'mgmt-list-host'; inventoryCard.appendChild(inventoryHost);

    const activityCard = addMgmtCard(grid, 'Live lifecycle activity');
    activityCard.id = 'task-activity-card';
    const activityHost = document.createElement('div'); activityHost.className = 'mgmt-list-host'; activityCard.appendChild(activityHost);

    const detailCard = addMgmtCard(grid, 'Task details / runs / trace');
    detailCard.id = 'task-detail-card'; detailCard.classList.add('mgmt-card-wide');
    const detailHost = document.createElement('div'); detailHost.className = 'mgmt-list-host'; detailHost.innerHTML = '<div class="sidebar-empty">Choose a task to inspect available details.</div>'; detailCard.appendChild(detailHost);

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

    renderTaskActivityCard();
    await Promise.all([loadTaskInventoryCard(), loadTaskSuggestionsCard(), loadPendingQuestionsCard()]);
  }

  // ── Boards view: widget frame host + ticket postMessage bridge ────────────
  // The parent page owns the WebSocket, so a sandboxed widget frame (opaque
  // origin) reaches the ticket-scoped gateway methods only by posting a
  // request to this window. The relay matches the message's source window to a
  // registered frame, injects THAT frame's view ticket (never a ticket from
  // widget code), calls the gateway, and posts the reply back to the frame.
  //   widget → host : { source:'metiq-board-widget', type:'request', id, method, params }
  //   host   → widget: { source:'metiq-board-host',  type:'response', id, ok, result|error }
  // method ∈ { 'prompt.authorize' | 'data.read' | 'action' | 'event' }.
  function boardBridgeDispatch(entry, msg) {
    const params = (msg && msg.params && typeof msg.params === 'object') ? msg.params : {};
    if (msg.method === 'prompt.authorize') {
      return callMethod('board.prompt.authorize', { ticket: entry.ticket });
    }
    if (msg.method === 'data.read') {
      return callMethod('board.data.read', { ticket: entry.ticket, bindingId: String(params.bindingId || ''), params: params.params || {} });
    }
    if (msg.method === 'action') {
      const call = { ticket: entry.ticket, action: String(params.action || '') };
      if (params.jobId) call.jobId = String(params.jobId);
      if (params.params) call.params = params.params;
      return callMethod('board.action', call);
    }
    if (msg.method === 'event') {
      return callMethod('board.event', { ticket: entry.ticket, payload: params.payload !== undefined ? params.payload : params });
    }
    return Promise.reject({ message: 'unknown board bridge method: ' + (msg && msg.method) });
  }

  async function onBoardWidgetMessage(event) {
    const msg = event.data;
    if (!msg || msg.source !== 'metiq-board-widget' || msg.type !== 'request' || typeof msg.id !== 'string') return;
    const entry = boardWidgetFrames.find(e => e.frame.contentWindow && e.frame.contentWindow === event.source);
    if (!entry || !entry.ticket) return; // unknown/foreign frame or no minted ticket
    const reply = (ok, payload) => {
      if (!event.source) return;
      const body = { source: 'metiq-board-host', type: 'response', id: msg.id, ok };
      if (ok) body.result = payload;
      else body.error = { message: (payload && payload.message) ? payload.message : String((payload && payload.error) || payload || 'request failed') };
      try { event.source.postMessage(body, '*'); } catch (e) { /* frame gone */ }
    };
    try { reply(true, await boardBridgeDispatch(entry, msg)); }
    catch (err) { reply(false, err); }
  }

  function installBoardBridge() {
    if (boardBridgeInstalled) return;
    boardBridgeInstalled = true;
    window.addEventListener('message', onBoardWidgetMessage);
  }

  // mcp.app.viewCreated fires when an MCP tool call mints an app view. Surface
  // it, and refresh the board when it targets the session on screen so a
  // freshly interactive mcp-app widget can be re-minted.
  function handleMcpAppViewCreated(payload) {
    if (!payload) return;
    const origin = [payload.serverName, payload.toolName].filter(Boolean).join('/');
    addMsg('MCP app view created' + (origin ? ' (' + origin + ')' : '') + (payload.viewId ? ' · ' + payload.viewId : ''), 'system');
    if (mainView === 'boards' && boardsSessionKey && payload.sessionKey === boardsSessionKey) {
      if (boardsRefreshTimer) clearTimeout(boardsRefreshTimer);
      boardsRefreshTimer = setTimeout(() => { if (mainView === 'boards') loadBoardSnapshot(); }, 400);
    }
  }

  function handleBoardChanged(payload) {
    if (!payload || mainView !== 'boards') return;
    if (boardsSessionKey && payload.sessionKey !== boardsSessionKey) return;
    if (boardsRefreshTimer) clearTimeout(boardsRefreshTimer);
    boardsRefreshTimer = setTimeout(() => { if (mainView === 'boards') loadBoardSnapshot(); }, 400);
  }

  // board.widget.grant approval UX for pending widgets: show the declared
  // capability surface (tools + net origins) and let the operator grant or
  // reject at the widget's exact revision/instance.
  function renderBoardWidgetGrant(row, widget) {
    const caps = (widget.declaredSummary && widget.declaredSummary.length)
      ? widget.declaredSummary
      : ((widget.declared && widget.declared.tools) || []);
    const origins = (widget.declared && widget.declared.netOrigins) || [];
    const desc = document.createElement('div'); desc.className = 'sub';
    desc.textContent = 'Requests: ' + (caps.length ? caps.join(', ') : 'no tools') + (origins.length ? ' · origins: ' + origins.join(', ') : '');
    row.appendChild(desc);
    const actions = document.createElement('div'); actions.className = 'action-row';
    const decide = async (decision, btn, other) => {
      btn.disabled = true; other.disabled = true;
      const res = await callSafe('board.widget.grant', { sessionKey: boardsSessionKey, name: widget.name, decision, revision: widget.revision, instanceId: widget.instanceId });
      if (res.error) { btn.textContent = truncate(res.error, 20); btn.disabled = false; other.disabled = false; return; }
      loadBoardSnapshot();
    };
    const grant = document.createElement('button'); grant.className = 'mini-btn'; grant.textContent = 'Grant';
    const reject = document.createElement('button'); reject.className = 'mini-btn'; reject.textContent = 'Reject';
    grant.addEventListener('click', () => decide('granted', grant, reject));
    reject.addEventListener('click', () => decide('rejected', reject, grant));
    actions.append(grant, reject); row.appendChild(actions);
  }

  // mcp-app widgets carry no frame in board.get; re-mint an interactive view
  // via board.widget.appView (read-only unless the pinned view was interactive
  // and granted). Re-minting also refreshes a view gone stale after a re-put.
  function renderBoardWidgetAppView(row, widget) {
    const status = document.createElement('div'); status.className = 'sub';
    status.textContent = 'MCP App widget — mint an interactive view.';
    row.appendChild(status);
    const actions = document.createElement('div'); actions.className = 'action-row';
    const mint = document.createElement('button'); mint.className = 'mini-btn'; mint.textContent = 'Mint / re-mint app view';
    mint.addEventListener('click', async () => {
      mint.disabled = true; status.textContent = 'Minting…';
      const res = await callSafe('board.widget.appView', { sessionKey: boardsSessionKey, name: widget.name, revision: widget.revision, instanceId: widget.instanceId });
      mint.disabled = false;
      if (res.error) { status.textContent = 'appView failed: ' + res.error; return; }
      status.textContent = 'View ' + truncate(res.viewId || '', 32) + ' · expires ' + (res.expiresAtMs ? new Date(res.expiresAtMs).toLocaleTimeString() : '—');
    });
    actions.appendChild(mint); row.appendChild(actions);
  }

  function renderBoardWidgetFrame(row, widget) {
    installBoardBridge();
    const frame = document.createElement('iframe');
    frame.className = 'board-frame';
    frame.setAttribute('sandbox', 'allow-scripts');
    frame.src = widget.frameUrl;
    frame.title = widget.title || widget.name;
    row.appendChild(frame);
    boardWidgetFrames.push({ frame, name: widget.name, ticket: widget.viewTicket, revision: widget.revision, instanceId: widget.instanceId });
  }

  async function loadBoardSnapshot() {
    const card = $('board-snapshot-card');
    if (!card || !boardsSessionKey) return;
    const host = card.querySelector('.mgmt-list-host');
    if (!host) return;
    const res = await callSafe('board.get', { sessionKey: boardsSessionKey });
    // Rebuild the frame→ticket registry from the fresh snapshot; stale entries
    // (old tickets, removed frames) must never linger in the relay.
    boardWidgetFrames = [];
    if (boardsTicketTimer) { clearTimeout(boardsTicketTimer); boardsTicketTimer = null; }
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
    let minTicketTtl = 0;
    widgets.forEach(widget => {
      const row = document.createElement('div');
      row.className = 'board-widget';
      const title = document.createElement('div');
      title.innerHTML = `<strong>${escapeHTML(widget.title || widget.name)}</strong> <span class="sub">${escapeHTML(widget.contentKind || '')} · grant: ${escapeHTML(widget.grantState || 'none')}</span>`;
      row.appendChild(title);
      if (widget.grantState === 'pending') {
        renderBoardWidgetGrant(row, widget);
      } else if (widget.grantState === 'rejected') {
        const note = document.createElement('div'); note.className = 'sub'; note.textContent = 'Grant rejected — no view is rendered.'; row.appendChild(note);
      } else if (widget.contentKind === 'mcp-app') {
        renderBoardWidgetAppView(row, widget);
      } else if (widget.frameUrl) {
        renderBoardWidgetFrame(row, widget);
        if (widget.viewTicketTtlMs && (!minTicketTtl || widget.viewTicketTtlMs < minTicketTtl)) minTicketTtl = widget.viewTicketTtlMs;
      } else {
        const note = document.createElement('div');
        note.className = 'sub';
        note.textContent = 'No renderable view for this widget kind.';
        row.appendChild(note);
      }
      host.appendChild(row);
    });
    // View tickets expire on their own TTL; refresh the snapshot before the
    // earliest one lapses so bridged calls keep resolving without a manual
    // reload (board.changed already covers re-put/grant churn).
    if (minTicketTtl > 0) {
      const delay = Math.max(15000, minTicketTtl - 15000);
      boardsTicketTimer = setTimeout(() => { if (mainView === 'boards') loadBoardSnapshot(); }, delay);
    }
  }

  async function showBoardsView(token) {
    const { grid } = beginManagementView('Boards', 'Session boards: sandboxed widget frames bridged to the gateway ticket methods, mcp-app views, and grant approvals.');
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
