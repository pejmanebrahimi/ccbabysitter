// docs-shots takes the screenshots the docs show, from the demo page, so
// they never show a real session. It builds ccbabysitter and runs it with
// --demo, which keeps its state in a temporary folder of its own, takes no
// lock and touches nothing real, on a port of its own. It drives a headless
// Chrome, with a temporary profile, through the DevTools protocol, and
// writes every shot in light and dark, at twice the size it is shown, into
// site/docs/shots. manifest.json there records each shot's size and a hash
// of the page code and the demo it was taken from; a test in site fails
// when that code changes and the shots do not.
//
// Run it from anywhere in the repository, with Go, Node 22 or later and
// Google Chrome, Chromium or Microsoft Edge installed:
//
//	node scripts/docs-shots.mjs
//
// CHROME names the browser when it is not in one of its usual places.
import { execFileSync, spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { copyFileSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const out = join(root, "site", "docs", "shots");

// The page width the shots are taken at: wide enough for a card's three
// columns, narrow enough that its text stays readable in the docs column.
const WIDTH = 800;
const SCALE = 2;

// Each shot finds an element on the demo page, marks it with data-shot,
// and keeps it with a margin around it. Whatever stands around it is
// hidden, so the margin shows the page's own colour.
const SHOTS = [
  {
    name: "babysit-dialog",
    margin: 16,
    ready: `[...document.querySelectorAll("#sessions .row [data-f=name]")].some((n) => n.textContent === "web-app")`,
    // The Babysit dialog for the demo's terminal session web-app, which has
    // Remote Control on, as in the tutorial. The demo starts with start at
    // login off, so its dialog offers to turn it on; a plain ccbabysitter
    // has turned it on already for the tutorial's reader, so the offer is
    // left out.
    find: `(() => {
      const row = [...document.querySelectorAll("#sessions .row")].find((r) => r.querySelector('[data-f="name"]').textContent === "web-app");
      row.querySelector('[data-act="babysit"]').click();
      const hide = document.createElement("style");
      hide.textContent = "body > :not(dialog) { visibility: hidden; } #dlg-babysit::backdrop { background: transparent; } #dlg-babysit [data-el=login] { display: none !important; }";
      document.head.append(hide);
      document.getElementById("dlg-babysit").dataset.shot = "";
      return true;
    })()`,
  },
  {
    name: "in-background-card",
    margin: 12,
    // The demo starts report-gen in the background a few seconds in.
    ready: `[...document.querySelectorAll("#watches [data-key]")].some((c) => c.querySelector("[data-f=name]").textContent === "report-gen" && c.textContent.includes("Kept alive while you were away"))`,
    // The In background card of report-gen, whose terminal closed.
    find: `(() => {
      const cards = [...document.querySelectorAll("#watches [data-key]")];
      const card = cards.find((c) => c.querySelector('[data-f="name"]').textContent === "report-gen");
      for (const c of cards) if (c !== card) c.style.visibility = "hidden";
      card.dataset.shot = "";
      return true;
    })()`,
  },
];

// pageHash is the hash the test in site/shots_test.go computes too:
// SHA-256 over each file's path and bytes, with CR LF made LF, in path
// order. It covers the page's own files and the demo; text the page shows
// that other Go packages write is not in it.
export function pageHash(repo) {
  const files = readdirSync(join(repo, "internal", "web", "ui"), { withFileTypes: true }).filter((e) => e.isFile()).map((e) => "internal/web/ui/" + e.name);
  files.push("internal/web/demo.go");
  files.sort();
  const h = createHash("sha256");
  for (const f of files) {
    h.update(f + "\n");
    h.update(readFileSync(join(repo, ...f.split("/"))).toString("latin1").replace(/\r\n/g, "\n"), "latin1");
    h.update("\n");
  }
  return h.digest("hex");
}

function chromePath() {
  if (process.env.CHROME) return process.env.CHROME;
  const win = (env, rest) => (process.env[env] ? join(process.env[env], rest) : "");
  const known = process.platform === "win32" ? [
    win("PROGRAMFILES", "Google\\Chrome\\Application\\chrome.exe"),
    win("PROGRAMFILES(X86)", "Google\\Chrome\\Application\\chrome.exe"),
    win("LOCALAPPDATA", "Google\\Chrome\\Application\\chrome.exe"),
    win("PROGRAMFILES(X86)", "Microsoft\\Edge\\Application\\msedge.exe"),
    win("PROGRAMFILES", "Microsoft\\Edge\\Application\\msedge.exe"),
  ] : [
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    "/Applications/Chromium.app/Contents/MacOS/Chromium",
    "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
    "/usr/bin/google-chrome",
    "/usr/bin/google-chrome-stable",
    "/usr/bin/chromium",
    "/usr/bin/chromium-browser",
    "/usr/bin/microsoft-edge",
  ];
  const found = known.find((p) => p && existsSync(p));
  if (!found) throw new Error("no Chrome, Chromium or Edge found; set CHROME to the browser's path");
  return found;
}

// started spawns a program and resolves with the first match of pattern in
// its output, or rejects if it cannot start, exits first or takes too long.
function started(file, args, env, stream, pattern) {
  return new Promise((resolve, reject) => {
    const child = spawn(file, args, { env, stdio: ["ignore", "pipe", "pipe"] });
    started.children.push(child);
    let seen = "";
    const fail = (why) => { clearTimeout(timer); reject(new Error(file + ": " + why + (seen ? "\n" + seen : ""))); };
    const timer = setTimeout(() => fail("no " + pattern + " within 30 seconds"), 30000);
    child.on("error", (e) => fail(e.message));
    child.on("exit", (code) => fail("exited with " + code));
    child[stream].on("data", (d) => {
      seen += d;
      const m = seen.match(pattern);
      if (m) {
        clearTimeout(timer);
        child.removeAllListeners("exit");
        resolve({ child, match: m });
      }
    });
  });
}
started.children = [];

async function devtools(url) {
  const ws = new WebSocket(url);
  await new Promise((resolve, reject) => {
    ws.addEventListener("open", resolve, { once: true });
    ws.addEventListener("error", () => reject(new Error("could not reach Chrome at " + url)), { once: true });
  });
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
  // evaluate runs an expression in the page and returns its value, and
  // throws with the page's own error when it fails or gives nothing.
  const evaluate = async (expression) => {
    const r = await send("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
    if (r.exceptionDetails) {
      throw new Error("in the page: " + (r.exceptionDetails.exception?.description || r.exceptionDetails.text) + "\nwhile running: " + expression);
    }
    if (r.result.value === undefined) throw new Error("in the page: no value from: " + expression);
    return r.result.value;
  };
  return { send, evaluate, close: () => ws.close() };
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// settled waits until the element marked data-shot stops moving and
// changing size, as a dialog does once it has opened, and returns its place
// on the page.
const MEASURE = `(() => { const e = document.querySelector("[data-shot]"); e.scrollIntoView({ block: "center" }); const r = e.getBoundingClientRect(); return { x: r.x + scrollX, y: r.y + scrollY, w: r.width, h: r.height }; })()`;
async function settled(cdp, name) {
  let last = "";
  for (let tries = 0; tries < 20; tries++) {
    await sleep(400);
    const box = await cdp.evaluate(MEASURE);
    if (JSON.stringify(box) === last) return box;
    last = JSON.stringify(box);
  }
  throw new Error(name + " kept moving for 8 seconds");
}

// until waits for an expression to be true in the page, for what the demo
// shows a few seconds after it starts.
async function until(cdp, expression, name) {
  for (let tries = 0; tries < 40; tries++) {
    if (await cdp.evaluate("!!(" + expression + ")")) return;
    await sleep(500);
  }
  throw new Error(name + ": the demo page never showed what the shot needs: " + expression);
}

async function take(cdp, shot, theme) {
  await cdp.send("Emulation.setEmulatedMedia", { features: [{ name: "prefers-color-scheme", value: theme }, { name: "prefers-reduced-motion", value: "reduce" }] });
  const box = await settled(cdp, shot.name);
  const m = shot.margin;
  const clip = { x: Math.max(0, Math.floor(box.x - m)), y: Math.max(0, Math.floor(box.y - m)), width: Math.ceil(box.w + 2 * m), height: Math.ceil(box.h + 2 * m), scale: 1 };
  const pic = await cdp.send("Page.captureScreenshot", { format: "webp", quality: 90, clip, captureBeyondViewport: true });
  writeFileSync(join(shot.dir, shot.name + "-" + theme + ".webp"), Buffer.from(pic.data, "base64"));
  console.log("took", shot.name, theme, clip.width + "x" + clip.height);
  return { width: clip.width, height: clip.height };
}

// sizePages writes each shot's size into the pictures of it on the docs
// pages, so the browser keeps their room before they load.
function sizePages(dir, shots) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) { sizePages(path, shots); continue; }
    if (!entry.name.endsWith(".html")) continue;
    const before = readFileSync(path, "utf8");
    const after = before.replace(/(src="\/docs\/shots\/([a-z-]+)-(?:light|dark)\.webp") width="\d+" height="\d+"/g, (all, src, name) =>
      shots[name] ? src + ' width="' + shots[name].width + '" height="' + shots[name].height + '"' : all);
    if (after !== before) { writeFileSync(path, after); console.log("sized the pictures in", path); }
  }
}

async function main() {
  const work = mkdtempSync(join(tmpdir(), "ccb-shots-"));
  let cdp = null;
  try {
    const bin = join(work, process.platform === "win32" ? "ccbabysitter.exe" : "ccbabysitter");
    execFileSync("go", ["build", "-o", bin, "./cmd/ccbabysitter"], { cwd: root, stdio: "inherit" });
    // Without a service manager's variables the demo serves in the
    // foreground and prints its page's address with the key.
    const env = { ...process.env };
    delete env.INVOCATION_ID;
    delete env.JOURNAL_STREAM;
    const demo = await started(bin, ["--demo", "--no-open", "--port", "0"], env, "stdout", /Page: (\S+)\r?\n/);
    const page = demo.match[1];

    const chrome = await started(chromePath(), ["--headless=new", "--remote-debugging-port=0", "--user-data-dir=" + join(work, "chrome"),
      "--no-first-run", "--hide-scrollbars", "--force-color-profile=srgb", "about:blank"], process.env, "stderr", /DevTools listening on ws:\/\/127\.0\.0\.1:(\d+)\//);
    const targets = await (await fetch("http://127.0.0.1:" + chrome.match[1] + "/json/list")).json();
    cdp = await devtools(targets.find((t) => t.type === "page").webSocketDebuggerUrl);
    // The page's content security policy forbids added styles, which the
    // shots use to hide what stands around them. This is the headless
    // tab's own setting.
    await cdp.send("Page.setBypassCSP", { enabled: true });

    // The new pictures are written aside, and replace the old ones only
    // once every shot is taken, so a failed run leaves the old ones.
    const fresh = join(work, "shots");
    mkdirSync(fresh);
    const manifest = { page: pageHash(root), shots: {} };
    for (const shot of SHOTS) {
      // One load per shot, so its light and dark pictures show the same
      // moment of the demo and only the theme changes between them.
      await cdp.send("Emulation.setDeviceMetricsOverride", { width: WIDTH, height: 1000, deviceScaleFactor: SCALE, mobile: false });
      await cdp.send("Emulation.setEmulatedMedia", { features: [{ name: "prefers-color-scheme", value: "light" }, { name: "prefers-reduced-motion", value: "reduce" }] });
      await cdp.send("Page.enable");
      await cdp.send("Page.navigate", { url: page });
      await sleep(3000);
      // The demo's theme setting starts as Dark. Auto makes the page follow
      // the emulated light or dark.
      await cdp.evaluate(`(() => { document.querySelector('button[data-theme-mode="auto"]').click(); return true; })()`);
      await until(cdp, shot.ready, shot.name);
      await cdp.evaluate(shot.find);
      shot.dir = fresh;
      const light = await take(cdp, shot, "light");
      const dark = await take(cdp, shot, "dark");
      if (light.width !== dark.width || light.height !== dark.height) {
        throw new Error(shot.name + " is " + light.width + "x" + light.height + " in light but " + dark.width + "x" + dark.height + " in dark");
      }
      manifest.shots[shot.name] = light;
    }
    mkdirSync(out, { recursive: true });
    for (const f of readdirSync(out)) if (f.endsWith(".webp")) rmSync(join(out, f));
    for (const f of readdirSync(fresh)) copyFileSync(join(fresh, f), join(out, f));
    writeFileSync(join(out, "manifest.json"), JSON.stringify(manifest, null, 2) + "\n");
    sizePages(join(root, "site", "docs"), manifest.shots);
  } finally {
    if (cdp) cdp.close();
    for (const c of started.children) c.kill();
    await sleep(500);
    try {
      rmSync(work, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 });
    } catch (e) {
      console.error("left the temporary folder " + work + ": " + e.message);
    }
  }
}

// Run only when started as a script, also through a symbolic link, and not
// when the test imports pageHash.
if (process.argv[1] && realpathSync(process.argv[1]) === realpathSync(fileURLToPath(import.meta.url))) {
  // Chrome's helper processes can hold its output open after it is gone,
  // so the script ends itself rather than wait for them.
  main().then(() => process.exit(0), (e) => { console.error(e.message || e); process.exit(1); });
}
