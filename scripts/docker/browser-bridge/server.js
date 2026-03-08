/**
 * swarmstr Browser Bridge Server
 *
 * An HTTP server that wraps Playwright/Chromium for browser automation.
 * Compatible with OpenClaw browser bridge API.
 *
 * Environment variables:
 *   SANDBOX_LISTEN      - host:port to listen on (default: 0.0.0.0:3500)
 *   SANDBOX_AUTH_TOKEN  - bearer token required on all requests (optional)
 *   BROWSER_EXECUTABLE  - path to Chrome/Chromium executable (optional)
 *   EVALUATE_ENABLED    - enable JS evaluation (default: "1")
 *   SCREENSHOT_TIMEOUT  - screenshot timeout ms (default: 30000)
 */
"use strict";

const express = require("express");
const { chromium } = require("playwright");

const [host, portStr] = (process.env.SANDBOX_LISTEN || "0.0.0.0:3500").split(":");
const PORT = parseInt(portStr || "3500", 10);
const AUTH_TOKEN = process.env.SANDBOX_AUTH_TOKEN || "";
const EVALUATE_ENABLED = process.env.EVALUATE_ENABLED !== "0";
const SCREENSHOT_TIMEOUT = parseInt(process.env.SCREENSHOT_TIMEOUT || "30000", 10);

const app = express();
app.use(express.json({ limit: "10mb" }));

// ── Auth middleware ────────────────────────────────────────────────────────
app.use((req, res, next) => {
  if (!AUTH_TOKEN) return next();
  const header = req.headers["authorization"] || "";
  const token = header.replace(/^Bearer\s+/i, "").trim();
  if (token !== AUTH_TOKEN) {
    return res.status(401).json({ error: "unauthorized" });
  }
  next();
});

// ── Browser state ─────────────────────────────────────────────────────────
let browser = null;
let defaultPage = null;

async function ensureBrowser() {
  if (!browser || !browser.isConnected()) {
    const launchOpts = {
      executablePath: process.env.BROWSER_EXECUTABLE || undefined,
      headless: true,
      args: [
        "--no-sandbox",
        "--disable-setuid-sandbox",
        "--disable-dev-shm-usage",
        "--disable-gpu",
        "--no-first-run",
        "--no-zygote",
      ],
    };
    browser = await chromium.launch(launchOpts);
  }
  return browser;
}

async function getPage(targetId) {
  const br = await ensureBrowser();
  const contexts = br.contexts();
  for (const ctx of contexts) {
    for (const page of ctx.pages()) {
      if (!targetId || page.url().includes(targetId) || String(page._guid).includes(targetId)) {
        return page;
      }
    }
  }
  // Create a new page if none found.
  const ctx = contexts[0] || await br.newContext();
  const page = await ctx.newPage();
  return page;
}

// ── Health check ─────────────────────────────────────────────────────────
app.get("/healthz", (_req, res) => {
  res.json({ ok: true, version: "1.0.0" });
});

// ── Status ───────────────────────────────────────────────────────────────
app.get("/", async (_req, res) => {
  try {
    const br = await ensureBrowser();
    const tabs = [];
    for (const ctx of br.contexts()) {
      for (const page of ctx.pages()) {
        tabs.push({ url: page.url(), title: await page.title().catch(() => ""), isClosed: page.isClosed() });
      }
    }
    res.json({ ok: true, connected: true, tabs });
  } catch (err) {
    res.json({ ok: false, connected: false, error: String(err) });
  }
});

// ── Tabs ─────────────────────────────────────────────────────────────────
app.get("/tabs", async (_req, res) => {
  try {
    const br = await ensureBrowser();
    const tabs = [];
    for (const ctx of br.contexts()) {
      for (const page of ctx.pages()) {
        tabs.push({
          targetId: page._guid,
          url: page.url(),
          title: await page.title().catch(() => ""),
        });
      }
    }
    res.json({ tabs });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

app.post("/tabs/navigate", async (req, res) => {
  const { url, targetId } = req.body || {};
  if (!url) return res.status(400).json({ error: "url is required" });
  try {
    const page = await getPage(targetId);
    await page.goto(url, { waitUntil: "domcontentloaded", timeout: 30000 });
    res.json({ ok: true, url: page.url(), title: await page.title() });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── Snapshot (DOM accessibility tree) ────────────────────────────────────
app.get("/snapshot", async (req, res) => {
  await handleSnapshot(req, res);
});
app.post("/snapshot", async (req, res) => {
  await handleSnapshot(req, res);
});

async function handleSnapshot(req, res) {
  const targetId = (req.body || {}).targetId || req.query.targetId;
  try {
    const page = await getPage(targetId);
    const snapshot = await page.accessibility.snapshot();
    const html = await page.content();
    res.json({
      ok: true,
      url: page.url(),
      title: await page.title(),
      snapshot,
      htmlLength: html.length,
    });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
}

// ── Screenshot ───────────────────────────────────────────────────────────
app.get("/screenshot", async (req, res) => {
  await handleScreenshot(req, res);
});
app.post("/screenshot", async (req, res) => {
  await handleScreenshot(req, res);
});

async function handleScreenshot(req, res) {
  const body = req.body || {};
  const targetId = body.targetId || req.query.targetId;
  const fullPage = body.fullPage === true || req.query.fullPage === "true";
  try {
    const page = await getPage(targetId);
    const data = await page.screenshot({
      type: "png",
      fullPage,
      timeout: SCREENSHOT_TIMEOUT,
    });
    const base64 = data.toString("base64");
    res.json({
      ok: true,
      url: page.url(),
      mimeType: "image/png",
      data: base64,
    });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
}

// ── Evaluate ─────────────────────────────────────────────────────────────
app.post("/evaluate", async (req, res) => {
  if (!EVALUATE_ENABLED) {
    return res.status(403).json({ error: "evaluate is disabled" });
  }
  const { script, targetId } = req.body || {};
  if (!script) return res.status(400).json({ error: "script is required" });
  try {
    const page = await getPage(targetId);
    const result = await page.evaluate(script);
    res.json({ ok: true, result });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── Act (click, fill, press, scroll, hover, select, wait) ─────────────────
app.post("/act", async (req, res) => {
  const { kind, ref, value, targetId, key, timeoutMs, doubleClick } = req.body || {};
  if (!kind) return res.status(400).json({ error: "kind is required" });

  try {
    const page = await getPage(targetId);
    const timeout = timeoutMs || 10000;

    switch (kind) {
      case "navigate": {
        const url = ref || value;
        if (!url) return res.status(400).json({ error: "ref/value (url) is required" });
        await page.goto(url, { waitUntil: "domcontentloaded", timeout: 30000 });
        return res.json({ ok: true, url: page.url() });
      }
      case "click": {
        if (!ref) return res.status(400).json({ error: "ref is required" });
        if (doubleClick) {
          await page.dblclick(ref, { timeout });
        } else {
          await page.click(ref, { timeout });
        }
        return res.json({ ok: true });
      }
      case "fill": {
        if (!ref) return res.status(400).json({ error: "ref is required" });
        await page.fill(ref, value || "", { timeout });
        return res.json({ ok: true });
      }
      case "press": {
        if (!ref && !key) return res.status(400).json({ error: "ref or key is required" });
        if (ref) {
          await page.press(ref, key || "Enter", { timeout });
        } else {
          await page.keyboard.press(key);
        }
        return res.json({ ok: true });
      }
      case "scroll": {
        const x = (req.body.x || 0);
        const y = (req.body.y || 0);
        await page.evaluate(`window.scrollBy(${x}, ${y})`);
        return res.json({ ok: true });
      }
      case "hover": {
        if (!ref) return res.status(400).json({ error: "ref is required" });
        await page.hover(ref, { timeout });
        return res.json({ ok: true });
      }
      case "select": {
        if (!ref) return res.status(400).json({ error: "ref is required" });
        await page.selectOption(ref, value || "", { timeout });
        return res.json({ ok: true });
      }
      case "focus": {
        if (!ref) return res.status(400).json({ error: "ref is required" });
        await page.focus(ref, { timeout });
        return res.json({ ok: true });
      }
      case "wait": {
        const ms = parseInt(value || "1000", 10);
        await page.waitForTimeout(ms);
        return res.json({ ok: true });
      }
      case "waitForSelector": {
        if (!ref) return res.status(400).json({ error: "ref is required" });
        await page.waitForSelector(ref, { timeout });
        return res.json({ ok: true });
      }
      default:
        return res.status(400).json({ error: `unknown kind: ${kind}` });
    }
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── Storage ──────────────────────────────────────────────────────────────
app.get("/storage", async (req, res) => {
  const targetId = req.query.targetId;
  try {
    const page = await getPage(targetId);
    const storageState = await page.context().storageState();
    res.json({ ok: true, storage: storageState });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── Fetch (basic HTTP fetch through the browser context) ──────────────────
app.post("/fetch", async (req, res) => {
  const { url, method, headers, body: bodyData, targetId } = req.body || {};
  if (!url) return res.status(400).json({ error: "url is required" });
  try {
    const page = await getPage(targetId);
    const response = await page.evaluate(async ([fetchUrl, fetchMethod, fetchHeaders, fetchBody]) => {
      const opts = { method: fetchMethod || "GET", headers: fetchHeaders || {} };
      if (fetchBody) opts.body = fetchBody;
      const r = await fetch(fetchUrl, opts);
      const text = await r.text();
      return { status: r.status, headers: Object.fromEntries(r.headers.entries()), body: text };
    }, [url, method, headers, bodyData]);
    res.json({ ok: true, ...response });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── Graceful shutdown ─────────────────────────────────────────────────────
process.on("SIGTERM", async () => {
  console.log("SIGTERM received, shutting down...");
  if (browser) await browser.close().catch(() => {});
  process.exit(0);
});

// ── Start ─────────────────────────────────────────────────────────────────
app.listen(PORT, host, () => {
  console.log(`swarmstr browser bridge listening on http://${host}:${PORT}`);
  // Warm up browser at startup
  ensureBrowser().then(() => {
    console.log("Browser started");
  }).catch((err) => {
    console.error("Browser startup warning:", err.message);
  });
});
