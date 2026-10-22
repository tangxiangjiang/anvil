// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Real-browser smoke for the p_0.45.5 split and p_0.45.6 navigation states.
 *
 * No npm dependency is required. The script starts anvil-web with a temporary
 * SQLite database, controls a locally installed Chromium through CDP, and
 * replaces only cluster/node API methods with in-page, read-only fixtures.
 */
import assert from "node:assert/strict";
import fs from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const SOURCE = path.resolve(HERE, "../../../..");
const temp = fs.mkdtempSync(path.join(os.tmpdir(), "anvil-web-browser-smoke-"));
const children = [];

function chromiumPath() {
  const candidates = [
    process.env.CHROME_PATH,
    process.platform === "win32"
      ? path.join(process.env.ProgramFiles || "", "Google/Chrome/Application/chrome.exe")
      : "/usr/bin/google-chrome",
    process.platform === "win32"
      ? path.join(process.env["ProgramFiles(x86)"] || "", "Microsoft/Edge/Application/msedge.exe")
      : "/usr/bin/chromium",
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  ].filter(Boolean);
  const found = candidates.find((candidate) => fs.existsSync(candidate));
  if (!found) throw new Error("Chromium not found; set CHROME_PATH");
  return found;
}

function freePort() {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.on("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const { port } = server.address();
      server.close(() => resolve(port));
    });
  });
}

async function eventually(fn, message, timeoutMs = 30000) {
  const until = Date.now() + timeoutMs;
  let last;
  while (Date.now() < until) {
    try {
      const value = await fn();
      if (value) return value;
    } catch (error) {
      last = error;
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(`${message}${last ? `: ${last.message}` : ""}`);
}

class CDP {
  constructor(url) {
    this.ws = new WebSocket(url);
    this.next = 1;
    this.pending = new Map();
    this.listeners = new Map();
  }

  async open() {
    await new Promise((resolve, reject) => {
      this.ws.addEventListener("open", resolve, { once: true });
      this.ws.addEventListener("error", reject, { once: true });
    });
    this.ws.addEventListener("message", (event) => {
      const message = JSON.parse(event.data);
      if (message.id) {
        const pending = this.pending.get(message.id);
        if (!pending) return;
        this.pending.delete(message.id);
        if (message.error) pending.reject(new Error(message.error.message));
        else pending.resolve(message.result);
        return;
      }
      for (const listener of this.listeners.get(message.method) || []) listener(message.params || {});
    });
  }

  send(method, params = {}) {
    const id = this.next++;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      this.ws.send(JSON.stringify({ id, method, params }));
    });
  }

  on(method, listener) {
    const listeners = this.listeners.get(method) || [];
    listeners.push(listener);
    this.listeners.set(method, listeners);
  }

  close() {
    this.ws.close();
  }
}

async function evaluate(cdp, expression) {
  const result = await cdp.send("Runtime.evaluate", {
    expression,
    awaitPromise: true,
    returnByValue: true,
  });
  if (result.exceptionDetails) {
    throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text);
  }
  return result.result.value;
}

async function waitJS(cdp, expression, message) {
  return eventually(() => evaluate(cdp, expression), message);
}

async function main() {
  const webPort = await freePort();
  const cdpPort = await freePort();
  const base = `http://127.0.0.1:${webPort}`;
  const config = path.join(temp, "web.json");
  fs.writeFileSync(
    config,
    JSON.stringify({
      listen: `127.0.0.1:${webPort}`,
      db_path: path.join(temp, "smoke.db"),
      scheduler_url: "http://127.0.0.1:1",
      scheduler_token_env: "ANVIL_SCHEDULER_TOKEN",
      fleets: [{ id: "smoke", name: "Smoke", scheduler_url: "http://127.0.0.1:1" }],
    }),
  );

  const web = spawn("go", ["run", "./cmd/anvil-web", "--config", config], {
    cwd: SOURCE,
    env: {
      ...process.env,
      ANVIL_SCHEDULER_TOKEN: "browser-smoke-only",
      ANVIL_WEB_ADMIN_USER: "smoke",
      ANVIL_WEB_ADMIN_PASSWORD: "password123",
    },
    stdio: ["ignore", "pipe", "pipe"],
  });
  children.push(web);
  let webLog = "";
  web.stdout.on("data", (chunk) => (webLog += chunk));
  web.stderr.on("data", (chunk) => (webLog += chunk));
  await eventually(
    async () => {
      const response = await fetch(`${base}/api/v1/setup/status`);
      return response.ok;
    },
    `anvil-web did not start\n${webLog}`,
    60000,
  );

  const chrome = spawn(
    chromiumPath(),
    [
      "--headless=new",
      "--disable-gpu",
      "--no-first-run",
      "--no-default-browser-check",
      `--remote-debugging-port=${cdpPort}`,
      `--user-data-dir=${path.join(temp, "chrome")}`,
      "about:blank",
    ],
    { stdio: "ignore" },
  );
  children.push(chrome);
  const page = await eventually(async () => {
    const response = await fetch(`http://127.0.0.1:${cdpPort}/json/list`);
    const pages = await response.json();
    return pages.find((target) => target.type === "page" && target.webSocketDebuggerUrl);
  }, "Chromium CDP endpoint did not start");

  const cdp = new CDP(page.webSocketDebuggerUrl);
  await cdp.open();
  const errors = [];
  cdp.on("Runtime.exceptionThrown", ({ exceptionDetails }) => {
    errors.push(exceptionDetails.exception?.description || exceptionDetails.text);
  });
  cdp.on("Runtime.consoleAPICalled", ({ type, args }) => {
    if (type !== "error") return;
    errors.push(args.map((arg) => arg.value ?? arg.description ?? "").join(" "));
  });
  await Promise.all([
    cdp.send("Runtime.enable"),
    cdp.send("Page.enable"),
    cdp.send("Log.enable"),
  ]);
  cdp.on("Log.entryAdded", ({ entry }) => {
    if (entry.level === "error" && entry.source !== "network") errors.push(entry.text);
  });

  await cdp.send("Page.navigate", { url: `${base}/#/login` });
  await waitJS(cdp, `document.readyState === "complete" && !!window.Features`, "application did not load");
  await evaluate(
    cdp,
    `(async () => {
      const result = await api.v1.login("smoke", "password123");
      api.setToken(result.token);
      const load = api.v1.delivery;
      api.v1.delivery = () => new Promise((resolve, reject) => {
        window.__releaseDelivery = () => {
          api.v1.delivery = load;
          load().then(resolve, reject);
        };
      });
      location.hash = "#/delivery/requirements";
    })()`,
  );
  await waitJS(
    cdp,
    `!!document.querySelector("#app .del-skeleton") && typeof window.__releaseDelivery === "function"`,
    "Delivery first paint did not show a skeleton",
  );
  await evaluate(cdp, `window.__releaseDelivery()`);  
  await waitJS(cdp, `!!Features.delivery.data`, "Delivery snapshot did not load");
  await waitJS(
    cdp,
    `document.querySelector("#shell-first-run-open")?.textContent.trim() === "Setup checklist 2/4" &&
     !document.querySelector("#shell-first-run")?.hidden`,
    "authoritative first-run checklist did not open at 2/4",
  );
  const firstRun = await evaluate(
    cdp,
    `(() => {
      document.querySelector("[data-first-run-close]").click();
      const closed = document.querySelector("#shell-first-run").hidden;
      const stored = localStorage.getItem(AnvilShell.firstRunStorageKey) || "";
      document.querySelector("#shell-first-run-open").click();
      return { closed, storesIdentifiers: /project|worker|workspace|token/i.test(stored) };
    })()`,
  );
  assert.deepEqual(firstRun, { closed: true, storesIdentifiers: false });
  await waitJS(
    cdp,
    `!document.querySelector("#shell-first-run").hidden`,
    "first-run checklist did not reopen",
  );
  await evaluate(
    cdp,
    `(() => {
      if (!Features.delivery.data.majors.some((m) => m.major === 99)) {
        Features.delivery.data.majors.push({
          major: 99, title: "Empty smoke generation", summary: "", status: "planning"
        });
      }
      Features.delivery.render();
    })()`,
  );

  const deliveryResults = [];
  for (const section of ["requirements", "designs", "roadmap", "test"]) {
    const surfaceSelector = {
      requirements: ".del-req-grid",
      designs: ".del-des-grid",
      roadmap: ".del-roadmap-graph",
      test: ".del-test-grid",
    }[section];
    await evaluate(
      cdp,
      `location.hash = "#/delivery/${section}/m/0";`,
    );
    await waitJS(
      cdp,
      `Features.delivery.el && Features.delivery.parseRoute().section === "${section}" &&
       Features.delivery.parseRoute().routeMajor === 0 &&
       document.querySelector("#app .del-head h2")?.textContent.trim() === "0.x" &&
       !!document.querySelector("#app .del-head .del-scope") &&
       document.querySelector("#app .del-head .del-scope")?.textContent.includes("milestones") &&
       document.querySelector("#app .del-head .del-scope")?.textContent.includes("designs") &&
       !!document.querySelector("${surfaceSelector}") &&
       document.querySelector("#app .del-body")?.textContent.trim().length > 0 &&
       !document.querySelector("#app > .err")`,
      `Delivery ${section} did not render`,
    );
    deliveryResults.push(
      await evaluate(
        cdp,
        `({ section: "${section}", bodyChars: document.querySelector("#app .del-body").textContent.trim().length })`,
      ),
    );
    await evaluate(cdp, `document.querySelector("[data-scope-major='99']").click()`);
    await waitJS(
      cdp,
      `location.hash === "#/delivery/${section}/m/99" &&
       document.querySelector("[data-scope-major='99']")?.getAttribute("aria-pressed") === "true" &&
       !!document.querySelector("#app .del-empty")`,
      `Delivery ${section} generation switch or empty state failed`,
    );
    const emptyAction = await evaluate(
      cdp,
      `(() => {
        const empty = document.querySelector("#app .del-empty");
        const action = empty?.querySelector("a, button");
        return { text: empty?.textContent.trim() || "", action: action?.textContent.trim() || "",
          href: action?.getAttribute("href") || "" };
      })()`,
    );
    assert.ok(emptyAction.text.length > 0, `${section} empty state has no guidance`);
    assert.ok(emptyAction.action.length > 0, `${section} empty state has no action`);
    if (section === "designs") {
      await evaluate(cdp, `document.querySelector("#des-empty-new").click()`);
      await waitJS(cdp, `!document.querySelector("#del-ui-modal").hidden`, "Design empty action did not open");
      await evaluate(cdp, `Features.delivery.closeUiModal()`);
    } else {
      assert.ok(emptyAction.href.startsWith("#/"), `${section} empty action is not navigable`);
    }
  }

  const detailFixture = await evaluate(
    cdp,
    `(() => {
      const eng = Features.delivery.data.eng_reqs.find((e) => e.versions?.length);
      const ver = eng?.versions?.[0];
      const design = Features.delivery.data.designs?.find((item) => item.versions?.length);
      return eng && ver ? {
        engId: eng.id, major: ver.major, minor: ver.minor, patch: ver.patch,
        designId: design?.id || ""
      } : null;
    })()`,
  );
  assert.ok(detailFixture, "seed data has no requirement detail for breadcrumb smoke");
  const reqBase = `#/delivery/requirements/${encodeURIComponent(detailFixture.engId)}`;
  await evaluate(cdp, `location.hash = ${JSON.stringify(`${reqBase}/${detailFixture.major}/${detailFixture.minor}`)}`);
  await waitJS(
    cdp,
    `document.querySelectorAll("#app nav[aria-label='Breadcrumb'] a").length === 3 &&
     !!document.querySelector("#app .del-head .del-scope")`,
    "Milestone breadcrumb or persistent generation scope failed",
  );
  await evaluate(
    cdp,
    `location.hash = ${JSON.stringify(`${reqBase}/${detailFixture.major}/${detailFixture.minor}/${detailFixture.patch}`)}`,
  );
  await waitJS(
    cdp,
    `document.querySelectorAll("#app nav[aria-label='Breadcrumb'] a").length === 4`,
    "Requirement version breadcrumb failed",
  );
  await evaluate(cdp, `document.querySelectorAll("#app nav[aria-label='Breadcrumb'] a")[2].click()`);
  await waitJS(cdp, `location.hash === ${JSON.stringify(reqBase)}`, "Requirement breadcrumb link did not navigate");

  if (detailFixture.designId) {
    await evaluate(cdp, `location.hash = ${JSON.stringify(`#/delivery/designs/${detailFixture.designId}`)}`);
    await waitJS(
      cdp,
      `document.querySelectorAll("#app nav[aria-label='Breadcrumb'] a").length >= 1 &&
       !!document.querySelector("#app .del-head .del-scope")`,
      "Design breadcrumb or generation scope failed",
    );
    const designKeys = await evaluate(
      cdp,
      `(() => {
        const rows = [...document.querySelectorAll('[data-sel-list="design-versions"][data-sel-id]')];
        if (!rows.length) return { rows: 0 };
        rows[0].focus();
        rows[0].dispatchEvent(new KeyboardEvent("keydown", { key: " ", bubbles: true }));
        const selected = rows[0].getAttribute("aria-selected");
        rows[0].dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
        const opened = rows[0].getAttribute("aria-expanded");
        rows[0].dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
        return { rows: rows.length, selected, opened,
          cleared: rows[0].getAttribute("aria-selected"),
          multi: rows[0].parentElement.getAttribute("aria-multiselectable") };
      })()`,
    );
    if (designKeys.rows > 0) {
      assert.deepEqual(designKeys, {
        rows: designKeys.rows,
        selected: "true",
        opened: "true",
        cleared: "false",
        multi: "true",
      });
    }
    await evaluate(cdp, `document.querySelector("#app nav[aria-label='Breadcrumb'] a").click()`);
    await waitJS(cdp, `location.hash === "#/delivery/designs"`, "Design breadcrumb link did not navigate");
  }

  await evaluate(
    cdp,
    `(() => {
      Features.delivery._clustersLoaded = false;
      Features.delivery._wsLoading = false;
      api.v1.clusters = async () => { throw new Error("smoke inventory unavailable"); };
      location.hash = "#/delivery/requirements/projects";
    })()`,
  );
  await waitJS(
    cdp,
    `document.querySelector("#app .err")?.textContent.includes("smoke inventory unavailable") &&
     document.querySelector("#app .err + .muted")?.textContent.includes("Clusters")`,
    "Requirements error state did not explain recovery",
  );

  await evaluate(
    cdp,
    `(() => {
      Features.delivery._clustersLoaded = false;
      Features.delivery._wsLoading = false;
      window.__inventoryStarted = false;
      api.v1.clusters = () => new Promise((resolve) => {
        window.__inventoryStarted = true;
        window.__releaseInventory = () => resolve({ clusters: [] });
      });
      location.hash = "#/delivery/requirements/projects";
      Features.delivery.render();
    })()`,
  );
  await waitJS(cdp, `window.__inventoryStarted`, "Delayed inventory did not start");
  await evaluate(cdp, `location.hash = "#/delivery/designs"; window.__releaseInventory()`);
  await waitJS(
    cdp,
    `Features.delivery.parseRoute().section === "designs" &&
     document.querySelector("#app .del-head h2")?.textContent.trim() === "Generations"`,
    "Stale inventory response repainted the route",
  );

  await evaluate(
    cdp,
    `(() => {
      const token = Features.delivery.beginAction();
      location.hash = "#/delivery/test";
      Features.delivery.onRoute();
      Features.delivery.render(token);
    })()`,
  );
  await waitJS(
    cdp,
    `Features.delivery.parseRoute().section === "test" &&
     document.querySelector("#app .del-head h2")?.textContent.trim() === "Generations"`,
    "Stale action token repainted the route",
  );

  await evaluate(cdp, `location.hash = "#/delivery/readiness"`);
  await waitJS(
    cdp,
    `Features.delivery.parseRoute().section === "readiness" &&
     document.querySelector("#readiness-refresh-state")?.textContent === "Refreshed" &&
     !!document.querySelector("#readiness-results .del-finding-list")`,
    "Readiness route did not refresh from the server",
  );

  const globalAPI = await evaluate(
    cdp,
    `({
      delivery: ["mount","unmount","onRoute","render","reload"].every(k => typeof Features.delivery[k] === "function"),
      clusters: ["mount","unmount","onRoute","openAddWorker","fsLoad"].every(k => typeof Features.clusters[k] === "function"),
      esc: typeof AnvilUI.esc === "function",
      modal: typeof AnvilUI.modal?.open === "function",
      action: typeof AnvilUI.withAction === "function"
    })`,
  );
  assert.deepEqual(globalAPI, {
    delivery: true,
    clusters: true,
    esc: true,
    modal: true,
    action: true,
  });

  await evaluate(
    cdp,
    `(() => {
      const worker = { id: "worker-smoke", name: "Smoke worker", status: "idle",
        project_rel: "project", git_branch: "smoke" };
      const node = { id: "node-smoke", name: "Smoke node", hostname: "localhost",
        workers: [worker] };
      const fleet = { id: "smoke", name: "Smoke", online: true,
        scheduler_url: "local-fixture", nodes: [node] };
      api.v1.clusters = async () => ({ clusters: [fleet] });
      api.v1.listWorkspaces = async () => ({
        workspaces: [{ id: "ws-smoke", name: "Smoke workspace", token: "redacted" }]
      });
      api.v1.listProjects = async () => ({
        projects: [{ id: "project-smoke", key: "SMOKE", name: "Smoke project",
          summary: "Local fixture", depends_on: [] }]
      });
      api.v1.workspaceGraph = async () => ({
        projects: [{ id: "project-smoke", key: "SMOKE", name: "Smoke project" }],
        edges: [], workers: [{ worker_id: "worker-smoke", project_id: "project-smoke",
          fleet_id: "smoke", node_id: "node-smoke" }], worker_edges: []
      });
      api.v1.getProject = async () => ({
        project: { id: "project-smoke", key: "SMOKE", name: "Smoke project",
          summary: "Local fixture", depends_on: [] },
        bindings: [{ worker_id: "worker-smoke", project_id: "project-smoke" }]
      });
      api.v1.listSandbox = async () => ({
        entries: [
          { name: "docs", path: "project/docs", is_dir: true, size: 0, mtime: 0 },
          { name: "README.md", path: "project/README.md", is_dir: false, size: 10, mtime: 0 }
        ]
      });
      api.v1.deliveryNextMilestone = async () => ({ major: 0, minor: 46, patch: 1, version: "0.46" });
    })()`,
  );

  await evaluate(
    cdp,
    `location.hash = "#/delivery/requirements/projects";`,
  );
  await waitJS(cdp, `Features.delivery.el && !!document.querySelector("#del-create-modal")`, "Requirements projects did not render");
  await evaluate(cdp, `Features.delivery.openCreateWizard()`);
  await waitJS(cdp, `!!document.querySelector("[data-fleet='smoke']")`, "Delivery wizard fleet step failed");
  await evaluate(cdp, `document.querySelector("[data-fleet='smoke']").click()`);
  await waitJS(cdp, `!!document.querySelector("[data-node='node-smoke']")`, "Delivery wizard node step failed");
  await evaluate(cdp, `document.querySelector("[data-node='node-smoke']").click()`);
  await waitJS(cdp, `!!document.querySelector("[data-worker='worker-smoke']")`, "Delivery wizard worker step failed");
  await evaluate(cdp, `document.querySelector("[data-worker='worker-smoke']").click()`);
  await waitJS(cdp, `!!document.querySelector("#del-fs-list .fs-row")`, "Delivery wizard folder step failed");
  await evaluate(cdp, `document.querySelector("#del-fs-list .fs-row").click()`);
  await evaluate(cdp, `document.querySelector("#del-create-next").click()`);
  await waitJS(cdp, `Features.delivery._create?.step === "config"`, "Delivery wizard config step failed");
  await evaluate(cdp, `Features.delivery.closeCreateWizard()`);

  await evaluate(
    cdp,
    `location.hash = "#/clusters";`,
  );
  await waitJS(cdp, `document.querySelector("#c-root h2")?.textContent === "Clusters"`, "Clusters did not render");
  await evaluate(
    cdp,
    `location.hash = "#/clusters/smoke/workspaces/ws-smoke/projects/project-smoke";`,
  );
  await waitJS(cdp, `!!document.querySelector("#c-worker-add")`, "Cluster project did not render");
  await evaluate(cdp, `document.querySelector("#c-worker-add").click()`);
  await waitJS(
    cdp,
    `!document.querySelector("#c-ui-modal").hidden && !!document.querySelector("#c-fs-list .fs-row")`,
    "Worker creation/sandbox picker did not open",
  );
  await evaluate(cdp, `document.querySelector("#c-fs-list .fs-row").click()`);
  assert.equal(
    await evaluate(cdp, `document.querySelector("#c-fs-sel").textContent`),
    "project/docs",
  );
  await evaluate(cdp, `Features.clusters.closeUiModal()`);

  await evaluate(
    cdp,
    `(() => {
      api.v1.workbenchSessions = async () => ({ sessions: [] });
      api.v1.workbenchSession = async () => ({
        id: "wb-smoke", approval_supported: true, read_only: false, state: "running"
      });
      window.__approvalAnswer = null;
      api.v1.workbenchApproval = async (sessionId, requestId, body) => {
        window.__approvalAnswer = { sessionId, requestId, body };
        return { ok: true };
      };
      location.hash = "#/workbench";
    })()`,
  );
  await waitJS(cdp, `document.querySelector("#app h2")?.textContent === "Workbench"`, "Workbench did not render");
  await evaluate(
    cdp,
    `(() => {
      Features.workbench._sessionId = "wb-smoke";
      Features.workbench.renderApproval({
        request_id: "appr-smoke", session_id: "wb-smoke", tool: "write_file",
        args: { path: "README.md", content: "safe" },
        preview: { kind: "summary", summary: "Write README.md", path: "README.md" },
        decisions: ["allow", "allow_session", "deny"],
        expires_at: new Date(Date.now() + 60000).toISOString()
      });
    })()`,
  );
  await waitJS(cdp, `!!document.querySelector("[data-approval='appr-smoke']")`, "Approval card did not render");
  await evaluate(
    cdp,
    `document.querySelector("[data-approval='appr-smoke'] button").click()`,
  );
  await waitJS(cdp, `window.__approvalAnswer?.requestId === "appr-smoke"`, "Approval answer was not sent");
  assert.deepEqual(
    await evaluate(cdp, `window.__approvalAnswer`),
    {
      sessionId: "wb-smoke",
      requestId: "appr-smoke",
      body: { decision: "allow", scope: "", args: { path: "README.md", content: "safe" } },
    },
  );

  // Deliberately drop a local fake stream, replay overlapping seq values, and
  // prove the visible state machine reconnects without redrawing duplicates.
  const reconnect = await evaluate(
    cdp,
    `(async () => {
      const wb = Features.workbench;
      const states = [];
      const originalSetConn = wb.setConn.bind(wb);
      wb.setConn = (state) => { states.push(state); originalSetConn(state); };
      let calls = 0;
      wb.consume = async (_url, _signal, hooks) => {
        calls++;
        hooks.onOpen();
        if (calls === 1) {
          hooks.onEvent({ seq: 1, type: "assistant", text: "A" });
          hooks.onEvent({ seq: 2, type: "assistant", text: "B" });
          return "dropped";
        }
        hooks.onEvent({ seq: 1, type: "assistant", text: "A" });
        hooks.onEvent({ seq: 2, type: "assistant", text: "B" });
        hooks.onEvent({ seq: 3, type: "assistant", text: "C" });
        hooks.onDone({ status: "finished", result: "done" });
        return "ended";
      };
      const started = Date.now();
      await wb.attach("wb-smoke");
      return {
        calls, states, elapsed: Date.now() - started,
        text: document.querySelector("#wb-stream")?.textContent || "",
        banner: document.querySelector("#wb-conn")?.textContent || "",
      };
    })()`,
  );
  assert.equal(reconnect.calls, 2);
  assert.ok(reconnect.states.includes("reconnecting"), "dropped stream never showed reconnecting");
  assert.ok(reconnect.states.includes("ended"), "resumed stream never reached ended");
  assert.ok(reconnect.elapsed >= 350, "reconnect did not observe backoff");
  assert.match(reconnect.text, /ABC/);
  assert.doesNotMatch(reconnect.text, /ABABC/);
  assert.match(reconnect.banner, /ended · last event/);

  // Real Chrome viewport acceptance: exact breakpoint edges plus the 375px
  // operator target. These are layout/interaction checks, not CSS string tests.
  await cdp.send("Emulation.setDeviceMetricsOverride", {
    width: 375, height: 812, deviceScaleFactor: 1, mobile: true,
  });
  await evaluate(
    cdp,
    `(() => {
      window.__stoppedJob = "";
      api.v1.health = async () => ({
        summary: "Local fake healthy", active_jobs: 1,
        fleets: [{ id: "local", reachable: true, idle: 1, busy: 1 }]
      });
      api.v1.jobs = async () => ({
        jobs: [{ job_id: "job-mobile-smoke", kind: "phase", status: "running",
          prompt: "sensitive text must not be cached" }]
      });
      api.v1.deliveryReadiness = async () => ({
        rows: [{
          id: "ms-mobile", title: "Mobile readiness", lifecycle: "in_progress",
          transition: "submit_test", ok: false,
          blockers: [{ code: "gate", message: "Gate incomplete" }],
          href: "#/delivery/roadmap/m/0"
        }]
      });
      api.v1.jobCancel = async (id) => { window.__stoppedJob = id; };
      location.hash = "#/ops";
    })()`,
  );
  await waitJS(cdp, `!!document.querySelector("[data-ops-stop='job-mobile-smoke']")`, "375px Ops did not render");
  const mobileOps = await evaluate(
    cdp,
    `(() => {
      const stop = document.querySelector("[data-ops-stop='job-mobile-smoke']");
      stop.focus();
      stop.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
      stop.click();
      const r = stop.getBoundingClientRect();
      return {
        viewport: innerWidth,
        overflow: document.documentElement.scrollWidth - innerWidth,
        stopHeight: r.height,
        focused: document.activeElement === stop,
        menuVisible: getComputedStyle(document.querySelector("#nav-toggle")).display !== "none",
        cachedSecret: JSON.stringify(Features.ops._snapshot).includes("sensitive text"),
        readiness: document.querySelector("#ops-body")?.textContent.includes("Mobile readiness")
      };
    })()`,
  );
  assert.equal(mobileOps.viewport, 375);
  assert.ok(mobileOps.overflow <= 1, `Ops overflowed 375px by ${mobileOps.overflow}px`);
  assert.ok(mobileOps.stopHeight >= 44, `Stop target was ${mobileOps.stopHeight}px`);
  assert.equal(mobileOps.focused, true);
  assert.equal(mobileOps.menuVisible, true);
  assert.equal(mobileOps.cachedSecret, false);
  assert.equal(mobileOps.readiness, true);
  await waitJS(cdp, `window.__stoppedJob === "job-mobile-smoke"`, "375px Stop CTA did not cancel");

  // The cached Ops snapshot paints immediately while a refresh is pending, and
  // a failed refresh keeps it with an actionable disconnected state.
  const staleOps = await evaluate(
    cdp,
    `(async () => {
      let rejectHealth;
      api.v1.health = () => new Promise((_resolve, reject) => { rejectHealth = reject; });
      api.v1.jobs = async () => ({ jobs: [] });
      const pending = Features.ops.refresh();
      await new Promise((resolve) => setTimeout(resolve, 0));
      const during = document.querySelector(".snapshot-state")?.textContent || "";
      rejectHealth(new Error("fake offline"));
      await pending;
      return {
        during,
        after: document.querySelector(".snapshot-state")?.textContent || "",
        retained: !!document.querySelector("[data-ops-stop='job-mobile-smoke']"),
        retry: !!document.querySelector("[data-ops-retry]")
      };
    })()`,
  );
  assert.match(staleOps.during, /last successful snapshot.*refreshing/);
  assert.match(staleOps.after, /last successful snapshot.*disconnected/);
  assert.equal(staleOps.retained, true);
  assert.equal(staleOps.retry, true);

  for (const section of ["requirements", "designs", "roadmap", "test"]) {
    await evaluate(cdp, `location.hash = "#/delivery/${section}/m/0"`);
    await waitJS(cdp, `Features.delivery.parseRoute().section === "${section}"`, `375px ${section} route failed`);
    const layout = await evaluate(
      cdp,
      `({
        overflow: document.documentElement.scrollWidth - innerWidth,
        actions: document.querySelectorAll("#app button, #app a").length
      })`,
    );
    assert.ok(layout.overflow <= 1, `${section} overflowed 375px by ${layout.overflow}px`);
    assert.ok(layout.actions > 0, `${section} has no reachable CTA at 375px`);
  }
  await evaluate(cdp, `location.hash = "#/delivery/roadmap/m/0"`);
  await waitJS(cdp, `!!document.querySelector(".desktop-only")`, "wide-only Roadmap note missing");
  assert.equal(
    await evaluate(cdp, `getComputedStyle(document.querySelector(".desktop-only")).display !== "none"`),
    true,
    "desktop-only note was not visible at 375px",
  );

  const breakpoint = async (width) => {
    await cdp.send("Emulation.setDeviceMetricsOverride", {
      width, height: 812, deviceScaleFactor: 1, mobile: true,
    });
    return evaluate(
      cdp,
      `({
        width: innerWidth,
        menu: getComputedStyle(document.querySelector("#nav-toggle")).display,
        sidePosition: getComputedStyle(document.querySelector(".side")).position,
        desktopNote: getComputedStyle(document.querySelector(".desktop-only")).display,
        wide: getComputedStyle(document.querySelector(".needs-wide")).display
      })`,
    );
  };
  const at600 = await breakpoint(600);
  const after600 = await breakpoint(601);
  const at900 = await breakpoint(900);
  const after900 = await breakpoint(901);
  assert.equal(at600.menu === "none", false);
  assert.equal(at600.sidePosition, "fixed");
  assert.equal(after600.menu, "none");
  assert.notEqual(after600.sidePosition, "fixed");
  assert.notEqual(at900.desktopNote, "none");
  assert.equal(at900.wide, "none");
  assert.equal(after900.desktopNote, "none");
  assert.notEqual(after900.wide, "none");

  assert.deepEqual(errors, [], `browser errors:\n${errors.join("\n")}`);
  console.log(
    JSON.stringify(
      {
        browser: path.basename(chromiumPath()),
        delivery: deliveryResults,
        clusters: "project + worker sandbox picker",
        deliveryWizard: "fleet → node → worker → folder → config",
        workbench: "approval card → routed answer",
        reconnect: "drop → backoff → replay dedupe → resume",
        responsive: "Chrome 375px + 600/900 boundaries",
        consoleErrors: errors.length,
      },
      null,
      2,
    ),
  );
  cdp.close();
}

try {
  await main();
} finally {
  const exits = children.map(
    (child) =>
      child.exitCode == null
        ? new Promise((resolve) => child.once("exit", resolve))
        : Promise.resolve(),
  );
  for (const child of children.reverse()) {
    if (child.exitCode != null) continue;
    if (process.platform === "win32") {
      spawnSync("taskkill", ["/PID", String(child.pid), "/T", "/F"], { stdio: "ignore" });
    } else {
      child.kill();
    }
  }
  await Promise.race([
    Promise.all(exits),
    new Promise((resolve) => setTimeout(resolve, 3000)),
  ]);
  try {
    fs.rmSync(temp, { recursive: true, force: true, maxRetries: 20, retryDelay: 100 });
  } catch (error) {
    console.warn(`temporary smoke directory retained: ${temp} (${error.code})`);
  }
}
