#!/usr/bin/env node
import { spawn, spawnSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, rmSync } from "node:fs";
import { request } from "node:http";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../", import.meta.url));
const chromePath = process.env.CHROME ?? "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const failures = [];

function check(name, ok, detail = "") {
  process.stdout.write(`${ok ? "ok  " : "FAIL"} ${name}${ok || !detail ? "" : ` (${detail})`}\n`);
  if (!ok) failures.push(name);
}

if (process.argv.includes("--help")) {
  process.stdout.write("Usage: npm run check:panel\nBuilds the panel and engine, starts an isolated engine and checks the full and compact views in headless Chrome (set CHROME to use another binary).\n");
  process.exit(0);
}
if (typeof WebSocket === "undefined") {
  process.stderr.write("check:panel needs Node 22 or newer (built-in WebSocket).\n");
  process.exit(1);
}
if (!existsSync(chromePath)) {
  process.stderr.write(`Google Chrome not found at ${chromePath}. Set CHROME to a Chrome or Chromium binary.\n`);
  process.exit(1);
}

function freePort() {
  return new Promise((resolve, reject) => {
    const server = createServer();
    server.on("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const { port } = server.address();
      server.close(() => resolve(port));
    });
  });
}

function engineCall(socketPath, method, path) {
  return new Promise((resolve, reject) => {
    const call = request({ socketPath, method, path, timeout: 2000 }, (response) => {
      let body = "";
      response.on("data", (chunk) => (body += chunk));
      response.on("end", () => resolve({ status: response.statusCode, body }));
    });
    call.on("error", reject);
    call.on("timeout", () => call.destroy(new Error("timeout")));
    call.end();
  });
}

async function panelURL(sockets) {
  for (let attempt = 0; attempt < 60; attempt++) {
    for (const socketPath of sockets) {
      try {
        const answer = await engineCall(socketPath, "GET", "/v1/panel");
        if (answer.status === 200) return { socketPath, url: JSON.parse(answer.body).url };
      } catch {}
    }
    await sleep(250);
  }
  throw new Error("the engine did not start");
}

async function browser(port) {
  let page;
  for (let attempt = 0; attempt < 40 && !page; attempt++) {
    await sleep(250);
    try {
      page = (await (await fetch(`http://127.0.0.1:${port}/json`)).json()).find((target) => target.type === "page");
    } catch {}
  }
  if (!page) throw new Error("Chrome did not start");
  const socket = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((resolve) => socket.addEventListener("open", resolve));
  let id = 0;
  const pending = new Map();
  const errors = [];
  socket.addEventListener("message", (message) => {
    const data = JSON.parse(message.data);
    if (data.method === "Runtime.exceptionThrown") errors.push(data.params.exceptionDetails.exception?.description ?? data.params.exceptionDetails.text);
    if (data.method === "Runtime.consoleAPICalled" && data.params.type === "error") errors.push(data.params.args.map((arg) => arg.value ?? arg.description).join(" "));
    pending.get(data.id)?.(data);
    pending.delete(data.id);
  });
  const send = (method, params = {}) => new Promise((resolve) => {
    const next = ++id;
    pending.set(next, resolve);
    socket.send(JSON.stringify({ id: next, method, params }));
  });
  const run = async (expression) => {
    const answer = await send("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
    if (answer.result.exceptionDetails) throw new Error(answer.result.exceptionDetails.exception?.description ?? "script failed");
    return answer.result.result.value;
  };
  const size = (width, height) => send("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: 1, mobile: false });
  await send("Runtime.enable");
  return { send, run, size, errors, close: () => socket.close() };
}

async function checkViews(page, url) {
  const { run, send, size } = page;
  await size(1440, 900);
  await send("Page.navigate", { url });
  await sleep(4000);
  const visible = (selector) => run(`(() => { const element = document.querySelector(${JSON.stringify(selector)}); return Boolean(element && element.offsetParent !== null); })()`);
  const sections = ["section-status", "section-connect", "section-rules", "section-test", "section-history"];
  const sectionsVisible = () => run(`${JSON.stringify(sections)}.filter((id) => document.getElementById(id)?.offsetParent !== null).length`);

  const openPanels = () => run(`["section-rules", "section-test", "section-history"].filter((id) => document.getElementById(id)?.offsetParent !== null)`);
  check("full view shows Status and Connect with Rules, Test and History closed under tiles", (await sectionsVisible()) === 2 && (await run(`document.querySelectorAll(".section-tile").length`)) === 3 && (await openPanels()).length === 0);
  check("full view shows the totals", await visible("#total-masked"));
  check("full view keeps the header navigation", (await run(`document.querySelectorAll(".section-nav a").length`)) === 5);

  await run(`document.getElementById("open-demo").click()`);
  await sleep(800);
  await run(`document.getElementById("send").click()`);
  for (let attempt = 0; attempt < 40 && (await run(`document.getElementById("send").disabled`)); attempt++) await sleep(250);
  await run(`document.getElementById("close-demo").click()`);
  await sleep(3500);
  check("the History tile counts the new prompt", (await run(`document.querySelector("#tile-section-history .tile-badge")?.textContent.trim()`)) === "1 new");
  await run(`document.getElementById("more-details").click()`);
  await sleep(800);
  check("More details opens the History panel and clears its new count", (await openPanels()).join() === "section-history" && !(await run(`document.querySelector("#tile-section-history .tile-badge")`)));
  await run(`[...document.querySelectorAll(".history-search button")].find((button) => button.textContent.trim().startsWith("By prompt")).click()`);
  await sleep(500);
  check("full history lists the demo prompt", (await run(`document.querySelectorAll("#event-rows tr.event-row").length`)) >= 1);
  check("full view shows the live strip without a Reset button", (await run(`document.querySelector("#section-status .first-screen #live-state")?.textContent.includes("Live") && !document.getElementById("compact-reset")`)) === true);
  check("full view lists the masked data types", (await run(`document.querySelectorAll("#live-types li").length`)) >= 1);
  const recent = await run(`document.querySelectorAll("#live-activity #activity-rows tr").length`);
  check("full view shows up to five recent activity rows", recent >= 1 && recent <= 5, `${recent} rows`);
  check("full view chart shows bars", (await run(`document.querySelectorAll("#live-activity .bars .bar").length`)) >= 1);
  check("full view offers only bar sizes slower than its refresh", (await run(`[...document.querySelectorAll("#live-activity .bar-size button")].map((button) => button.textContent.trim()).join(",")`)) === "4 s,10 s,1 min");
  await run(`[...document.querySelectorAll(".section-nav a")].find((link) => link.textContent === "Rules").click()`);
  await sleep(800);
  check("the Rules link opens only the Rules panel", (await openPanels()).join() === "section-rules" && (await run(`document.getElementById("tile-section-rules").getAttribute("aria-expanded")`)) === "true");
  await run(`document.getElementById("tile-section-rules").click()`);
  await sleep(300);
  check("a second click on an open tile closes it", (await openPanels()).length === 0);

  await run(`document.getElementById("compact-view").click()`);
  await sleep(3500);
  check("compact view replaces the sections", (await visible("#compact-view-page")) && (await sectionsVisible()) === 0);
  check("compact view keeps the header navigation without an active section", (await run(`document.querySelectorAll(".section-nav a").length === 5 && !document.querySelector(".section-nav a.active")`)));
  check("compact ring lists the masked types", (await run(`document.querySelectorAll(".legend li").length`)) >= 1);
  check("compact view shows the connection map", await visible(".connection-card .map"));
  check("an empty connection map draws no dangling lines", (await run(`document.querySelectorAll(".connection-card .map-links path").length`)) === 0);
  check("compact activity lists category rows", (await run(`document.querySelectorAll("#compact-view-page #activity-rows tr").length`)) >= 1);
  check("compact chart shows bars", (await run(`document.querySelectorAll("#compact-view-page .bars .bar").length`)) >= 1);
  check("compact view leaves the full view live card out", (await run(`document.getElementById("live-activity") === null && document.querySelectorAll("#live-state").length === 1`)) === true);
  check("compact telemetry strip is live", (await run(`document.getElementById("live-state")?.textContent.includes("Live")`)) === true);
  for (const [width, height] of [[1440, 900], [1280, 800]]) {
    await size(width, height);
    await sleep(600);
    const fit = await run(`({ bottom: document.querySelector(".compact-grid").getBoundingClientRect().bottom, height: innerHeight, overflow: document.documentElement.scrollWidth > document.documentElement.clientWidth })`);
    check(`compact view fits ${width}×${height} without scrolling`, fit.bottom <= fit.height && !fit.overflow, `bottom ${Math.round(fit.bottom)}, viewport ${fit.height}`);
  }
  await size(390, 900);
  await sleep(600);
  check("compact view has no sideways scroll on a phone", !(await run(`document.documentElement.scrollWidth > document.documentElement.clientWidth`)));
  await size(1440, 900);
  await sleep(400);

  await run(`document.getElementById("theme-toggle").click()`);
  await sleep(300);
  check("light theme applies to the compact view", (await run(`document.documentElement.dataset.theme`)) === "light");
  await run(`document.getElementById("compact-reset").click()`);
  await sleep(300);
  const dialogOpen = await run(`document.getElementById("app-dialog").open`);
  await run(`document.querySelector("#app-dialog button.secondary").click()`);
  await sleep(300);
  check("reset asks in the app dialog and Cancel keeps the data", dialogOpen && !(await run(`document.getElementById("app-dialog").open`)) && (await run(`document.querySelectorAll("#compact-view-page #activity-rows tr").length`)) >= 1);

  await run(`[...document.querySelectorAll(".section-nav a")].find((link) => link.textContent === "History").click()`);
  await sleep(1200);
  check("a section link leaves the compact view", !(await visible("#compact-view-page")) && (await visible("#section-history")));
  check("the light theme stays on in the full view", (await run(`document.documentElement.dataset.theme`)) === "light");
  check("Status, Connect and the History panel are back", (await sectionsVisible()) === 3 && (await openPanels()).join() === "section-history");

  await run(`document.getElementById("clear-events").click()`);
  await sleep(300);
  const clearAsks = await run(`document.getElementById("app-dialog").open`);
  await run(`document.querySelector("#app-dialog button.secondary").click()`);
  await sleep(300);
  check("Clear history asks first and Cancel keeps the history", clearAsks && (await run(`document.querySelectorAll("#live-activity #activity-rows tr").length`)) >= 1);
  await run(`document.getElementById("clear-events").click()`);
  await sleep(300);
  await run(`document.querySelector("#app-dialog .danger-button").click()`);
  await sleep(1500);
  check("confirmed Clear history empties the activity, types and chart", (await run(`document.querySelectorAll("#live-activity #activity-rows tr").length + document.querySelectorAll("#live-types li").length + document.querySelectorAll("#live-activity .bars .bar").length`)) === 0);
  check("no JavaScript errors", page.errors.length === 0, page.errors.slice(0, 3).join(" | "));
}

const work = mkdtempSync(join(tmpdir(), "pp-check-"));
const home = join(work, "home");
const temp = join(work, "tmp");
mkdirSync(home);
mkdirSync(temp);
const binary = join(work, "phantomprompt");
let engine;
let chrome;
let page;
let socketPath;
try {
  const build = spawnSync("go", ["build", "-o", binary, "./cmd/phantomprompt"], { cwd: root, stdio: "inherit" });
  if (build.error || build.status !== 0) throw new Error("go build failed");
  const [ui, proxy, gateway, debug] = await Promise.all([freePort(), freePort(), freePort(), freePort()]);
  engine = spawn(binary, ["web", "-open=false", "-ui-listen", `127.0.0.1:${ui}`, "-proxy-listen", `127.0.0.1:${proxy}`, "-gateway-listen", `127.0.0.1:${gateway}`], { env: { ...process.env, HOME: home, TMPDIR: `${temp}/` }, stdio: "ignore" });
  const sockets = [join(home, "Library", "Application Support", "PhantomPrompt", "engine.sock"), join(temp, `phantomprompt-${process.getuid()}`, "engine.sock")].filter((path) => path.length <= 104);
  const panel = await panelURL(sockets);
  socketPath = panel.socketPath;
  chrome = spawn(chromePath, ["--headless=new", `--user-data-dir=${join(work, "chrome")}`, `--remote-debugging-port=${debug}`, "--window-size=1440,900", "about:blank"], { stdio: "ignore" });
  page = await browser(debug);
  await checkViews(page, panel.url);
} catch (error) {
  check("panel check ran to the end", false, error.message);
} finally {
  page?.close();
  chrome?.kill();
  if (socketPath) await engineCall(socketPath, "POST", "/v1/shutdown").catch(() => {});
  await sleep(500);
  engine?.kill();
  rmSync(work, { recursive: true, force: true });
}
process.stdout.write(failures.length ? `\n${failures.length} check(s) failed.\n` : "\nAll panel checks passed.\n");
process.exitCode = failures.length ? 1 : 0;
