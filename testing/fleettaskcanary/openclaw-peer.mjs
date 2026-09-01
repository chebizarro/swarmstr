import { createInterface } from "node:readline";
import { pathToFileURL } from "node:url";
import path from "node:path";

const [
  compiledOpenclawDir,
  relay,
  goPubkey,
  tsSecretHex,
  taskId,
  queueId,
  epicId,
] = process.argv.slice(2);

if (!compiledOpenclawDir || !relay || !goPubkey || !tsSecretHex || !taskId || !queueId || !epicId) {
  throw new Error(
    "usage: openclaw-peer.mjs <compiled-openclaw-dir> <relay> <go-pubkey> "
      + "<ts-secret-hex> <task-id> <queue-id> <epic-id>",
  );
}
if (!/^[0-9a-f]{64}$/i.test(tsSecretHex)) {
  throw new Error("ts-secret-hex must be a 32-byte hex key");
}

const fleetTasks = await import(pathToFileURL(
  path.join(compiledOpenclawDir, "src", "fleet-tasks.js"),
).href);
const fleetTaskTool = await import(pathToFileURL(
  path.join(compiledOpenclawDir, "src", "fleet-tasks-tool.js"),
).href);
const fleetAgent = await import(pathToFileURL(
  path.join(compiledOpenclawDir, "src", "fleet-agent.js"),
).href);
const nostrTools = await import(pathToFileURL(
  path.join(compiledOpenclawDir, "node_modules", "nostr-tools", "lib", "esm", "index.js"),
).href);

const { FLEET_TASK_COLLECTION_KIND, createFleetTaskService } = fleetTasks;
const { createNostrFleetTasksToolFactory } = fleetTaskTool;
const { createLocalKeyFleetAgentSigner } = fleetAgent;
const { SimplePool } = nostrTools;

const tsSecret = Uint8Array.from(Buffer.from(tsSecretHex, "hex"));
const signer = createLocalKeyFleetAgentSigner(tsSecret);
const tsPubkey = await signer.getPublicKey();
const collectionSources = [
  { author: tsPubkey, dTag: `queue:${queueId}` },
  { author: tsPubkey, dTag: `epic:${epicId}` },
];
const runtimeErrors = [];
const service = await createFleetTaskService({
  signer,
  relays: [relay],
  trust: { type: "static", pubkeys: [goPubkey, tsPubkey] },
  collectionSources,
  claimSettlementSeconds: 1,
  initialSyncTimeoutMs: 5_000,
  maxFutureSkewSeconds: 600,
  onError(error, context) {
    runtimeErrors.push(`${context}: ${error.message}`);
  },
});

const sync = await service.start();
if (sync.initialSync !== "eose") {
  throw new Error(`initial sync did not reach EOSE: ${sync.initialSync}`);
}
const tool = createNostrFleetTasksToolFactory({ resolveService: () => service })({
  agentAccountId: "fleet-task-canary",
});
if (!tool || tool.name !== "nostr_fleet_tasks") {
  throw new Error("nostr_fleet_tasks tool did not materialize");
}

function write(value) {
  process.stdout.write(`${JSON.stringify(value)}\n`);
}

function parseToolResult(result) {
  const text = result?.content?.[0]?.text;
  if (typeof text !== "string") {
    throw new Error("nostr_fleet_tasks returned a non-text tool result");
  }
  return JSON.parse(text);
}

function taskMatches(view, expected) {
  if (!view?.effective) return false;
  if (expected.status && view.effective.task.status !== expected.status) return false;
  if (expected.effectiveEventId && view.effective.event.id !== expected.effectiveEventId) return false;
  if (expected.winningClaimId && view.winningClaim?.id !== expected.winningClaimId) return false;
  return true;
}

async function waitForTask(expected) {
  const current = service.getTask(taskId);
  if (taskMatches(current, expected)) return current;
  return new Promise((resolve) => {
    const unsubscribe = service.subscribeTasks((changedTaskId, view) => {
      if (changedTaskId !== taskId || !taskMatches(view, expected)) return;
      unsubscribe();
      resolve(view);
    });
    const afterSubscribe = service.getTask(taskId);
    if (taskMatches(afterSubscribe, expected)) {
      unsubscribe();
      resolve(afterSubscribe);
    }
  });
}

async function publishCollection(dTag, taskIds) {
  const [type, id] = String(dTag).split(":", 2);
  if ((type !== "queue" && type !== "epic") || !id) {
    throw new Error("collection dTag must identify a queue or epic");
  }
  const tags = [["d", dTag]];
  for (const memberTaskId of taskIds) {
    const view = service.getTask(memberTaskId);
    if (!view?.effective || view.effective.task[type] !== id) {
      throw new Error(`task ${memberTaskId} does not agree with collection ${dTag}`);
    }
    tags.push(["a", `30900:${view.effective.event.pubkey}:task:${memberTaskId}`, ""]);
  }
  const event = await signer.signEvent({
    kind: FLEET_TASK_COLLECTION_KIND,
    created_at: Math.floor(Date.now() / 1000),
    tags,
    content: "",
  });
  const pool = new SimplePool();
  try {
    await Promise.any(pool.publish([relay], event));
  } finally {
    pool.close([relay]);
  }
  service.ingest(event);
  return service.getCollection({ author: tsPubkey, dTag });
}

async function publishProbe(params) {
  const kind = Number(params.kind);
  const dTag = String(params.dTag ?? "");
  if (!Number.isSafeInteger(kind) || !dTag) throw new Error("probe kind and dTag are required");
  const tags = [["d", dTag]];
  if (params.schema) tags.push(["schema", String(params.schema)]);
  if (kind === 30900) {
    tags.push(["domain", "task"], ["status", "open"], ["priority", "P2"]);
  }
  const event = await signer.signEvent({
    kind,
    created_at: Math.floor(Date.now() / 1000),
    tags,
    content: kind === 30900 ? "{}" : "",
  });
  const pool = new SimplePool();
  try {
    await Promise.any(pool.publish([relay], event));
  } finally {
    pool.close([relay]);
  }
  return { eventId: event.id };
}

async function handle(command) {
  switch (command.op) {
    case "tool":
      return parseToolResult(await tool.execute(`canary-${command.id}`, command.params ?? {}));
    case "wait_task":
      return waitForTask(command.expected ?? {});
    case "publish_collection":
      return publishCollection(String(command.dTag), command.taskIds ?? []);
    case "get_collection":
      return service.getCollection({ author: tsPubkey, dTag: String(command.dTag) });
    case "publish_probe":
      return publishProbe(command);
    case "diagnostics":
      return {
        runtimeErrors: [...runtimeErrors],
        taskCount: service.listTasks().length,
        collections: service.listCollections(),
      };
    case "shutdown":
      return { shuttingDown: true };
    default:
      throw new Error(`unknown peer operation: ${String(command.op)}`);
  }
}

write({ type: "ready", tsPubkey, toolName: tool.name, initialSync: sync.initialSync });
const input = createInterface({ input: process.stdin, crlfDelay: Infinity });
let shutdown = false;
try {
  for await (const line of input) {
    if (!line.trim()) continue;
    let command;
    try {
      command = JSON.parse(line);
      const result = await handle(command);
      write({ id: command.id, ok: true, result });
      if (command.op === "shutdown") {
        shutdown = true;
        break;
      }
    } catch (error) {
      write({
        id: command?.id,
        ok: false,
        error: error instanceof Error ? error.message : String(error),
      });
    }
  }
} finally {
  await service.close();
  if (!shutdown) process.exitCode = 1;
}
