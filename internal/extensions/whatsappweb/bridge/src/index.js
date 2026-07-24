import http from "node:http";
import os from "node:os";
import path from "node:path";
import { promises as fs } from "node:fs";
import makeWASocket, {
  DisconnectReason,
  Browsers,
  jidNormalizedUser,
  useMultiFileAuthState,
} from "baileys";
import { WebSocketServer } from "ws";
import {
  decodeRef,
  encodeRef,
  eventID,
  extractText,
  normalizeReactionLevel,
  normalizeTarget,
  unwrapMessage,
  validateReaction,
} from "./protocol.js";

const host = process.env.WHATSAPPWEB_BRIDGE_HOST || "127.0.0.1";
const port = Number(process.env.WHATSAPPWEB_BRIDGE_PORT || 18789);
const token = process.env.WHATSAPPWEB_BRIDGE_TOKEN || "";
const dataRoot = expandHome(process.env.WHATSAPPWEB_DATA_DIR || "~/.metiq/whatsappweb");
const remote = !["127.0.0.1", "::1", "localhost"].includes(host);
if (remote && (process.env.WHATSAPPWEB_ALLOW_REMOTE !== "true" || !token)) {
  throw new Error("remote bind requires WHATSAPPWEB_ALLOW_REMOTE=true and WHATSAPPWEB_BRIDGE_TOKEN");
}

const sessions = new Map();
const wss = new WebSocketServer({ noServer: true });

function expandHome(value) {
  return value === "~" ? os.homedir() : value.startsWith("~/") ? path.join(os.homedir(), value.slice(2)) : value;
}

function accountDir(accountId) {
  return path.join(dataRoot, "accounts", Buffer.from(accountId, "utf8").toString("base64url"));
}

async function regularJSONFiles(dir) {
  try {
    const entries = await fs.readdir(dir, { withFileTypes: true });
    return entries.filter((entry) => entry.isFile() && entry.name.endsWith(".json")).map((entry) => entry.name);
  } catch (error) {
    if (error?.code === "ENOENT") return [];
    throw error;
  }
}

async function hasCreds(dir) {
  try {
    const stat = await fs.lstat(path.join(dir, "creds.json"));
    return stat.isFile() && !stat.isSymbolicLink();
  } catch {
    return false;
  }
}

// Legacy migration is deliberately copy-only. The source remains a rollback
// backup and creds.json is copied last so a partial target is never authoritative.
async function migrateLegacy(accountId, target, legacy) {
  if (accountId !== "default" || await hasCreds(target) || !await hasCreds(legacy)) return false;
  await fs.mkdir(target, { recursive: true, mode: 0o700 });
  const files = await regularJSONFiles(legacy);
  const ordered = files.filter((name) => name !== "creds.json");
  ordered.push("creds.json");
  for (const name of ordered) {
    const source = path.join(legacy, name);
    const stat = await fs.lstat(source);
    if (!stat.isFile() || stat.isSymbolicLink()) throw new Error("legacy auth contains unsafe credential entry");
    const tmp = path.join(target, `.${name}.${process.pid}.tmp`);
    await fs.copyFile(source, tmp);
    await fs.chmod(tmp, 0o600);
    await fs.rename(tmp, path.join(target, name));
  }
  await fs.writeFile(path.join(target, ".migrated-from-legacy"), `${new Date().toISOString()}\n`, { mode: 0o600 });
  return true;
}


class AccountSession {
  constructor(accountId, config) {
    this.accountId = accountId;
    this.authDir = "";
    this.legacyAuthDir = "";
    this.reactionLevel = "minimal";
    this.state = "stopped";
    this.sock = null;
    this.desired = false;
    this.generation = 0;
    this.reconnectAttempt = 0;
    this.reconnectTimer = null;
    this.qr = "";
    this.qrWaiters = new Set();
    this.subscriber = null;
    this.seen = new Set();
    this.seenOrder = [];
    this.configure(config);
  }

  configure(config = {}) {
    const authDir = config.auth_dir ? expandHome(String(config.auth_dir)) : accountDir(this.accountId);
    if (!path.isAbsolute(authDir)) throw new Error("auth_dir must be absolute");
    const legacyAuthDir = config.legacy_auth_dir
      ? expandHome(String(config.legacy_auth_dir))
      : dataRoot;
    if (!path.isAbsolute(legacyAuthDir)) throw new Error("legacy_auth_dir must be absolute");
    if (this.sock && (this.authDir !== authDir || this.legacyAuthDir !== legacyAuthDir)) {
      throw new Error("auth directory cannot change while the account session is active");
    }
    this.authDir = authDir;
    this.legacyAuthDir = legacyAuthDir;
    this.reactionLevel = normalizeReactionLevel(config.reaction_level);
  }

  emit(frame) {
    if (this.subscriber?.readyState === this.subscriber.OPEN) {
      this.subscriber.send(JSON.stringify(frame));
    }
  }

  setState(state, lastError = "") {
    this.state = state;
    this.emit({ type: "session", state, ...(lastError ? { last_error: lastError } : {}) });
  }

  async start() {
    this.desired = true;
    if (this.sock) return;
    await migrateLegacy(this.accountId, this.authDir, this.legacyAuthDir);
    await fs.mkdir(this.authDir, { recursive: true, mode: 0o700 });
    const { state, saveCreds } = await useMultiFileAuthState(this.authDir);
    const generation = ++this.generation;
    this.setState("connecting");
    const sock = makeWASocket({
      auth: state,
      browser: Browsers.macOS("Desktop"),
      printQRInTerminal: false,
      syncFullHistory: false,
      markOnlineOnConnect: false,
    });
    this.sock = sock;
    sock.ev.on("creds.update", saveCreds);
    sock.ev.on("messages.upsert", ({ messages = [] }) => {
      if (generation !== this.generation) return;
      for (const message of messages) this.onMessage(message);
    });
    sock.ev.on("connection.update", (update) => {
      if (generation !== this.generation) return;
      void this.onConnectionUpdate(update);
    });
  }

  async onConnectionUpdate(update) {
    if (update.qr) {
      this.qr = update.qr;
      this.setState("qr_pending");
      for (const resolve of this.qrWaiters) resolve(update.qr);
      this.qrWaiters.clear();
    }
    if (update.connection === "open") {
      this.qr = "";
      this.reconnectAttempt = 0;
      this.setState("authenticated");
    }
    if (update.connection !== "close") return;
    const code = update.lastDisconnect?.error?.output?.statusCode
      || update.lastDisconnect?.error?.statusCode;
    this.sock = null;
    if (!this.desired) return this.setState("stopped");
    if (code === DisconnectReason.loggedOut) {
      this.desired = false;
      return this.setState("logged_out", "WhatsApp logged out this linked device");
    }
    this.setState("reconnecting", "WhatsApp connection closed");
    const wait = Math.min(30_000, 1000 * (2 ** this.reconnectAttempt++));
    clearTimeout(this.reconnectTimer);
    this.reconnectTimer = setTimeout(() => {
      if (this.desired && !this.sock) void this.start().catch((error) => {
        this.setState("failed", String(error?.message || error));
      });
    }, wait);
  }

  onMessage(message) {
    const key = message?.key;
    if (!key?.id || !key.remoteJid || key.fromMe || key.remoteJid === "status@broadcast") return;
    const text = String(extractText(message.message)).trim();
    if (!text) return;
    const group = key.remoteJid.endsWith("@g.us");
    const sender = group ? key.participant : key.remoteJid;
    if (!sender) return;
    const dedup = `${key.remoteJid}\0${key.id}\0${key.participant || ""}`;
    if (this.seen.has(dedup)) return;
    this.seen.add(dedup);
    this.seenOrder.push(dedup);
    if (this.seenOrder.length > 2048) this.seen.delete(this.seenOrder.shift());
    const quotedID = unwrapMessage(message.message).extendedTextMessage?.contextInfo?.stanzaId;
    const quotedParticipant = unwrapMessage(message.message).extendedTextMessage?.contextInfo?.participant;
    this.emit({
      type: "message",
      event_id: eventID(key),
      message_ref: encodeRef(this.accountId, key),
      chat_jid: key.remoteJid,
      sender_jid: jidNormalizedUser(sender),
      thread_id: group ? key.remoteJid : "",
      ...(quotedID ? { reply_to_event_id: eventID({ remoteJid: key.remoteJid, id: quotedID, participant: quotedParticipant }) } : {}),
      text,
      timestamp_s: Number(message.messageTimestamp || Math.floor(Date.now() / 1000)),
      is_self: false,
    });
  }

  requireSocket() {
    if (!this.sock || this.state !== "authenticated") throw new Error(`account is not authenticated (state=${this.state})`);
    return this.sock;
  }

  async send(input) {
    const jid = normalizeTarget(input.to);
    const result = await this.requireSocket().sendMessage(jid, { text: String(input.text || "") });
    return { message_id: result?.key?.id || "", chat_jid: jid };
  }

  async typing(input) {
    const jid = normalizeTarget(input.to);
    await this.requireSocket().sendPresenceUpdate(input.typing ? "composing" : "paused", jid);
    return {};
  }

  async react(input) {
    validateReaction(this.reactionLevel, input.emoji);
    const key = decodeRef(this.accountId, input.message_ref);
    await this.requireSocket().sendMessage(key.remoteJid, {
      react: { text: input.remove ? "" : String(input.emoji), key },
    });
    return {};
  }

  async pairCode(input) {
    const phone = String(input.phone_number || "").replace(/\D/g, "");
    if (phone.length < 8 || phone.length > 15) throw new Error("phone_number must contain 8-15 digits");
    if (!this.sock) await this.start();
    const pairingCode = await this.sock.requestPairingCode(phone);
    this.setState("pair_code_pending");
    return { state: this.state, pairing_code: pairingCode };
  }

  async nextQR(signal) {
    if (this.state === "authenticated") return { state: this.state, linked: true };
    if (!this.sock) await this.start();
    if (this.qr) return { state: "qr_pending", qr: this.qr };
    return new Promise((resolve, reject) => {
      const done = (qr) => {
        clearTimeout(timer);
        signal?.removeEventListener("abort", aborted);
        resolve({ state: "qr_pending", qr });
      };
      const aborted = () => {
        clearTimeout(timer);
        this.qrWaiters.delete(done);
        reject(new Error("QR request cancelled"));
      };
      const timer = setTimeout(() => {
        this.qrWaiters.delete(done);
        reject(new Error("no QR received before request deadline"));
      }, 30_000);
      this.qrWaiters.add(done);
      signal?.addEventListener("abort", aborted, { once: true });
    });
  }

  status() {
    return {
      state: this.state,
      linked: this.state === "authenticated",
      ...(this.sock?.user?.id ? { self_jid: jidNormalizedUser(this.sock.user.id) } : {}),
    };
  }

  async stop() {
    this.desired = false;
    clearTimeout(this.reconnectTimer);
    this.reconnectTimer = null;
    this.generation += 1;
    const sock = this.sock;
    this.sock = null;
    try { sock?.ws?.close(); } catch {}
    this.setState("stopped");
  }

  async logout() {
    this.desired = false;
    clearTimeout(this.reconnectTimer);
    try { await this.sock?.logout(); } catch {}
    await this.stop();
    for (const name of await regularJSONFiles(this.authDir)) {
      const target = path.join(this.authDir, name);
      const stat = await fs.lstat(target);
      if (!stat.isFile() || stat.isSymbolicLink()) throw new Error("unsafe auth credential entry");
      await fs.unlink(target);
    }
    this.setState("auth_required");
    return { state: this.state, cleared: true };
  }
}

function authorized(request) {
  if (!token) return true;
  return request.headers.authorization === `Bearer ${token}`;
}

function reply(response, status, payload) {
  const raw = JSON.stringify(payload);
  response.writeHead(status, { "content-type": "application/json", "content-length": Buffer.byteLength(raw) });
  response.end(raw);
}

async function readJSON(request) {
  const chunks = [];
  let size = 0;
  for await (const chunk of request) {
    size += chunk.length;
    if (size > 64 * 1024) throw new Error("request body exceeds 65536 bytes");
    chunks.push(chunk);
  }
  return chunks.length ? JSON.parse(Buffer.concat(chunks).toString("utf8")) : {};
}

function route(pathname) {
  const match = pathname.match(/^\/v1\/accounts\/([^/]+)\/(.+)$/);
  if (!match) return null;
  const accountId = decodeURIComponent(match[1]);
  if (!accountId || accountId.length > 128 || accountId.includes("\0")) return null;
  return { accountId, operation: match[2] };
}

function sessionFor(accountId, config) {
  let session = sessions.get(accountId);
  if (!session) {
    session = new AccountSession(accountId, config || {});
    sessions.set(accountId, session);
  } else if (config) {
    session.configure(config);
  }
  return session;
}

const server = http.createServer(async (request, response) => {
  try {
    if (!authorized(request)) return reply(response, 401, { ok: false, error: { code: "unauthorized", message: "bridge authentication required" } });
    const parsed = route(new URL(request.url, "http://bridge").pathname);
    if (!parsed || request.method !== "POST") return reply(response, 404, { ok: false, error: { code: "not_found", message: "unknown route" } });
    const body = await readJSON(request);
    const session = sessionFor(parsed.accountId, body.config || {});
    const input = body.input || {};
    let result;
    switch (parsed.operation) {
      case "session/start": await session.start(); result = session.status(); break;
      case "session/stop": await session.stop(); result = session.status(); break;
      case "messages": result = await session.send(input); break;
      case "typing": result = await session.typing(input); break;
      case "reactions": result = await session.react(input); break;
      case "auth/status": result = session.status(); break;
      case "auth/qr": result = await session.nextQR(AbortSignal.timeout(31_000)); break;
      case "auth/pair-code": result = await session.pairCode(input); break;
      case "auth/logout": result = await session.logout(); break;
      default: return reply(response, 404, { ok: false, error: { code: "not_found", message: "unknown operation" } });
    }
    return reply(response, 200, { ok: true, result });
  } catch (error) {
    const message = String(error?.message || error);
    const status = message.includes("cannot change") ? 409 : 400;
    return reply(response, status, { ok: false, error: { code: "bridge_error", message } });
  }
});

server.on("upgrade", (request, socket, head) => {
  if (!authorized(request)) return socket.destroy();
  const parsed = route(new URL(request.url, "http://bridge").pathname);
  if (!parsed || parsed.operation !== "events") return socket.destroy();
  let session;
  try { session = sessionFor(parsed.accountId, null); } catch { return socket.destroy(); }
  wss.handleUpgrade(request, socket, head, (ws) => {
    if (session.subscriber && session.subscriber !== ws) session.subscriber.close(1000, "replaced");
    session.subscriber = ws;
    ws.send(JSON.stringify({ type: "stream_ready", state: session.state }));
    ws.on("close", () => {
      if (session.subscriber === ws) session.subscriber = null;
    });
  });
});

async function shutdown() {
  server.close();
  await Promise.allSettled([...sessions.values()].map((session) => session.stop()));
  wss.close();
}

process.once("SIGINT", () => void shutdown());
process.once("SIGTERM", () => void shutdown());
server.listen(port, host, () => {
  console.log(`whatsappweb bridge listening on http://${host}:${port}`);
});
