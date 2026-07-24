  // ── Approval queue management ─────────────────────────────────────────────
  function enqueueApproval(payloadOrID, command, kind) {
    const payload = typeof payloadOrID === 'object' && payloadOrID !== null
      ? Object.assign({}, payloadOrID)
      : { id: payloadOrID, command };
    payload.kind = payload.kind || payload.approval_kind || kind || 'exec';
    payload.id = payload.id || payload.request_id || payload.approval_id;
    if (!payload.id) return;
    if (approvalQueue.some(a => a.id === payload.id)) return;
    approvalQueue.push(payload);
    updateApprovalBadge();
    if (!currentApprovalID) showNextApproval();
  }

  function normalizePendingApproval(raw, kind) {
    if (!raw || typeof raw !== 'object') return null;
    const approval = Object.assign({}, raw.presentation || {}, raw);
    approval.id = approval.id || approval.request_id || approval.approval_id;
    approval.kind = approval.kind || approval.approval_kind || kind || 'exec';
    approval.status = approval.status || 'pending';
    approval.expires_at = Number(approval.expires_at || approval.expiresAtMs || 0);
    approval.requested_at = Number(approval.requested_at || approval.createdAtMs || 0);
    return approval.id ? approval : null;
  }

  function approvalSortTime(approval) {
    return Number(approval.requested_at || approval.createdAtMs || approval.expires_at || approval.expiresAtMs || 0);
  }

  async function reconcilePendingApprovals() {
    const snapshot = await callMethod('approval.list', {});
    const authoritative = Array.isArray(snapshot)
      ? snapshot
      : ((snapshot && (snapshot.pending || snapshot.approvals || snapshot.items)) || []);
    const now = Date.now();
    const merged = new Map();
    authoritative.forEach(item => {
      const approval = normalizePendingApproval(item);
      if (!approval || approval.status !== 'pending') return;
      if (approval.expires_at && approval.expires_at <= now) return;
      merged.set(approval.id, approval);
    });
    approvalQueue = Array.from(merged.values()).sort((a, b) => {
      const byTime = approvalSortTime(a) - approvalSortTime(b);
      return byTime || String(a.id).localeCompare(String(b.id));
    });
    if (currentApprovalID && !merged.has(currentApprovalID)) {
      currentApprovalID = null;
      currentApproval = null;
    }
    updateApprovalBadge();
    showNextApproval();
  }

  function updateApprovalBadge() {
    approvalsBadge.style.display = approvalQueue.length ? '' : 'none';
    approvalsBadge.textContent = approvalQueue.length > 1
      ? `⚠ ${approvalQueue.length} approvals pending`
      : t('approvalPending');
    approvalQueueCount.textContent = `queue: ${approvalQueue.length}`;
  }

  function showNextApproval() {
    if (approvalCountdownTimer) {
      clearInterval(approvalCountdownTimer);
      approvalCountdownTimer = null;
    }
    if (!approvalQueue.length) {
      currentApprovalID = null;
      currentApproval = null;
      approvalModal.classList.remove('visible');
      approvalsBadge.style.display = 'none';
      return;
    }
    const a = approvalQueue[0];
    currentApprovalID = a.id;
    currentApproval = a;
    approvalError.classList.remove('visible');
    approvalError.textContent = '';
    setApprovalBusy(false);
    renderApproval(a);
    const alwaysBtn = $('approval-approve-always');
    if (alwaysBtn) alwaysBtn.disabled = !a.allow_always_available;
    approvalModal.classList.add('visible');
  }

  function renderApproval(a) {
    updateApprovalBadge();
    const isPlugin = a.kind === 'plugin';
    const isSystem = a.kind === 'system';
    approvalTitle.textContent = isPlugin ? '⚠ Plugin Approval Required' : (isSystem ? '⚠ System Approval Required' : '⚠ Exec Approval Required');
    approvalSubtitle.textContent = isPlugin
      ? 'Review plugin identity, permissions, and requested operation before deciding:'
      : (isSystem ? 'Review the system operation before deciding:' : 'Review the command metadata before deciding:');
    approvalIDLabel.textContent = 'id: ' + truncate(a.id, 28);
    approvalCmd.innerHTML = isPlugin ? escapeHTML(resolveApprovalCommand(a)) : highlightCommand(resolveApprovalCommand(a));
    approvalMeta.innerHTML = '';
    [
      ['Type', a.kind || 'exec'],
      ['Plugin', a.plugin_id || a.pluginId || a.name],
      ['Operation', a.operation || a.action || a.requested_action],
      ['Permissions', a.permissions || a.requested_permissions || a.scopes],
      ['Agent', a.agent_id || a.agentId || (a.system_run_plan && a.system_run_plan.agentId)],
      ['Session', a.session_id || a.sessionKey || (a.system_run_plan && a.system_run_plan.sessionKey)],
      ['CWD', a.cwd || (a.system_run_plan && a.system_run_plan.cwd)],
      ['Host', a.host],
      ['Node', a.node_id || a.nodeId],
      ['Summary', a.analysis_summary],
      ['Warnings', a.analysis_warnings],
      ['Signature', a.analysis_signature],
      ['Mode', a.approval_mode],
      ['Allow always', a.allow_always_available ? (a.allow_always_reason || 'available') : (a.allow_always_reason || 'not available')],
    ].forEach(([label, value]) => addApprovalMeta(label, value || '—'));
    updateApprovalCountdown(a);
    if (a.expires_at) {
      approvalCountdownTimer = setInterval(() => updateApprovalCountdown(a), 1000);
    }
  }

  function addApprovalMeta(label, value) {
    const dt = document.createElement('dt');
    const dd = document.createElement('dd');
    dt.textContent = label;
    dd.textContent = typeof value === 'object' ? formatValue(value) : String(value || '—');
    approvalMeta.appendChild(dt);
    approvalMeta.appendChild(dd);
  }

  function resolveApprovalCommand(a) {
    if (a.kind === 'plugin') {
      return a.summary || a.reason || a.description || formatValue({
        plugin: a.plugin_id || a.pluginId || a.name,
        operation: a.operation || a.action,
        permissions: a.permissions || a.requested_permissions || a.scopes,
      });
    }
    if (a.system_run_plan) {
      return a.system_run_plan.commandPreview || a.system_run_plan.commandText || (a.system_run_plan.argv || []).join(' ');
    }
    if (Array.isArray(a.command_argv) && a.command_argv.length) return a.command_argv.join(' ');
    return a.command || a.id;
  }

  function highlightCommand(command) {
    return escapeHTML(command || '').replace(/("[^"]*"|'[^']*'|\s--?[^\s]+|(?:^|\s)(?:\.\.?\/|\/)[^\s]+)/g, token => {
      const leading = token.match(/^\s/) ? token[0] : '';
      const body = leading ? token.slice(1) : token;
      let cls = 'cmd-token';
      if (body.startsWith('-')) cls = 'cmd-flag';
      else if (body.startsWith('"') || body.startsWith("'")) cls = 'cmd-string';
      else if (body.startsWith('/') || body.startsWith('./') || body.startsWith('../')) cls = 'cmd-path';
      return leading + `<span class="${cls}">${body}</span>`;
    });
  }

  function updateApprovalCountdown(a) {
    if (!a.expires_at) {
      approvalCountdown.textContent = 'expires: —';
      return;
    }
    const remaining = Math.max(0, a.expires_at - Date.now());
    approvalCountdown.textContent = 'expires: ' + Math.ceil(remaining / 1000) + 's';
  }

  function setApprovalBusy(busy) {
    approvalButtons.forEach(btn => { btn.disabled = busy; });
    if (!busy && currentApproval) {
      const alwaysBtn = $('approval-approve-always');
      if (alwaysBtn) alwaysBtn.disabled = !currentApproval.allow_always_available;
    }
  }

  async function resolveApproval(decision) {
    if (!currentApprovalID) return;
    const id = currentApprovalID;
    setApprovalBusy(true);
    approvalError.classList.remove('visible');
    approvalError.textContent = '';
    try {
      const approval = currentApproval || approvalQueue.find(a => a.id === id) || {};
      const normalizedDecision = decision === 'approve_always'
        ? 'allow-always'
        : (decision === 'approve' ? 'allow-once' : 'deny');
      const params = { id, kind: approval.kind || 'exec', decision: normalizedDecision };
      if (approval.plugin_id || approval.pluginId) params.plugin_id = approval.plugin_id || approval.pluginId;
      await callMethod('approval.resolve', params);
      approvalQueue = approvalQueue.filter(a => a.id !== id);
      currentApprovalID = null;
      currentApproval = null;
      approvalModal.classList.remove('visible');
      showNextApproval();
    } catch (e) {
      approvalError.textContent = 'Could not resolve approval: ' + (e && e.message ? e.message : 'request failed');
      approvalError.classList.add('visible');
      setApprovalBusy(false);
    }
  }

  $('approval-approve-once').addEventListener('click', () => resolveApproval('approve'));
  $('approval-approve-always').addEventListener('click', () => resolveApproval('approve_always'));
  $('approval-deny').addEventListener('click', () => resolveApproval('deny'));

  msgs.addEventListener('click', async e => {
    const btn = e.target.closest('.copy-code-btn');
    if (!btn) return;
    const pre = btn.closest('pre');
    const code = pre && pre.querySelector('code');
    if (!code) return;
    try {
      await navigator.clipboard.writeText(code.textContent || '');
      btn.textContent = 'Copied';
      setTimeout(() => { btn.textContent = 'Copy'; }, 1200);
    } catch {
      btn.textContent = 'Failed';
      setTimeout(() => { btn.textContent = 'Copy'; }, 1200);
    }
  });

