import assert from "node:assert/strict";
import test from "node:test";
import {
  decodeRef,
  encodeRef,
  eventID,
  extractText,
  normalizeReactionLevel,
  normalizeTarget,
  validateReaction,
} from "../src/protocol.js";

test("normalizes direct targets and preserves group JIDs", () => {
  assert.equal(normalizeTarget("+1 (555) 123-4567"), "15551234567@s.whatsapp.net");
  assert.equal(normalizeTarget("120363000000@g.us"), "120363000000@g.us");
});

test("round-trips participant-aware reaction references", () => {
  const key = {
    remoteJid: "120363000000@g.us",
    id: "message-1",
    fromMe: false,
    participant: "15551234567@s.whatsapp.net",
  };
  assert.deepEqual(decodeRef("personal", encodeRef("personal", key)), key);
  assert.throws(() => decodeRef("other", encodeRef("personal", key)), /invalid message_ref/);
  assert.notEqual(eventID(key), eventID({ ...key, participant: "15550000000@s.whatsapp.net" }));
});

test("extracts wrapped text and media placeholders", () => {
  assert.equal(extractText({ ephemeralMessage: { message: { conversation: "hello" } } }), "hello");
  assert.equal(extractText({ imageMessage: {} }), "[whatsappweb image]");
});

test("enforces configured reaction levels", () => {
  assert.equal(normalizeReactionLevel(undefined), "minimal");
  assert.throws(() => validateReaction("off", "👍"), /disabled/);
  assert.throws(() => validateReaction("ack", "✅"), /disabled/);
  assert.throws(() => validateReaction("minimal", "🧪"), /not allowed/);
  assert.doesNotThrow(() => validateReaction("minimal", "👍"));
  assert.doesNotThrow(() => validateReaction("extensive", "🧪"));
});
