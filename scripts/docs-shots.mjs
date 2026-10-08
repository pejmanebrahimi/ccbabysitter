// docs-shots takes the screenshots the docs show, from the demo page, so
// they never show a real session. It builds ccbabysitter, runs it with
// --demo in a temporary state folder, drives a headless Chrome through the
// DevTools protocol, and writes every shot in light and dark, at twice the
// size it is shown, into site/docs/shots. manifest.json there records each
// shot's size and a hash of the page code and the demo it was taken from;
// a test in site fails when that code changes and the shots do not.
//
// Run it from anywhere in the repository, with Go, Node 22 or later and
// Google Chrome or Chromium installed:
//
//	node scripts/docs-shots.mjs
//
// CHROME names the browser when it is not in its usual place.
import { execFileSync, spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const out = join(root, "site", "docs", "shots");

// The page width the shots are taken at: wide enough for a card's three
// columns, narrow enough that its text stays readable in the docs column.
const WIDTH = 800;
const SCALE = 2;

// Each shot finds an element on the demo page, after an optional step, and
// keeps it with a margin around it.
const SHOTS = [
  {
    name: "babysit-dialog",
    margin: 16,
    // The Babysit dialog for the demo's terminal session web-app, which has
    // Remote Control on, as in the tutorial. The page behind it and the
    // dimming over it are hidden, so the dialog stands on the page's colour.
    find: `(() => {
      const row = [...document.querySelectorAll("#sessions .row")].find((r) => r.querySelector('[data-f="name"]').textContent === "web-app");
      row.querySelector('[data-act="babysit"]').click();
      const hide = document.createElement("style");
      hide.textContent = "body > :not(dialog) { visibility: hidden; } #dlg-babysit::backdrop { background: transparent; }";
      document.head.append(hide);
      return "#dlg-babysit";
    })()`,
  },
  {
    name: "in-background-card",
    margin: 10,
    // The In background card of report-gen, whose terminal closed.
    find: `(() => {
      const card = [...document.querySelectorAll("#watches [data-key]")].find((c) => c.querySelector('[data-f="name"]').textContent === "report-gen");
      card.id = "shot-target";
      return "#shot-target";
    })()`,
  },
];

// pageHash is the hash test in site/shots_test.go computes too: SHA-256
// over each file's path and contents, line endings made LF, in path order.
export function pageHash(repo) {
  const ui = join("internal", "web", "ui");
  const files = readdirSync(join(repo, ui)).map((f) => join(ui, f).split("\\").join("/"));
  files.push("internal/web/demo.go");
  files.sort();
  const h = createHash("sha256");
  for (const f of files) {
    h.update(f + "\n");
    h.update(readFileSync(join(repo, f), "utf8").replace(/\r\n/g, "\n"));
    h.update("\n");
  }
  return h.digest("hex");
}

function chromePath() {
  if (process.env.CHROME) return process.env.CHROME;
  const known = [
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    "/Applications/Chromium.app/Contents/MacOS/Chromium",
    "/usr/bin/google-chrome",
    "/usr/bin/chromium",
    "/usr/bin/chromium-browser",
  ];
  const found = known.find((p) => existsSync(p));
  if (!found) throw new Error("no Chrome found; set CHROME to its path");
  return found;
}

// lineFrom resolves with the first line of a child's output that matches.
function lineFrom(child, stream, pattern) {
  return new Promise((resolve, reject) => {
    let seen = "";
    const timer = setTimeout(() => reject(new Error("timed out waiting for " + pattern + " in:\n" + seen)), 20000);
    child[stream].on("data", (d) => {
      seen += d;
      const m = seen.match(pattern);
      if (m) { clearTimeout(timer); resolve(m); }
    });
    child.on("exit", (code) => reject(new Error("exited with " + code + " before " + pattern + ":\n" + seen)));
  });
}

async function devtools(url) {
  const ws = new WebSocket(url);
  await new Promise((r, j) => { ws.addEventListener("open", r, { once: true }); ws.addEventListener("error", j, { once: true }); });
  let id = 0;
  const pending = new Map();
  ws.addEventListener("message", (e) => {
    const m = JSON.parse(e.data);
    if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id); }
  });
  const send = (method, params = {}) => new Promise((resolve, reject) => {
    const i = ++id;
    pending.set(i, (m) => (m.error ? reject(new Error(method + ": " + m.error.message)) : resolve(m.result)));
    ws.send(JSON.stringify({ id: i, method, params }));
  });
  return { send, close: () => ws.close() };
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function main() {
  const work = mkdtempSync(join(tmpdir(), "ccb-shots-"));
  const children = [];
  try {
    const bin = join(work, "ccbabysitter");
    execFileSync("go", ["build", "-o", bin, "./cmd/ccbabysitter"], { cwd: root, stdio: "inherit" });
    const demo = spawn(bin, ["--demo", "--no-open"], { env: { ...process.env, XDG_DATA_HOME: join(work, "data") } });
    children.push(demo);
    const [, page] = await lineFrom(demo, "stdout", /Page: (\S+)/);

    const chrome = spawn(chromePath(), ["--headless=new", "--remote-debugging-port=0", "--user-data-dir=" + join(work, "chrome"),
      "--no-first-run", "--hide-scrollbars", "--force-color-profile=srgb", "about:blank"]);
    children.push(chrome);
    const [, port] = await lineFrom(chrome, "stderr", /DevTools listening on ws:\/\/127\.0\.0\.1:(\d+)\//);
    const targets = await (await fetch("http://127.0.0.1:" + port + "/json/list")).json();
    const cdp = await devtools(targets.find((t) => t.type === "page").webSocketDebuggerUrl);

    // The page's content security policy forbids added styles, which the
    // shots use to hide what stands behind a dialog.
    await cdp.send("Page.setBypassCSP", { enabled: true });
    mkdirSync(out, { recursive: true });
    for (const f of readdirSync(out)) if (f.endsWith(".webp")) rmSync(join(out, f));
    const manifest = { page: pageHash(root), shots: {} };
    for (const shot of SHOTS) {
      for (const theme of ["light", "dark"]) {
        await cdp.send("Emulation.setDeviceMetricsOverride", { width: WIDTH, height: 1000, deviceScaleFactor: SCALE, mobile: false });
        await cdp.send("Emulation.setEmulatedMedia", { features: [{ name: "prefers-color-scheme", value: theme }, { name: "prefers-reduced-motion", value: "reduce" }] });
        await cdp.send("Page.enable");
        await cdp.send("Page.navigate", { url: page });
        await sleep(3000);
        // The page's own theme setting follows the system, so the emulated
        // light or dark decides.
        await cdp.send("Runtime.evaluate", { expression: `document.querySelector('button[data-theme-mode="auto"]').click()` });
        await sleep(800);
        const sel = (await cdp.send("Runtime.evaluate", { expression: shot.find, returnByValue: true })).result.value;
        // Wait until the element stops moving, as a dialog does once it has
        // opened, so light and dark are taken at the same place and size.
        let box = null;
        for (let tries = 0; tries < 20; tries++) {
          await sleep(400);
          const now = (await cdp.send("Runtime.evaluate", {
            expression: `(() => { const e = document.querySelector(${JSON.stringify(sel)}); e.scrollIntoView({ block: "center" }); const r = e.getBoundingClientRect(); return { x: r.x + scrollX, y: r.y + scrollY, w: r.width, h: r.height }; })()`,
            returnByValue: true,
          })).result.value;
          if (box && JSON.stringify(now) === JSON.stringify(box)) break;
          box = now;
        }
        const m = shot.margin;
        const clip = { x: Math.max(0, Math.floor(box.x - m)), y: Math.max(0, Math.floor(box.y - m)), width: Math.ceil(box.w + 2 * m), height: Math.ceil(box.h + 2 * m), scale: 1 };
        const png = await cdp.send("Page.captureScreenshot", { format: "webp", quality: 90, clip, captureBeyondViewport: true });
        writeFileSync(join(out, shot.name + "-" + theme + ".webp"), Buffer.from(png.data, "base64"));
        manifest.shots[shot.name] = { width: clip.width, height: clip.height };
        console.log("took", shot.name, theme, clip.width + "x" + clip.height);
      }
    }
    writeFileSync(join(out, "manifest.json"), JSON.stringify(manifest, null, 2) + "\n");
    cdp.close();
  } finally {
    for (const c of children) c.kill();
    await sleep(300);
    rmSync(work, { recursive: true, force: true });
  }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  // Chrome's helper processes can hold its output open after it is gone,
  // so the script ends itself rather than wait for them.
  main().then(() => process.exit(0), (e) => { console.error(e); process.exit(1); });
}
