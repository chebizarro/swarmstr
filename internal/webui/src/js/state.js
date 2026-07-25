const WS_PATH = '{{.WSPath}}';
const TOKEN   = '{{.Token}}';
(function () {
  const $ = id => document.getElementById(id);

  const msgs      = $('messages');
  const input     = $('input');
  const sendBtn   = $('send');
  const attachBtn = $('attach-btn');
  const attachInput = $('attach-input');
  const attachmentTray = $('attachment-tray');
  const slashMenu = $('slash-menu');
  const searchBox = $('search-box');
  const searchToggle = $('search-toggle');
  const exportChatBtn = $('export-chat');
  const themeToggle = $('theme-toggle');
  const localeSelect = $('locale-select');
  const abortBtn  = $('abort-run');
  const compactBtn = $('compact-session');
  const clearBtn  = $('clear-session');
  const dot       = $('dot');
  const connLabel = $('conn-label');
  const agentBadge      = $('agent-badge');
  const approvalsBadge  = $('approvals-badge');
  const approvalModal   = $('approval-modal');
  const approvalTitle   = $('approval-title');
  const approvalSubtitle = $('approval-subtitle');
  const approvalCmd     = $('approval-cmd');
  const approvalMeta    = $('approval-meta');
  const approvalQueueCount = $('approval-queue-count');
  const approvalCountdown  = $('approval-countdown');
  const approvalIDLabel    = $('approval-id');
  const approvalError      = $('approval-error');
  const approvalButtons    = ['approval-deny', 'approval-approve-once', 'approval-approve-always'].map($);
  const questionsBadge     = $('questions-badge');
  const questionModal      = $('question-modal');
  const questionSubtitle   = $('question-subtitle');
  const questionBody       = $('question-body');
  const questionError      = $('question-error');
  const questionQueueCount = $('question-queue-count');
  const questionCountdown  = $('question-countdown');
  const questionIDLabel    = $('question-id');
  const questionSubmitBtn  = $('question-submit');
  const questionCancelBtn  = $('question-cancel');
  const questionLaterBtn   = $('question-later');
  const sessionsList    = $('sessions-list');
  const channelsList    = $('channels-list');
  const agentsList      = $('agents-list');
  const mobileMenuBtn   = $('mobile-menu-btn');
  const sidebarBackdrop = $('sidebar-backdrop');

  // ── State ─────────────────────────────────────────────────────────────────
  let ws, reconnectDelay = 1500;
  let connected = false;
  let pendingResolvers = {};
  let gatewayMethodDescriptors = new Map();
  let gatewayScopes = new Set();
  let seq = 0;
  let sessionID = localStorage.getItem('metiq_session') || null;
  let activeAgentID = 'main';
  // Streaming: the current agent bubble being written to (or null).
    let streamingBubble = null;
    let streamingText = '';
    const chatRunSeq = new Map();
    const nodeInvokeProgress = new Map();
    let chatActivityVersion = 0;
    let thinkingEl = null;
  // Approval queue: pending exec/plugin approval payloads.
  let approvalQueue = [];
  let currentApprovalID = null;
  let currentApproval = null;
  // Question queue: pending question.requested records (agent pose-and-block).
  let questionQueue = [];
  let currentQuestionID = null;
  let questionCountdownTimer = null;
  // Operator-minted attach grants (process-local; tokens vanish on reload).
  let mintedAttachGrants = [];
  // Boards view state.
  let boardsSessionKey = null;
  let boardsRefreshTimer = null;
  let activeRun = false;
  let activeRunSessionID = null;
  let toolCards = {};
  let historyLoadingSessionID = null;
  let bufferedLiveEvents = [];
  let mainView = 'chat';
  let viewSeq = 0;
  let pendingAttachments = [];
  let attachmentReadsPending = 0;
  let attachmentReadGeneration = 0;
  const maxAttachments = 6;
  const maxAttachmentBytes = 8 * 1024 * 1024;
  const maxAttachmentTotalBytes = 20 * 1024 * 1024;
  let inputHistory = JSON.parse(localStorage.getItem('metiq_input_history') || '[]');
  let inputHistoryIndex = inputHistory.length;
  let historyBrowsing = false;
  let locale = localStorage.getItem('metiq_locale') || 'en';
  let slashCommands = [];
  const I18N = {
    en: {
      send: 'Send', placeholder: 'Send a message… (Shift+Enter for newline)', connected: 'Connected. Type /help for commands.', connecting: 'connecting…',
      sessions: 'Sessions', channels: 'Channels', agents: 'Agents', dashboard: 'Dashboard', config: 'Config', usage: 'Usage', cron: 'Cron', nodes: 'Nodes', mcp: 'MCP', skills: 'Skills',
      newSession: '+ New session', loading: 'Loading…', noSessions: 'No sessions yet', noChannels: 'No channels connected', noAgents: 'No agents configured', noItems: 'No items',
      stopRun: 'Stop run', compact: 'Compact', clear: 'Clear', tip: 'Tip: /help for slash commands', search: 'Search chat…', export: 'Export', theme: 'Theme', approvalPending: '⚠ approval pending',
      errorLoadHistory: 'Could not load session history', noMessages: 'No messages in this session yet.'
    },
    es: {
      send: 'Enviar', placeholder: 'Envía un mensaje… (Shift+Enter para nueva línea)', connected: 'Conectado. Escribe /help para comandos.', connecting: 'conectando…',
      sessions: 'Sesiones', channels: 'Canales', agents: 'Agentes', dashboard: 'Panel', config: 'Config', usage: 'Uso',
      newSession: '+ Nueva sesión', loading: 'Cargando…', noSessions: 'Aún no hay sesiones', noChannels: 'No hay canales conectados', noAgents: 'No hay agentes configurados', noItems: 'Sin elementos',
      stopRun: 'Detener', compact: 'Compactar', clear: 'Borrar', tip: 'Consejo: /help para comandos', search: 'Buscar chat…', export: 'Exportar', theme: 'Tema', approvalPending: '⚠ aprobación pendiente',
      errorLoadHistory: 'No se pudo cargar el historial', noMessages: 'Esta sesión aún no tiene mensajes.'
    },
    fr: {
      send: 'Envoyer', placeholder: 'Envoyer un message… (Maj+Entrée pour une nouvelle ligne)', connected: 'Connecté. Tapez /help pour les commandes.', connecting: 'connexion…',
      sessions: 'Sessions', channels: 'Canaux', agents: 'Agents', dashboard: 'Tableau', config: 'Config', usage: 'Utilisation',
      newSession: '+ Nouvelle session', loading: 'Chargement…', noSessions: 'Aucune session', noChannels: 'Aucun canal connecté', noAgents: 'Aucun agent configuré', noItems: 'Aucun élément',
      stopRun: 'Arrêter', compact: 'Compacter', clear: 'Effacer', tip: 'Astuce : /help pour les commandes', search: 'Rechercher…', export: 'Exporter', theme: 'Thème', approvalPending: '⚠ approbation en attente',
      errorLoadHistory: 'Impossible de charger l’historique', noMessages: 'Aucun message dans cette session.'
    },
    de: {
      send: 'Senden', placeholder: 'Nachricht senden… (Umschalt+Enter für neue Zeile)', connected: 'Verbunden. /help zeigt Befehle.', connecting: 'verbinden…',
      sessions: 'Sitzungen', channels: 'Kanäle', agents: 'Agenten', dashboard: 'Übersicht', config: 'Konfig', usage: 'Nutzung',
      newSession: '+ Neue Sitzung', loading: 'Laden…', noSessions: 'Noch keine Sitzungen', noChannels: 'Keine Kanäle verbunden', noAgents: 'Keine Agenten konfiguriert', noItems: 'Keine Elemente',
      stopRun: 'Stoppen', compact: 'Kompakt', clear: 'Leeren', tip: 'Tipp: /help für Befehle', search: 'Chat suchen…', export: 'Export', theme: 'Design', approvalPending: '⚠ Genehmigung ausstehend',
      errorLoadHistory: 'Verlauf konnte nicht geladen werden', noMessages: 'Noch keine Nachrichten in dieser Sitzung.'
    },
    ja: {
      send: '送信', placeholder: 'メッセージを送信…（Shift+Enterで改行）', connected: '接続済み。/help でコマンドを表示。', connecting: '接続中…',
      sessions: 'セッション', channels: 'チャンネル', agents: 'エージェント', dashboard: 'ダッシュボード', config: '設定', usage: '使用量',
      newSession: '+ 新規セッション', loading: '読み込み中…', noSessions: 'セッションはまだありません', noChannels: '接続済みチャンネルなし', noAgents: '設定済みエージェントなし', noItems: '項目なし',
      stopRun: '停止', compact: '圧縮', clear: 'クリア', tip: 'ヒント: /help でコマンド', search: 'チャット検索…', export: 'エクスポート', theme: 'テーマ', approvalPending: '⚠ 承認待ち',
      errorLoadHistory: '履歴を読み込めません', noMessages: 'このセッションにはまだメッセージがありません。'
    },
    'zh-CN': {
      send: '发送', placeholder: '发送消息…（Shift+Enter 换行）', connected: '已连接。输入 /help 查看命令。', connecting: '连接中…',
      sessions: '会话', channels: '频道', agents: '智能体', dashboard: '仪表盘', config: '配置', usage: '用量',
      newSession: '+ 新建会话', loading: '加载中…', noSessions: '暂无会话', noChannels: '未连接频道', noAgents: '未配置智能体', noItems: '无项目',
      stopRun: '停止', compact: '压缩', clear: '清除', tip: '提示：/help 查看命令', search: '搜索聊天…', export: '导出', theme: '主题', approvalPending: '⚠ 待审批',
      errorLoadHistory: '无法加载会话历史', noMessages: '此会话暂无消息。'
    },
    'pt-BR': {
      send: 'Enviar', placeholder: 'Envie uma mensagem… (Shift+Enter para nova linha)', connected: 'Conectado. Digite /help para comandos.', connecting: 'conectando…',
      sessions: 'Sessões', channels: 'Canais', agents: 'Agentes', dashboard: 'Painel', config: 'Config', usage: 'Uso',
      newSession: '+ Nova sessão', loading: 'Carregando…', noSessions: 'Nenhuma sessão ainda', noChannels: 'Nenhum canal conectado', noAgents: 'Nenhum agente configurado', noItems: 'Nenhum item',
      stopRun: 'Parar', compact: 'Compactar', clear: 'Limpar', tip: 'Dica: /help para comandos', search: 'Buscar chat…', export: 'Exportar', theme: 'Tema', approvalPending: '⚠ aprovação pendente',
      errorLoadHistory: 'Não foi possível carregar o histórico', noMessages: 'Ainda não há mensagens nesta sessão.'
    },
    ko: {
      send: '보내기', placeholder: '메시지 보내기… (Shift+Enter로 줄바꿈)', connected: '연결됨. /help로 명령을 확인하세요.', connecting: '연결 중…',
      sessions: '세션', channels: '채널', agents: '에이전트', dashboard: '대시보드', config: '설정', usage: '사용량',
      newSession: '+ 새 세션', loading: '불러오는 중…', noSessions: '아직 세션이 없습니다', noChannels: '연결된 채널 없음', noAgents: '설정된 에이전트 없음', noItems: '항목 없음',
      stopRun: '중지', compact: '압축', clear: '지우기', tip: '팁: /help로 명령 보기', search: '채팅 검색…', export: '내보내기', theme: '테마', approvalPending: '⚠ 승인 대기',
      errorLoadHistory: '기록을 불러올 수 없습니다', noMessages: '이 세션에는 아직 메시지가 없습니다.'
    },
  };
  let approvalCountdownTimer = null;

