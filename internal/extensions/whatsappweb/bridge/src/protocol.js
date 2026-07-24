import crypto from "node:crypto";
import { jidNormalizedUser } from "baileys";

export function normalizeReactionLevel(value) {
  const level = String(value || "minimal").trim().toLowerCase();
  if (!["off", "ack", "minimal", "extensive"].includes(level)) throw new Error("invalid reaction_level");
  return level;
}

export function validateReaction(level, emoji) {
  if (level === "off" || level === "ack") throw new Error(`agent reactions disabled at reaction_level=${level}`);
  const text = String(emoji || "").trim();
  if (!text || Buffer.byteLength(text) > 32) throw new Error("invalid reaction emoji");
  const minimal = new Set(["👍", "👎", "❤️", "😂", "😮", "😢", "🙏", "🎉", "✅", "👀"]);
  if (level === "minimal" && !minimal.has(text)) throw new Error("reaction is not allowed at reaction_level=minimal");
}

export function normalizeTarget(value) {
  const target = String(value || "").trim();
  if (!target) throw new Error("target is required");
  if (target.includes("@")) return jidNormalizedUser(target);
  const digits = target.replace(/\D/g, "");
  if (digits.length < 8 || digits.length > 15) throw new Error("target must be a WhatsApp JID or 8-15 digit phone number");
  return `${digits}@s.whatsapp.net`;
}

export function unwrapMessage(message) {
  let current = message;
  for (let i = 0; current && i < 8; i += 1) {
    const wrapper = current.ephemeralMessage?.message
      || current.viewOnceMessage?.message
      || current.viewOnceMessageV2?.message
      || current.documentWithCaptionMessage?.message
      || current.editedMessage?.message;
    if (!wrapper) break;
    current = wrapper;
  }
  return current || {};
}

export function extractText(raw) {
  const message = unwrapMessage(raw);
  return message.conversation
    || message.extendedTextMessage?.text
    || message.imageMessage?.caption
    || message.videoMessage?.caption
    || message.documentMessage?.caption
    || (message.imageMessage ? "[whatsappweb image]" : "")
    || (message.videoMessage ? "[whatsappweb video]" : "")
    || (message.audioMessage ? "[whatsappweb audio]" : "")
    || (message.documentMessage ? `[whatsappweb document${message.documentMessage.fileName ? `: ${message.documentMessage.fileName}` : ""}]` : "")
    || (message.stickerMessage ? "[whatsappweb sticker]" : "");
}

export function eventID(key) {
  const input = `${key.remoteJid || ""}\0${key.id || ""}\0${key.participant || ""}`;
  return `wweb-${crypto.createHash("sha256").update(input).digest("hex").slice(0, 24)}`;
}

export function encodeRef(accountId, key) {
  return Buffer.from(JSON.stringify({
    v: 1, a: accountId, remoteJid: key.remoteJid, id: key.id,
    fromMe: Boolean(key.fromMe), participant: key.participant || undefined,
  })).toString("base64url");
}

export function decodeRef(accountId, value) {
  const ref = JSON.parse(Buffer.from(String(value), "base64url").toString("utf8"));
  if (ref.v !== 1 || ref.a !== accountId || !ref.remoteJid || !ref.id) throw new Error("invalid message_ref");
  return {
    remoteJid: ref.remoteJid, id: ref.id, fromMe: Boolean(ref.fromMe),
    ...(ref.participant ? { participant: ref.participant } : {}),
  };
}
