import { pathToFileURL } from "node:url";
import path from "node:path";

const [
  compiledOpenclawDir,
  relay,
  goPubkey,
  taskId,
  tsClaimAtText,
  scenarioNowText,
] = process.argv.slice(2);

if (!compiledOpenclawDir || !relay || !goPubkey || !taskId) {
  throw new Error(
    "usage: openclaw-peer.mjs <compiled-openclaw-nostr-dir> <relay> <go-pubkey> "
      + "<task-id> <ts-claim-at> <scenario-now>",
  );
}

const fleetTasks = await import(pathToFileURL(
  path.join(compiledOpenclawDir, "src", "fleet-tasks.js"),
).href);
const fleetAgent = await import(pathToFileURL(
  path.join(compiledOpenclawDir, "src", "fleet-agent.js"),
).href);
const nostrTools = await import(pathToFileURL(
  path.join(compiledOpenclawDir, "node_modules", "nostr-tools", "lib", "esm", "index.js"),
).href);

const {
  FLEET_TASK_KIND,
  FLEET_TASK_SCHEMA,
  createFleetTaskService,
} = fleetTasks;
const { createLocalKeyFleetAgentSigner } = fleetAgent;
const { SimplePool, finalizeEvent, generateSecretKey } = nostrTools;

const tsClaimAt = Number(tsClaimAtText);
const scenarioNow = Number(scenarioNowText);
if (!Number.isSafeInteger(tsClaimAt) || !Number.isSafeInteger(scenarioNow)) {
  throw new Error("claim and scenario timestamps must be safe integers");
}

const secretKey = generateSecretKey();
const signer = createLocalKeyFleetAgentSigner(secretKey);
const tsPubkey = await signer.getPublicKey();
const service = await createFleetTaskService({
  signer,
  relays: [relay],
  trust: { type: "static", pubkeys: [goPubkey, tsPubkey] },
  claimSettlementSeconds: 0,
  initialSyncTimeoutMs: 5_000,
  maxFutureSkewSeconds: 600,
  now: () => scenarioNow,
});

try {
  const sync = await service.start();
  if (sync.initialSync !== "eose") {
    throw new Error(`initial sync did not reach EOSE: ${sync.initialSync}`);
  }
  const initial = service.getTask(taskId);
  if (initial?.winningClaim?.pubkey !== goPubkey) {
    throw new Error("openclaw-nostr did not ingest the Go-authored relay state");
  }

  const claimedAt = new Date(tsClaimAt * 1000).toISOString().replace(".000Z", "Z");
  const task = {
    schema_version: FLEET_TASK_SCHEMA,
    id: taskId,
    title: "Go and TypeScript interoperability",
    status: "in_progress",
    priority: 1,
    assignee: "openclaw-peer",
    labels: ["interop", "nip-cas-0006"],
    dependencies: [],
    notes: "TypeScript contender published through the live relay.",
    created_at: new Date((tsClaimAt - 10) * 1000).toISOString().replace(".000Z", "Z"),
    updated_at: claimedAt,
    started_at: claimedAt,
    claimed_at: claimedAt,
  };
  const claimEvent = finalizeEvent({
    kind: FLEET_TASK_KIND,
    created_at: tsClaimAt,
    tags: [
      ["d", `task:${taskId}`],
      ["domain", "task"],
      ["schema", FLEET_TASK_SCHEMA],
      ["status", "in_progress"],
      ["priority", "P1"],
      ["assignee", "openclaw-peer"],
      ["t", "interop"],
      ["t", "nip-cas-0006"],
    ],
    content: JSON.stringify(task),
  }, secretKey);

  const pool = new SimplePool();
  try {
    await Promise.any(pool.publish([relay], claimEvent));
  } finally {
    pool.close([relay]);
  }
  // A real relay may deliver the event to the live subscription before this
  // local ingest. In that case ingest returns false because it deduplicates the
  // already-observed event; the resulting view is the authoritative assertion.
  service.ingest(claimEvent);
  const claimed = service.getTask(taskId);
  if (claimed?.winningClaim?.id !== claimEvent.id) {
    throw new Error("openclaw-nostr did not select the earlier TypeScript claim");
  }

  const continuation = await service.publishTask({
    baseEventId: claimEvent.id,
    task: {
      ...claimed.effective.task,
      status: "blocked",
      blocked_at: new Date(scenarioNow * 1000).toISOString().replace(".000Z", "Z"),
      updated_at: new Date(scenarioNow * 1000).toISOString().replace(".000Z", "Z"),
      notes: "Cross-runtime claim convergence verified; blocked only as an interop fixture.",
    },
  });
  if (continuation.winningClaim?.id !== claimEvent.id) {
    throw new Error("openclaw-nostr continuation lost the winning claim lineage");
  }

  process.stdout.write(JSON.stringify({
    tsPubkey,
    claimEventId: claimEvent.id,
    continuationEventId: continuation.effective.event.id,
    winningClaimId: continuation.winningClaim.id,
  }));
} finally {
  await service.close();
}
