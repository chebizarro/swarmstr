  // ── Gateway WS protocol ───────────────────────────────────────────────────
  function sendFrame(frame) {
    if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(frame));
  }

  function callMethod(method, params) {
    return new Promise((resolve, reject) => {
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject({ message: 'gateway is not connected' });
        return;
      }
      const descriptor = gatewayMethodDescriptors.get(method);
      if (descriptor && descriptor.scope && descriptor.scope !== 'dynamic' && descriptor.scope !== 'node') {
        const allowed = gatewayScopes.has(descriptor.scope) || (descriptor.scope === 'operator.read' && gatewayScopes.has('operator.write'));
        if (!allowed) {
          reject({ message: 'missing scope: ' + descriptor.scope });
          return;
        }
      }
      const id = nextID();
      pendingResolvers[id] = { resolve, reject };
      sendFrame({ type: 'request', id, method, params: params || {} });
    });
  }

  // ── WS event dispatcher ───────────────────────────────────────────────────
  function shouldBufferLiveEvent(event, payload) {
    if (mainView !== 'chat' || !historyLoadingSessionID || !payload) return false;
    const eventSession = event === 'chat' ? payload.sessionKey : (payload.session_id || payload.session);
    if (eventSession !== historyLoadingSessionID) return false;
    return event === 'chat' || event === 'agent.status' || event === 'chat.message' ||
      event === 'tool.start' || event === 'tool.progress' || event === 'tool.result' || event === 'tool.error';
  }

  function onEvent(event, payload) {
    if (shouldBufferLiveEvent(event, payload)) {
      bufferedLiveEvents.push({ event, payload });
      return;
    }
    switch (event) {
      case 'agent.status':
        if (payload) {
          activeAgentID = payload.agent_id || activeAgentID;
          agentBadge.textContent = 'agent: ' + activeAgentID;
          const statusSession = payload.session || payload.session_id;
          if (mainView === 'chat' && payload.status === 'thinking' && statusSession === sessionID) {
            if (!streamingBubble) showThinking();
          }
          if (mainView === 'chat' && payload.status === 'idle' && statusSession === sessionID) {
            if (!streamingBubble) hideThinking();
          }
        }
        break;
      case 'chat': {
        if (!payload || !payload.runId || !payload.sessionKey || !Number.isInteger(payload.seq) || payload.seq < 0) break;
        const runKey = payload.sessionKey + '\u0000' + payload.runId;
        const lastSeq = chatRunSeq.get(runKey);
        if (lastSeq !== undefined && payload.seq <= lastSeq) break;
        chatRunSeq.set(runKey, payload.seq);
        chatActivityVersion++;
        const visible = mainView === 'chat' && payload.sessionKey === sessionID;
        if (payload.state === 'status') {
          setRunActive(true, payload.sessionKey);
          if (visible && !streamingBubble) showThinking();
        } else if (payload.state === 'delta') {
          setRunActive(true, payload.sessionKey);
          if (visible) {
            hideThinking();
            if (payload.replace === true) replaceStreaming(payload.deltaText || '');
            else if (payload.deltaText) appendChunk(payload.deltaText);
          }
        } else if (payload.state === 'final') {
          const finalText = chatMessageText(payload.message);
          if (visible) {
            if (streamingBubble) finalizeStreaming();
            else if (finalText) addMsg(finalText, 'agent');
          }
          setRunActive(false, payload.sessionKey);
          loadSessions();
        } else if (payload.state === 'aborted' || payload.state === 'error') {
          if (visible) {
            finalizeStreaming();
            if (payload.errorMessage) addMsg('⚠️ ' + payload.errorMessage, 'error-msg');
          }
          setRunActive(false, payload.sessionKey);
          loadSessions();
        }
        break;
      }
      case 'session.typing':
        if (mainView === 'chat' && payload && payload.sessionKey === sessionID && payload.actor) {
          showTypingIndicator(payload.actor.label || payload.actor.id || 'Someone', payload.typing === true);
        }
        break;
      case 'chat.message':
        if (mainView === 'chat' && payload && (!payload.session_id || payload.session_id === sessionID) && payload.direction === 'outbound' && !streamingBubble) {
          hideThinking();
          if (payload.text) addMsg(payload.text, 'agent');
        }
        break;
      case 'tool.start':
        setRunActive(true, payload && payload.session_id);
        if (mainView === 'chat') renderToolActivity(payload, 'started');
        break;
      case 'tool.progress':
        if (mainView === 'chat') renderToolActivity(payload, 'progress');
        break;
      case 'tool.result':
        if (mainView === 'chat') renderToolActivity(payload, 'result');
        break;
      case 'tool.error':
        if (mainView === 'chat') renderToolActivity(payload, 'error');
        break;
      case 'exec.approval.requested':
        if (payload) enqueueApproval(payload, null, 'exec');
        break;
      case 'exec.approval.resolved':
        if (payload && payload.id) {
          approvalQueue = approvalQueue.filter(a => a.id !== payload.id);
          if (currentApprovalID === payload.id) { currentApprovalID = null; currentApproval = null; showNextApproval(); }
          updateApprovalBadge();
        }
        break;
      case 'plugin.approval.requested':
        if (payload) enqueueApproval(payload, null, 'plugin');
        break;
      case 'plugin.approval.resolved':
        if (payload && (payload.id || payload.request_id || payload.approval_id)) {
          const id = payload.id || payload.request_id || payload.approval_id;
          approvalQueue = approvalQueue.filter(a => a.id !== id);
          if (currentApprovalID === id) { currentApprovalID = null; currentApproval = null; showNextApproval(); }
          updateApprovalBadge();
        }
        break;
      case 'node.invoke.progress': {
        if (!payload) break;
        const invokeID = payload.invoke_id || payload.invokeId;
        const nodeID = payload.node_id || payload.nodeId;
        if (!invokeID || !Number.isInteger(payload.seq)) break;
        const previous = nodeInvokeProgress.get(invokeID);
        if (previous && payload.seq <= previous.seq) break;
        nodeInvokeProgress.set(invokeID, { nodeID, seq: payload.seq, text: (previous ? previous.text : '') + (payload.chunk || '') });
        renderNodeInvokeProgress();
        break;
      }
      case 'config.updated':
        addMsg('Config reloaded.', 'system');
        loadAgents();
        break;
      case 'channel.message':
        // Optionally surface channel messages in a notification.
        break;
      case 'terminal.data':
        handleTerminalData(payload);
        break;
      case 'terminal.exit':
        handleTerminalExit(payload);
        break;
      case 'question.requested':
        if (payload) enqueueQuestion(payload);
        break;
      case 'question.resolved':
        if (payload && payload.id) dropQuestion(payload.id);
        break;
      case 'task.suggestion':
        handleTaskSuggestionEvent(payload);
        break;
      case 'board.changed':
        handleBoardChanged(payload);
        break;
      case 'mcp.app.viewCreated':
        handleMcpAppViewCreated(payload);
        break;
    }
  }

  // ── WebSocket connect ─────────────────────────────────────────────────────
  function connect() {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    ws = new WebSocket(proto + '//' + location.host + WS_PATH);
    ws.binaryType = 'arraybuffer';
    ws.onopen = () => {};
    ws.onmessage = async (ev) => {
      let frame;
      try { frame = JSON.parse(ev.data); } catch { return; }

      if (frame.type === 'event') {
        if (frame.event === 'connect.challenge') {
          const nonce = frame.payload && frame.payload.nonce;
          const id = nextID();
          sendFrame({
            type: 'request', id, method: 'connect',
            params: {
              client: { id: 'metiq-webui', mode: 'local', platform: navigator.platform || 'browser', version: '0.0.0', instanceId: 'webui-' + Math.random().toString(36).slice(2) },
              minProtocol: 4, maxProtocol: 4,
              role: 'operator',
              scopes: ['operator.admin', 'operator.read', 'operator.write', 'operator.approvals', 'operator.pairing', 'operator.questions'],
              auth: { token: TOKEN, nonce }
            }
          });
          pendingResolvers[id] = {
            resolve: async (hello) => {
              try {
                if (!hello || hello.protocol !== 4) throw new Error('gateway protocol v4 is required');
                const advertised = new Set((hello.features && hello.features.events) || []);
                if (!advertised.has('chat')) throw new Error('gateway does not advertise protocol-v4 chat events');
                const descriptors = (hello.features && hello.features.methodDescriptors) || [];
                if (!Array.isArray(descriptors) || descriptors.length === 0) throw new Error('gateway does not advertise method descriptors');
                gatewayMethodDescriptors = new Map(descriptors.filter(item => item && item.name).map(item => [item.name, item]));
                gatewayScopes = new Set((hello.auth && hello.auth.scopes) || []);
                const wanted = ['chat', 'agent.status', 'tool.start', 'tool.progress', 'tool.result', 'tool.error', 'exec.approval.requested', 'exec.approval.resolved', 'plugin.approval.requested', 'plugin.approval.resolved', 'config.updated', 'channel.message', 'node.invoke.progress', 'session.typing', 'session.suggestion', 'question.requested', 'question.resolved', 'task.suggestion', 'board.changed', 'mcp.app.viewCreated'];
                const events = wanted.filter(name => advertised.has(name));
                await callMethod('events.subscribe', { events });
                await reconcilePendingApprovals();
                await reconcilePendingQuestions();
                connected = true;
                reconnectDelay = 1500;
                chatRunSeq.clear();
                setConnected(true, 'connected');
                loadSidebar();
                loadCommandCatalog();
                if (sessionID && mainView === 'chat') {
                  toolCards = {};
                  msgs.innerHTML = '';
                  addMsg('Resuming session history…', 'system');
                  updateRunControls();
                  loadSessionHistory(sessionID);
                } else if (!sessionID && mainView === 'chat') {
                  addMsg(t('connected'), 'system');
                }
              } catch (err) {
                setConnected(false, 'protocol setup failed');
                addMsg('Connection refused: ' + (err && err.message || 'protocol setup failed'), 'error-msg');
                if (ws) ws.close(1008, 'protocol setup failed');
              }
            },
            reject: (err) => {
              setConnected(false, 'auth failed');
              addMsg('Connection refused: ' + (err && err.message || 'authentication required'), 'error-msg');
            }
          };
          return;
        }
        onEvent(frame.event, frame.payload);
        return;
      }

      if (frame.type === 'response' && frame.id) {
        const resolver = pendingResolvers[frame.id];
        delete pendingResolvers[frame.id];
        if (resolver) {
          if (frame.ok) resolver.resolve(frame.payload);
          else resolver.reject(frame.error || { message: 'request failed' });
        }
      }
    };

    ws.onerror = () => setConnected(null, 'connection error');
    ws.onclose = () => {
      connected = false;
      gatewayMethodDescriptors = new Map();
      gatewayScopes = new Set();
      finalizeStreaming();
      setRunActive(false);
      handleTerminalDisconnect();
      setConnected(null, 'reconnecting in ' + (reconnectDelay / 1000).toFixed(1) + 's…');
      Object.values(pendingResolvers).forEach(resolver => resolver.reject({ message: 'gateway disconnected' }));
      pendingResolvers = {};
      hideThinking();
      setTimeout(() => {
        reconnectDelay = Math.min(reconnectDelay * 1.5, 15000);
        connect();
      }, reconnectDelay);
    };
  }


