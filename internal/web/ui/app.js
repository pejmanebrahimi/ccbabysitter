/* The page. It reads one JSON view over an event stream and keeps the
   document in step with it, updating the fields of nodes that are already
   there instead of rebuilding lists, so a re-render never takes away a
   scroll position, an open dialog, a half-typed field or the focus ring.
   Nothing here builds markup out of values that came from the machine:
   every one of them is set as text. */

"use strict";

(function () {

  /* ---------- tiny DOM helpers ---------- */

  function $(sel, root) { return (root || document).querySelector(sel); }
  function $$(sel, root) { return Array.prototype.slice.call((root || document).querySelectorAll(sel)); }
  function f(root, name) { return root.querySelector('[data-f="' + name + '"]'); }
  function el(root, name) { return root.querySelector('[data-el="' + name + '"]'); }

  function setText(node, value) {
    if (!node) { return; }
    var next = value === null || value === undefined ? "" : String(value);
    if (node.textContent !== next) { node.textContent = next; }
  }

  function setTitle(node, value) {
    if (!node) { return; }
    var next = value || "";
    if (node.getAttribute("title") !== next) { node.setAttribute("title", next); }
  }

  function show(node, on) {
    if (node && node.hidden === !!on) { node.hidden = !on; }
  }

  function clone(id) {
    return $(id).content.firstElementChild.cloneNode(true);
  }

  /* ---------- formatting ---------- */

  var DOT = " \u00b7 ";

  function pad2(n) { return n < 10 ? "0" + n : String(n); }

  function fmtPercent(n) { return (n || 0).toFixed(1) + "%"; }

  function fmtBytes(n) {
    var mb = (n || 0) / 1048576;
    if (mb < 1024) { return (mb < 10 ? mb.toFixed(1) : String(Math.round(mb))) + " MB"; }
    return (mb / 1024).toFixed(1) + " GB";
  }

  /* fmtTokens writes a token count short: as it is under a thousand, then
     in thousands, millions or billions, with one decimal below ten of the
     unit. */
  function fmtTokens(n) {
    n = Math.max(0, Math.round(n || 0));
    if (n < 1000) { return String(n); }
    var units = ["k", "M", "B"];
    var i = 0;
    var value = n / 1000;
    while (i < units.length - 1 && Math.round(value) >= 1000) {
      i++;
      value = n / Math.pow(1000, i + 1);
    }
    return (value < 9.95 ? value.toFixed(1) : String(Math.round(value))) + units[i];
  }

  function fmtDuration(seconds) {
    var s = Math.max(0, Math.floor(seconds || 0));
    var d = Math.floor(s / 86400);
    var h = Math.floor((s % 86400) / 3600);
    var m = Math.floor((s % 3600) / 60);
    if (d > 0) { return d + "d " + h + "h"; }
    if (h > 0) { return h + "h " + m + "m"; }
    if (m > 0) { return m + "m"; }
    return s + "s";
  }

  function whenOf(iso) {
    var t = new Date(iso);
    if (isNaN(t.getTime()) || t.getFullYear() < 1980) { return null; }
    return t;
  }

  function clockOf(t, withSeconds) {
    var s = pad2(t.getHours()) + ":" + pad2(t.getMinutes());
    return withSeconds ? s + ":" + pad2(t.getSeconds()) : s;
  }

  var MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

  /* closedWhen says when an app closed the way the In background card
     puts it: the time alone today, yesterday and the time, and otherwise
     the day and the time. It is empty when the time is not known. */
  function closedWhen(iso, now) {
    var t = whenOf(iso);
    if (!t) { return ""; }
    var today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
    var yesterday = new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1);
    var at = clockOf(t, false);
    if (t >= today) { return "at " + at; }
    if (t >= yesterday) { return "yesterday at " + at; }
    return MONTHS[t.getMonth()] + " " + t.getDate() + " at " + at;
  }

  /* stayFor says how long a session has been in the background: in
     minutes under an hour, in hours under two days, in days after that. */
  function stayFor(iso, now) {
    var t = whenOf(iso);
    if (!t) { return ""; }
    var s = Math.max(0, (now.getTime() - t.getTime()) / 1000);
    if (s < 3600) { return "for " + Math.max(1, Math.floor(s / 60)) + " min"; }
    if (s < 172800) { return "for " + Math.floor(s / 3600) + " h"; }
    return "for " + Math.floor(s / 86400) + " days";
  }

  /* fmtAgo says how long ago something happened in one unit, which is
     all a list of old conversations needs. */
  function fmtAgo(iso) {
    var t = whenOf(iso);
    if (!t) { return ""; }
    var s = Math.max(0, (Date.now() - t.getTime()) / 1000);
    if (s < 60) { return "just now"; }
    if (s < 3600) { return Math.floor(s / 60) + "m ago"; }
    if (s < 86400) { return Math.floor(s / 3600) + "h ago"; }
    return Math.floor(s / 86400) + "d ago";
  }

  var HOME_PREFIX = [
    /^(\/Users\/[^/]+)(?=\/|$)/,
    /^(\/home\/[^/]+)(?=\/|$)/,
    /^([A-Za-z]:\\Users\\[^\\]+)(?=\\|$)/
  ];

  /* shortenPath writes a path the way the person who typed it would: the
     home folder as a tilde, everything else untouched. The full path is
     always kept in the title, so nothing is ever only guessed at. */
  function shortenPath(p) {
    if (!p) { return ""; }
    if (p.charAt(0) === "~") { return p; }
    for (var i = 0; i < HOME_PREFIX.length; i++) {
      var m = HOME_PREFIX[i].exec(p);
      if (m) { return "~" + p.slice(m[1].length); }
    }
    return p;
  }

  function fillPath(node, raw) {
    var full = raw || "";
    setText(node, shortenPath(full));
    setTitle(node, full);
  }

  /* ---------- names for hosts and states ---------- */

  var HOST_LABEL = {
    terminal: "Terminal", background: "Background", desktop: "Desktop",
    vscode: "VS Code", other: "Other program", none: "Not running"
  };
  var HOST_PHRASE = {
    terminal: "a terminal window", background: "the background", desktop: "the desktop app",
    vscode: "VS Code", other: "another program", none: "no app"
  };
  /* What closes, in the babysit dialog's first line. A background session
     has no app to close and gets a promise of its own. */
  var HOST_APP = {
    terminal: "the terminal", desktop: "the desktop app", vscode: "VS Code"
  };
  /* What closed, on an In background card, and the way back to it. */
  var ORIGIN_PHRASE = { desktop: "The desktop app", vscode: "VS Code", terminal: "The terminal" };
  var BACK = {
    desktop: {
      label: "Back to Desktop", go: "Back to Desktop",
      done: function (name) { return name + " is back with Desktop. Open it from the sidebar."; }
    },
    vscode: {
      label: "Back to VS Code", go: "Back to VS Code",
      done: function (name) { return name + " is back with VS Code. Open it from past conversations."; }
    },
    terminal: {
      label: "Back to a terminal", go: "End background copy",
      done: function () { return "Background copy ended. Paste the command in any terminal to carry on."; }
    }
  };
  /* Where a Not running conversation was handed back to from the
     background, in the last day. */
  var HANDED_BACK = { desktop: "Handed back to Desktop", vscode: "Handed back to VS Code", terminal: "Handed back to a terminal" };
  /* Only an address of the one shape the engine builds is ever a link. */
  var REMOTE_URL = /^https:\/\/claude\.ai\/code\/session_[A-Za-z0-9_-]+$/;
  var POSE = { watching: "calm", background: "alert", starting: "alert", stuck: "worried" };
  var STATUS_WORDS = { busy: "busy", idle: "idle" };

  function phrase(host) { return HOST_PHRASE[host] || "an app"; }

  /* hostMark shows an app as its pixel icon and, where there is room, its
     name. It rebuilds only when the app changes. */
  function hostMark(node, host, entrypoint, withLabel) {
    var label = host === "other" ? (entrypoint || HOST_LABEL.other) : (HOST_LABEL[host] || "Unknown");
    var key = (host || "none") + " " + label + " " + withLabel;
    if (node.dataset.mark !== key) {
      node.dataset.mark = key;
      var parts = [window.ccbArt.hostIcon(host)];
      if (withLabel) { parts.push(document.createTextNode(label)); }
      node.replaceChildren.apply(node, parts);
    }
    node.className = "hostmark h-" + (host || "none");
    setTitle(node, host === "other" ? "Started by " + label + ", so CC Babysitter leaves it alone" : label);
  }

  function attachCommand(short) { return "claude attach " + short; }

  /* nameTip is the tooltip on a name: the other name the session goes by,
     when the one shown is the name its app shows, and its id otherwise. */
  function nameTip(alsoCalled, id) {
    return alsoCalled ? "Also called " + alsoCalled : id;
  }

  /* backOf is the way back an In background card offers: to the app the
     session came from, or to a terminal for anything else. */
  function backOf(host) { return BACK[host] ? host : "terminal"; }

  /* chip shows an app on an In background card's trail as its pixel icon
     and its name. It rebuilds only when the app changes. */
  function chip(node, host, role) {
    var label = HOST_LABEL[host] || HOST_LABEL.other;
    if (node.dataset.mark !== host) {
      node.dataset.mark = host;
      node.replaceChildren(window.ccbArt.hostIcon(host), document.createTextNode(label));
    }
    node.className = "hop " + role + " h-" + host;
  }

  /* The same four sentences the engine gives a babysat session. A session
     that is not babysat has no watch to carry one, so the page has to say
     it itself; a test compares these four with the engine's own wording so
     the two can never drift apart. */
  function rcHintFor(host, short) {
    if (host === "terminal") { return "Type /rc in that terminal session."; }
    if (host === "desktop") { return "Switch Remote Control on in the desktop app for this session."; }
    if (host === "vscode") { return "Switch Remote Control on in the VS Code panel for this session."; }
    if (host === "background") { return "Attach with `" + attachCommand(short) + "` and type /rc."; }
    return "";
  }

  /* asClause turns one of those sentences into the end of another one. */
  function asClause(sentence) {
    var s = sentence.replace(/\.$/, "");
    return s.charAt(0).toLowerCase() + s.slice(1);
  }

  /* poseOf is the kangaroo's pose for the page as a whole: worried when a
     watch is stuck, alert while a copy carries a session or is starting,
     calm while every watch is simply watching, asleep when nothing is
     babysat. */
  function poseOf(watches) {
    var rank = { asleep: 0, calm: 1, alert: 2, worried: 3 };
    var pose = "asleep";
    for (var i = 0; i < watches.length; i++) {
      var p = POSE[watches[i].state] || "calm";
      if (rank[p] > rank[pose]) { pose = p; }
    }
    return pose;
  }

  /* ---------- page state ---------- */

  var state = {
    view: null,
    firstRender: true,
    activity: [],
    filter: "",
    search: "",
    pending: {},
    drawer: null,
    returnFocus: null,
    /* saving counts the settings changes on their way to the server, and
       saveChain sends them one after the other, in the order they were
       made. */
    saving: 0,
    saveChain: Promise.resolve(),
    dialog: { id: null }
  };

  function watchById(id) {
    var list = (state.view && state.view.watches) || [];
    for (var i = 0; i < list.length; i++) { if (list[i].sessionId === id) { return list[i]; } }
    return null;
  }

  function sessionById(id) {
    var list = (state.view && state.view.sessions) || [];
    for (var i = 0; i < list.length; i++) { if (list[i].id === id) { return list[i]; } }
    return null;
  }

  function pastById(id) {
    var list = (state.view && state.view.notRunning) || [];
    for (var i = 0; i < list.length; i++) { if (list[i].id === id) { return list[i]; } }
    return null;
  }

  /* matches is the search: name, folder or short id, ignoring case. */
  function matches(name, cwd, shortId) {
    var q = state.search;
    if (!q) { return true; }
    return [name, cwd, shortId].some(function (v) { return String(v || "").toLowerCase().indexOf(q) >= 0; });
  }

  /* ---------- talking to the server ---------- */

  function statusMessage(status) {
    if (status === 401) { return "This needs the page's key. Run ccbabysitter to open the page again, or open the address ccbabysitter status prints."; }
    if (status === 403) { return "CC Babysitter refused that request. Open this page from the address it printed when it started."; }
    if (status === 404) { return "That session is not there any more."; }
    if (status === 400) { return "CC Babysitter could not read that request."; }
    if (status === 503) { return "Too many pages are connected to CC Babysitter. Close one and try again."; }
    return "CC Babysitter could not do that. The server answered " + status + ".";
  }

  /* hasCommand reports whether a message carries a command to run, which
     the engine always writes between backticks. */
  function hasCommand(message) { return /`[^`]+`/.test(message); }

  /* send posts an action and shows what came back. An action that says
     something of its own when it works passes that as okMessage. A
     refusal that names a command to run by hand stays until it is
     dismissed, so the command can be selected and copied. */
  function send(method, url, payload, okMessage) {
    return fetch(url, {
      method: method,
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload || {})
    }).then(function (res) {
      return res.json().catch(function () { return null; }).then(function (body) {
        var message = body && body.message ? body.message : statusMessage(res.status);
        if (!res.ok || (body && body.ok === false)) {
          toast(message, true, hasCommand(message));
          return null;
        }
        toast(okMessage || message, false);
        return body;
      });
    }, function () {
      toast("CC Babysitter did not answer. It may have stopped running.", true);
      return null;
    });
  }

  /* getJSON reads the server's own sentence out of a refusal rather than
     throwing a bare status code, so a caller that wants to show the person
     what went wrong has something worth showing. */
  function getJSON(url) {
    return fetch(url).then(function (res) {
      if (res.ok) { return res.json(); }
      return res.json().catch(function () { return null; }).then(function (body) {
        throw new Error(body && body.message ? body.message : statusMessage(res.status));
      });
    });
  }

  /* ---------- busy buttons ---------- */

  function busyKey(id, act) { return id + " " + act; }

  function anyPending(id) {
    for (var key in state.pending) {
      if (state.pending[key] && key.indexOf(id + " ") === 0) { return true; }
    }
    return false;
  }

  /* run marks one button as working, so the card keeps its shape and the
     rest of its buttons stay out of the way until the answer lands. */
  function run(id, act, work) {
    var key = busyKey(id, act);
    if (state.pending[key]) { return Promise.resolve(); }
    state.pending[key] = true;
    render();
    return Promise.resolve()
      .then(work)
      .then(null, function () { toast("Something went wrong in the page while doing that.", true); })
      .then(function () {
        delete state.pending[key];
        render();
      });
  }

  function markBusy(node, id) {
    $$("button[data-act]", node).forEach(function (button) {
      var mine = !!state.pending[busyKey(id, button.dataset.act)];
      button.classList.toggle("busy", mine);
      button.disabled = mine || anyPending(id);
    });
  }

  /* ---------- toasts ---------- */

  /* A toast that stays is one the person has to act on, such as a command
     to select by hand. It goes only when it is dismissed, and when there
     are too many toasts the ones that would go anyway are dropped first. */
  function toast(message, bad, stays) {
    var list = $("#toasts");
    var node = clone("#tpl-toast");
    node.classList.toggle("err", !!bad);
    node.classList.toggle("stays", !!stays);
    setText($(".tmsg", node), message);
    var close = $(".tclose", node);
    setText(close, "\u00d7");
    close.addEventListener("click", function () { node.remove(); });
    list.append(node);
    while (list.children.length > 4) {
      (list.querySelector(".toast:not(.stays)") || list.firstElementChild).remove();
    }
    if (!stays) {
      window.setTimeout(function () { node.remove(); }, bad ? 9000 : 6000);
    }
  }

  /* copyText puts a command on the clipboard. A browser that will not let
     the page do that gets the command itself in the message, so it can
     always be selected and copied by hand, even from a row that does not
     show it. */
  function copyText(value) {
    function byHand() { toast("Copy it by hand: " + value, true, true); }
    if (!navigator.clipboard) { byHand(); return; }
    navigator.clipboard.writeText(value).then(function () {
      toast("Copied.", false);
    }, byHand);
  }

  /* ---------- keyed lists ---------- */

  /* sync updates the nodes that are already in the container, creates one
     only for a key it has never seen, and moves nodes rather than
     replacing them. Anything the user is in the middle of inside a node
     that is staying survives it. A key holds one node: an item whose key
     is already placed is skipped, and every node not used in this pass
     goes, a second one with the same key included. */
  function sync(container, items, keyOf, make, fill) {
    var known = Object.create(null);
    var kids = Array.prototype.slice.call(container.children);
    for (var i = 0; i < kids.length; i++) {
      if (!known[kids[i].dataset.key]) { known[kids[i].dataset.key] = kids[i]; }
    }

    var placed = Object.create(null);
    var used = [];
    var previous = null;
    for (var j = 0; j < items.length; j++) {
      var key = keyOf(items[j]);
      if (placed[key]) { continue; }
      placed[key] = true;
      var node = known[key];
      var fresh = !node;
      if (fresh) {
        node = make();
        node.dataset.key = key;
      }
      fill(node, items[j], fresh);
      var wanted = previous ? previous.nextSibling : container.firstChild;
      if (node !== wanted) { container.insertBefore(node, wanted); }
      previous = node;
      used.push(node);
    }
    for (var k = 0; k < kids.length; k++) {
      if (used.indexOf(kids[k]) < 0) { kids[k].remove(); }
    }
  }

  /* ---------- rendering ---------- */

  function applyView(view) {
    state.view = view;
    render();
  }

  function render() {
    var view = state.view;
    if (!view) { return; }

    renderHeader(view);
    renderInfo(view);

    var watches = (view.watches || []).filter(function (w) { return matches(w.name, w.cwd, w.shortId); });
    var sessions = (view.sessions || []).filter(function (s) { return matches(s.name, s.cwd, s.shortId); });
    var past = (view.notRunning || []).filter(function (p) { return matches(p.name, p.cwd, p.shortId); });

    renderBabysatEmpty(view, view.watches || [], watches);
    section("#running-count", "#sessions-empty", sessions.length);
    section("#past-count", "#past-empty", past.length);
    /* The section stays as the person left it, closed at first, and its
       header says when something in it was just handed back. */
    var handed = handedBackCount(past);
    show($("#past-handed"), handed > 0);
    setText($("#past-handed"), handed > 0 ? handed + " handed back" : "");

    sync($("#watches"), watches, function (w) { return w.sessionId; },
      function () { return clone("#tpl-watch"); }, fillWatch);
    sync($("#sessions"), sessions, function (s) { return s.id; },
      function () { return clone("#tpl-session"); }, fillSession);
    sync($("#past"), past, function (p) { return p.id; },
      function () { return clone("#tpl-past"); }, fillPast);
    state.firstRender = false;

    /* While a change is on its way the view may still carry the setting
       from before it, which must not undo what the person just did. */
    if (!state.saving) {
      applyTheme(view.settings ? view.settings.theme : "dark");
      if (state.drawer) { paintSettings(view); }
    }
    fillBabysitDialog();
    fillStopDialog();
    fillBackDialog();
  }

  /* section writes a count and, when a list is empty, says why: nothing
     there at all, or nothing that matches the search. */
  function section(countSel, emptySel, n) {
    setText($(countSel), n);
    var empty = $(emptySel);
    show(empty, n === 0);
    setText(empty, state.search ? "Nothing matches." : empty.dataset.none);
  }

  /* The Babysat section has no plain empty sentence: when there is nothing
     babysat at all, it shows the hero instead. A search that matches
     nothing out of an otherwise non-empty list still just says so. */
  function renderBabysatEmpty(view, all, watches) {
    setText($("#babysat-count"), watches.length);
    var none = all.length === 0;
    show($("#watches-empty"), !none && watches.length === 0);
    setText($("#watches-empty"), "Nothing matches.");
    show($("#watches-hero"), none);
    if (none) { fillBabysatHero(view); }
  }

  /* The command that starts a background session on a server, with Remote
     Control on from the start. */
  var START_CMD = "claude --bg --remote-control";

  /* canBabysit is the rule a Running row follows for showing its buttons,
     Babysit among them: every session but one another program owns. */
  function canBabysit(s) { return !!s.actionable; }

  /* heroSteps is what the empty hero asks the person to do next, which
     follows what is running: press Babysit when there is something to
     press it on, and otherwise start a session first, on a server with
     the command to do it. A word to be shown bold is written as {b: word}. */
  function heroSteps(view) {
    view = view || {};
    var headless = !!(view.env || {}).headless;
    if ((view.sessions || []).some(canBabysit)) {
      return { next: ["Press ", { b: "Babysit" }, " on a session below to start."], command: false, after: [] };
    }
    if (!headless) {
      return { next: ["Start a Claude Code session, then press ", { b: "Babysit" }, " on it here."], command: false, after: [] };
    }
    var auto = !!(view.settings || {}).autoBabysit;
    return {
      next: ["Start a background session in a project folder:"],
      command: true,
      after: auto ? ["It is babysat as soon as it starts."] : ["Then press ", { b: "Babysit" }, " on it here."]
    };
  }

  /* fillParts writes a sentence of text and bold words into a node, and
     leaves the node alone when the sentence has not changed. */
  function fillParts(node, parts) {
    var key = JSON.stringify(parts);
    if (node.dataset.parts === key) { return; }
    node.dataset.parts = key;
    node.replaceChildren.apply(node, parts.map(function (part) {
      return typeof part === "string" ? document.createTextNode(part) : boldWord(part.b);
    }));
  }

  /* fillBabysatHero writes the mascot, the promise and the next step for
     the empty Babysat hero, none of it as markup: every part is text or an
     element built by hand. */
  function fillBabysatHero(view) {
    var hero = $("#watches-hero");
    var slot = el(hero, "mascot");
    if (!slot.firstChild) { slot.append(window.ccbArt.mascot("asleep", "")); }

    var headless = !!(view.env || {}).headless;
    setText(f(hero, "promise"), headless
      ? "A babysat session stays on your remote list. If it stops or the server reboots, it comes back in the background with Remote Control on."
      : "A babysat session comes back on its own. If its app crashes or closes, it carries on in the background, reachable from your other devices.");

    var steps = heroSteps(view);
    fillParts(f(hero, "next"), steps.next);
    show(el(hero, "startbox"), steps.command);
    setText(f(hero, "startcmd"), START_CMD);
    show(f(hero, "after"), steps.after.length > 0);
    fillParts(f(hero, "after"), steps.after);
  }

  /* boldWord returns a new <b> element holding the given text, for a
     sentence built by hand out of elements rather than markup. */
  function boldWord(text) {
    var b = document.createElement("b");
    b.textContent = text;
    return b;
  }

  function renderHeader(view) {
    var logo = $("#logo");
    if (!logo.firstChild) { logo.append(window.ccbArt.mascot("asleep", "CC Babysitter")); }
    window.ccbArt.setPose(logo.firstChild, poseOf(view.watches || []));

    /* The computer is kept awake while a session is babysat, which is a
       rule rather than a setting, so the header reports it. */
    var keep = view.keepAwake || {};
    var on = !!keep.held;
    $("#awake-held").classList.toggle("on", on);
    $("#awake").classList.toggle("on", on);
    setText($("#awake-text"), on ? "Computer awake: " + (keep.activeWatches || 0) + " babysat" : "Computer not kept awake");
    setTitle($("#awake"), keep.supported === false ? "This system cannot hold the keep-awake request" : "");
  }

  /* versionsLine lists what is installed: the Claude Code CLI always, with
     its version, and said to be missing when it is not there, then Desktop
     and the VS Code extension only when they are found. */
  function versionsLine(env) {
    env = env || {};
    var parts = ["Claude Code " + (env.cliVersion || (env.cliFound ? "found" : "not found"))];
    if (env.desktopInstalled) {
      parts.push("Desktop " + (env.desktopVersion || "installed") + (env.desktopRunning ? " running" : ""));
    }
    if (env.vscodeExtVersion) {
      parts.push("VS Code extension " + env.vscodeExtVersion);
    }
    return parts;
  }

  /* renderInfo is the one line under the header: versions, then totals.
     Under it, a second line says when the CLI is not logged in, which
     nothing else on the page could explain. */
  function renderInfo(view) {
    var env = view.env || {};
    var totals = view.totals || {};
    var parts = versionsLine(env).concat([
      (totals.sessions || 0) + " running",
      (totals.remoteControlled || 0) + " with Remote Control",
      (totals.babysat || 0) + " babysat",
      "CPU " + fmtPercent(totals.cpuPercent),
      fmtBytes(totals.rssBytes),
      "v" + (view.version || "")
    ]);
    setText($("#info"), parts.join(DOT));
    show($("#login-note"), env.cliLoggedIn === "no");
  }

  function fillStats(node, stats, tree) {
    stats = stats || {};
    tree = tree || {};
    setText(f(node, "cpu"), fmtPercent(tree.cpuPercent));
    setText(f(node, "mem"), fmtBytes(tree.rssBytes));
    setText(f(node, "uptime"), fmtDuration(tree.uptimeSeconds));
    setText(f(node, "turns"), stats.turns || 0);
    setText(f(node, "tokensIn"), fmtTokens(stats.inputTokens));
    setText(f(node, "tokensOut"), fmtTokens(stats.outputTokens));
    setText(f(node, "cache"), fmtTokens((stats.cacheReadTokens || 0) + (stats.cacheWriteTokens || 0)));
    setTitle(f(node, "cache"), "read " + fmtTokens(stats.cacheReadTokens) + DOT + "write " + fmtTokens(stats.cacheWriteTokens));
    /* A session that has not answered yet has no model, and then the
       stat is left out rather than shown empty. */
    setText(f(node, "model"), stats.model || "");
    show(f(node, "model").parentNode, !!stats.model);
  }

  /* stateLines is what a card says about where its session is, in words. */
  function stateLines(w) {
    if (w.state === "starting") {
      /* Just after start, apps that restore their own sessions go first,
         and the card says so rather than looking stuck. */
      if (state.view && state.view.graceUntil) { return ["Waiting for apps to restore their sessions first"]; }
      return ["Starting in the background"];
    }
    if (w.state === "stuck") { return ["Stuck: " + (w.pauseReason || "three starts failed in five minutes")]; }
    return ["Watching in " + phrase(w.host || w.originHost)];
  }

  function fillWatch(node, w, fresh) {
    ["watching", "background", "starting", "stuck"].forEach(function (s) {
      node.classList.toggle("s-" + s, w.state === s);
    });
    /* An In background card tells what happened while the person was
       away, in place of where the session is, and stacks its ways on. */
    var rescued = w.state === "background";
    node.classList.toggle("rescued", rescued);

    var slot = el(node, "mascot");
    var pose = POSE[w.state] || "calm";
    if (!slot.firstChild) { slot.append(window.ccbArt.mascot(pose, "")); }
    window.ccbArt.setPose(slot.firstChild, pose);
    /* A watch that starts while the page is open plays the hop once; the
       ones already there when the page loads do not. */
    if (fresh && !state.firstRender) { window.ccbArt.hop(slot.firstChild); }

    hostMark(el(node, "host"), w.host || w.originHost, "", true);
    show(el(node, "host"), !rescued);
    setText(f(node, "name"), w.name || w.shortId);
    setTitle(f(node, "name"), nameTip(w.alsoCalled, w.sessionId));
    /* Remote Control is a fact about a running session, so it is only
       shown while the session runs somewhere. */
    var live = w.live || [];
    var rc = f(node, "rc");
    show(rc, live.length > 0);
    setText(rc, w.remoteControl ? "Remote Control on" : "Remote Control off");
    rc.classList.toggle("on", !!w.remoteControl);
    var multi = f(node, "multi");
    show(multi, live.length > 1);
    if (live.length > 1) { setText(multi, "open in " + live.length + " apps"); }
    fillPath(f(node, "cwd"), w.cwd);

    var lines = rescued ? [] : stateLines(w);
    show(f(node, "state"), lines.length > 0);
    setText(f(node, "state"), lines[0] || "");
    setTitle(f(node, "state"), w.state === "stuck" ? lines[0] : "");
    show(f(node, "state2"), lines.length > 1);
    setText(f(node, "state2"), lines[1] || "");
    fillRescue(node, w, rescued);

    show(f(node, "warning"), !!w.fallbackWarning);
    setText(f(node, "warning"), w.fallbackWarning || "");
    var hint = w.state === "watching" && !w.remoteControl ? (w.rcHint || rcHintFor(w.host, w.shortId)) : "";
    show(f(node, "rchint"), !!hint);
    setText(f(node, "rchint"), hint);

    show(el(node, "attachbox"), !!w.attachCmd && !rescued);
    setText(f(node, "attach"), w.attachCmd || "");
    show(el(node, "sshbox"), !!w.sshAttachCmd);
    setText(f(node, "sshattach"), w.sshAttachCmd || "");

    fillStats(node, w.stats, w.tree);
    fillWaysOn(node, w, rescued);

    show(node.querySelector('[data-act="unbabysit"]'), !!w.canUnbabysit && w.state !== "stuck");
    show(node.querySelector('[data-act="stop"]'), !!w.canStop && !rescued);
    show(node.querySelector('[data-act="resume"]'), w.state === "stuck");
    show(node.querySelector('[data-act="forget"]'), !!w.canUnbabysit && w.state === "stuck");
    markBusy(node, w.sessionId);
  }

  /* fillRescue writes the panel an In background card shows: what closed
     and when, that the session kept going, and how long it has been in the
     background. The page renders on every view the stream brings, which
     keeps the time current without a timer of its own. */
  function fillRescue(node, w, rescued) {
    show(el(node, "rescue"), rescued);
    if (!rescued) { return; }
    var now = new Date();
    var when = closedWhen(w.backgroundSince, now);
    setText(f(node, "rescue"), (ORIGIN_PHRASE[w.originHost] || "Its app") + " closed" + (when ? " " + when : "") + ". " +
      (w.name || w.shortId) + (w.remoteControl ? " kept running and stayed reachable from your other devices." : " kept running in the background."));
    chip(el(node, "from"), w.originHost, "from");
    chip(el(node, "to"), "background", "to");
    var since = stayFor(w.backgroundSince, now);
    show(f(node, "since"), !!since);
    setText(f(node, "since"), since);
  }

  /* fillWaysOn fills the stacked actions of an In background card: back to
     the app it came from, and the two that leave it running as it is,
     each shown only where it can work. */
  function fillWaysOn(node, w, rescued) {
    var view = state.view || {};
    var env = view.env || {};
    el(node, "acts").classList.toggle("stack", rescued);
    var back = node.querySelector('[data-act="back"]');
    show(back, rescued && !!w.canStop);
    setText(back, BACK[backOf(w.originHost)].label);

    var remote = el(node, "remote");
    var url = rescued && REMOTE_URL.test(w.remoteUrl || "") ? w.remoteUrl : "";
    show(remote, !!url);
    if (url && remote.getAttribute("href") !== url) { remote.setAttribute("href", url); }
    if (!url && remote.hasAttribute("href")) { remote.removeAttribute("href"); }

    /* The engine names what really opens here, and names nothing where
       nothing can be opened. */
    var terminal = !!w.attachCmd && rescued && !env.headless && !!view.terminalLabel;
    show(el(node, "terminal"), terminal);
    setText(node.querySelector('[data-act="open-terminal"]'), view.terminalLabel || "");
    setTitle(node.querySelector('.split [data-act="copy-attach"]'), w.attachCmd ? "Copy the command: " + w.attachCmd : "");

    var hint = f(node, "actshint");
    show(hint, !!url || terminal);
    setText(hint, url && terminal ? "The last two keep it running as it is." : "It keeps running as it is.");
  }

  function fillSession(node, s) {
    hostMark(el(node, "host"), s.host, s.entrypoint, true);
    setText(f(node, "name"), s.name || s.shortId);
    setTitle(f(node, "name"), nameTip(s.alsoCalled, s.id));
    var live = s.live || [];
    var multi = f(node, "multi");
    show(multi, live.length > 1);
    setText(multi, live.length > 1 ? "open in " + live.length + " apps" : "");
    fillPath(f(node, "cwd"), s.cwd);
    var dot = f(node, "rcdot");
    dot.classList.toggle("on", !!s.remoteControl);
    setTitle(dot, s.remoteControl ? "Remote Control on" : "Remote Control off");
    setText(f(node, "status"), STATUS_WORDS[String(s.status || "").toLowerCase()] || "");
    show(el(node, "acts"), canBabysit(s));
    show(node.querySelector('[data-act="stop"]'), !!s.canStop);
    var attach = node.querySelector('[data-act="copy-attach"]');
    show(attach, !!s.attachCmd);
    setTitle(attach, s.attachCmd || "");
    var ssh = node.querySelector('[data-act="copy-ssh"]');
    show(ssh, !!s.sshAttachCmd);
    setTitle(ssh, s.sshAttachCmd || "");
    markBusy(node, s.id);
  }

  /* handedBackCount counts the conversations handed back to their app. */
  function handedBackCount(list) {
    var n = 0;
    for (var i = 0; i < list.length; i++) {
      if (HANDED_BACK[list[i].handedBackTo]) { n++; }
    }
    return n;
  }

  function fillPast(node, p) {
    setText(f(node, "name"), p.name || p.shortId);
    setTitle(f(node, "name"), p.id);
    var handed = HANDED_BACK[p.handedBackTo] || "";
    show(f(node, "handed"), !!handed);
    setText(f(node, "handed"), handed);
    fillPath(f(node, "cwd"), p.cwd);
    node.dataset.when = p.lastActivity || "";
    setText(f(node, "when"), fmtAgo(p.lastActivity));
    var cmd = pastCommand(p);
    setText(f(node, "cmd"), cmd);
    setTitle(f(node, "cmd"), cmd);
    show(el(node, "sshbox"), !!p.sshAttachCmd);
    setText(f(node, "sshattach"), p.sshAttachCmd || "");
    setTitle(f(node, "sshattach"), p.sshAttachCmd || "");
  }

  /* pastCommand is the command a Not running row offers: the attach
     command of a background session CC Babysitter stopped, which starts it
     again as it was, and the resume command for any other. */
  function pastCommand(p) { return p.attachCmd || p.resumeCmd || ""; }

  function tickTimes() {
    $$("#past [data-when]").forEach(function (node) { setText(f(node, "when"), fmtAgo(node.dataset.when)); });
    window.setTimeout(tickTimes, 30000);
  }

  /* ---------- theme ---------- */

  /* theme.js turns the setting into the palette that is painted, and
     remembers it for the next load. */
  function applyTheme(theme) {
    window.ccbTheme.apply(theme);
  }

  /* ---------- dialogs ---------- */

  /* The cancel button is focused by hand rather than left to autofocus,
     which a browser honours only the first time a dialog is opened. The
     way out of a dialog has to be the thing under the fingers every
     single time, not just once. */
  function openDialog(dialog) {
    if (!dialog.open) { dialog.showModal(); }
    var cancel = dialog.querySelector('[data-act="cancel"]');
    if (cancel) { cancel.focus(); }
  }

  function closeDialog(dialog) {
    if (dialog.open) { dialog.close(); }
  }

  function openBabysit(id) {
    state.dialog.id = id;
    $("#babysit-login").checked = false;
    if (fillBabysitDialog()) { openDialog($("#dlg-babysit")); }
  }

  /* offerStartAtLogin says whether the babysit dialog offers to switch
     start at login on: only while it is off, where it is supported, and
     never on a server, where the systemd unit does that job. */
  function offerStartAtLogin() {
    var view = state.view || {};
    var env = view.env || {};
    var settings = view.settings || {};
    return !env.headless && !settings.autostart && view.autostartSupported !== false;
  }

  /* babysitPromise is the dialog's first line. A server is not kept awake
     by babysitting, and what closes there is the terminal or the SSH
     connection it runs over. A background session has no app to close and
     gets a promise of its own. */
  function babysitPromise(s, headless) {
    var promise;
    if (s.host === "background") {
      promise = "If this session stops, it is started again in the background with Remote Control.";
    } else if (headless) {
      promise = "If the terminal or your SSH connection closes, this session continues in the background with Remote Control.";
    } else {
      promise = "If " + (HOST_APP[s.host] || "its app") + " closes, this session continues in the background with Remote Control.";
    }
    return headless ? promise : "Keeps the computer awake. " + promise;
  }

  /* rcMissing says what Remote Control being off means right now, and what
     to do about it, in the words of the app the session is in. */
  function rcMissing(s) {
    if (s.remoteControl) { return ""; }
    return "Remote Control is off, so your other devices can't reach it yet: " + asClause(rcHintFor(s.host, s.shortId)) + ".";
  }

  /* The babysit dialog is at most three lines: the promise, what to do
     about Remote Control when it is off, and why a background copy would
     not start here when that is known. */
  function fillBabysitDialog() {
    var dialog = $("#dlg-babysit");
    var s = sessionById(state.dialog.id);
    if (!s) {
      if (dialog.open) { closeDialog(dialog); }
      return false;
    }
    var env = (state.view && state.view.env) || {};
    setText(f(dialog, "name"), s.name || s.shortId);
    setText(f(dialog, "promise"), babysitPromise(s, !!env.headless));
    var rcLine = rcMissing(s);
    show(f(dialog, "rc"), !!rcLine);
    setText(f(dialog, "rc"), rcLine);
    show(f(dialog, "fallback"), !!s.fallbackWarning);
    setText(f(dialog, "fallback"), s.fallbackWarning || "");
    /* Under the lines, on a machine with a display, what makes the promise
       survive a restart. A server runs as a service already. */
    show(el(dialog, "login"), offerStartAtLogin());
    return true;
  }

  function doBabysit() {
    var id = state.dialog.id;
    if (!sessionById(id)) { return; }
    var startAtLogin = offerStartAtLogin() && $("#babysit-login").checked;
    closeDialog($("#dlg-babysit"));
    run(id, "babysit", function () {
      return send("POST", "/api/sessions/" + id + "/babysit", { startAtLogin: startAtLogin });
    });
  }

  function openStop(id) {
    state.dialog.id = id;
    if (fillStopDialog()) { openDialog($("#dlg-stop")); }
  }

  function fillStopDialog() {
    var dialog = $("#dlg-stop");
    var subject = watchById(state.dialog.id) || sessionById(state.dialog.id);
    if (!subject || !subject.canStop) {
      if (dialog.open) { closeDialog(dialog); }
      return false;
    }
    setText(f(dialog, "name"), subject.name || subject.shortId);
    return true;
  }

  function doStop() {
    var id = state.dialog.id;
    closeDialog($("#dlg-stop"));
    run(id, "stop", function () {
      return send("POST", "/api/sessions/" + id + "/stop", {});
    });
  }

  /* doQuit says how to start CC Babysitter again from the moment the
     person confirms, not only once the answer arrives: the answer can be
     cut off by the shutdown it starts, and the pill must not fall back to
     Reconnecting or Not running meanwhile. */
  function doQuit() {
    closeDialog($("#dlg-quit"));
    closeDrawer();
    quitHere = true;
    show($("#reconnect"), true);
    reconnectSays(QUIT_SAYS, false);
    send("POST", "/api/quit", {});
  }

  function openBack(id) {
    state.dialog.id = id;
    if (fillBackDialog()) { openDialog($("#dlg-back")); }
  }

  /* The back dialog comes in one of three versions, for the app the
     session came from, and closes itself once the card it was opened from
     is no longer In background. */
  function fillBackDialog() {
    var dialog = $("#dlg-back");
    var w = watchById(state.dialog.id);
    if (!w || w.state !== "background" || !w.canStop) {
      if (dialog.open) { closeDialog(dialog); }
      return false;
    }
    var kind = backOf(w.originHost);
    setText(f(dialog, "title"), BACK[kind].label);
    ["desktop", "vscode", "terminal"].forEach(function (k) { show(el(dialog, "back-" + k), k === kind); });
    $$('[data-f="name"]', dialog).forEach(function (node) { setText(node, w.name || w.shortId); });
    setText(f(dialog, "resume"), w.resumeCmd || "");
    setText($('[data-act="go"]', dialog), BACK[kind].go);
    return true;
  }

  /* doBack ends the background copy the way Stop does, and says what to
     do next in the words of the app the session goes back to. */
  function doBack() {
    var id = state.dialog.id;
    var w = watchById(id);
    closeDialog($("#dlg-back"));
    if (!w) { return; }
    var done = BACK[backOf(w.originHost)].done(w.name || w.shortId);
    run(id, "back", function () {
      return send("POST", "/api/sessions/" + id + "/stop", {}, done);
    });
  }

  /* ---------- activity panel ---------- */

  /* The panel sits beside the sessions on a wide screen and along the
     bottom on a narrow one, and never on top of them. Whether it is open
     is remembered in this browser only. */
  function setActivity(open) {
    show($("#activity"), open);
    $("#layout").classList.toggle("with-activity", open);
    $("#toggle-activity").setAttribute("aria-pressed", open ? "true" : "false");
    try { window.localStorage.setItem("ccb.activity", open ? "open" : "closed"); } catch (err) { /* storage is optional */ }
  }

  function activityWasOpen() {
    try { return window.localStorage.getItem("ccb.activity") === "open"; } catch (err) { return false; }
  }

  function activityMatches(entry) {
    return !state.filter || entry.session === state.filter;
  }

  function makeActivityNode(entry) {
    var node = clone("#tpl-activity");
    var when = whenOf(entry.time);
    var time = $(".t", node);
    setText(time, when ? clockOf(when, true) : "");
    if (when) { setTitle(time, when.toString()); }
    var session = $(".sess", node);
    setText(session, entry.session || "");
    show(session, !!entry.session);
    show($(".chip", node), !!entry.automatic);
    var message = $(".msg", node);
    setText(message, entry.message || "");
    message.classList.toggle("err", entry.level === "ERROR");
    var why = $(".why", node);
    setText(why, entry.reason ? "Reason: " + entry.reason : "");
    show(why, !!entry.reason);
    return node;
  }

  function renderActivity() {
    var list = $("#activity-list");
    var body = list.parentElement;
    var top = body.scrollTop;
    list.replaceChildren();
    var shown = 0;
    for (var i = 0; i < state.activity.length; i++) {
      if (activityMatches(state.activity[i])) {
        list.append(makeActivityNode(state.activity[i]));
        shown++;
      }
    }
    show($("#activity-empty"), shown === 0);
    body.scrollTop = top;
    renderActivityFilter();
  }

  function renderActivityFilter() {
    var select = $("#activity-filter");
    var names = [];
    var seen = {};
    for (var i = 0; i < state.activity.length; i++) {
      var name = state.activity[i].session;
      if (name && !seen[name]) { seen[name] = true; names.push(name); }
    }
    names.sort();
    if (select.dataset.names === names.join("\n")) { return; }
    select.dataset.names = names.join("\n");
    var current = state.filter;
    select.replaceChildren();
    var all = document.createElement("option");
    all.value = "";
    all.textContent = "All sessions";
    select.append(all);
    names.forEach(function (name) {
      var option = document.createElement("option");
      option.value = name;
      option.textContent = name;
      select.append(option);
    });
    select.value = names.indexOf(current) >= 0 ? current : "";
    state.filter = select.value;
  }

  /* activityKey identifies one entry well enough to spot the same one
     arriving twice: once down the stream and once in the history the page
     asks for when it starts. The parts are joined with a newline, which
     none of them can contain on its own. */
  function activityKey(entry) {
    return [entry.time || "", entry.session || "", entry.message || ""].join("\n");
  }

  /* mergeActivity folds the history into whatever the stream has already
     delivered. The stream is connected first, so an entry written while
     the history was being fetched arrives on both paths; dropping the
     duplicate is what keeps it from being shown twice, and merging rather
     than replacing is what keeps it from being lost. */
  function mergeActivity(entries) {
    var seen = {};
    var merged = [];
    function take(list) {
      for (var i = 0; i < list.length; i++) {
        var key = activityKey(list[i]);
        if (seen[key]) { continue; }
        seen[key] = true;
        merged.push(list[i]);
      }
    }
    take(state.activity);
    take(entries || []);
    merged.sort(function (a, b) {
      return (Date.parse(b.time) || 0) - (Date.parse(a.time) || 0);
    });
    state.activity = merged.slice(0, 400);
    renderActivity();
  }

  function addActivity(entry) {
    state.activity.unshift(entry);
    while (state.activity.length > 400) { state.activity.pop(); }
    if (!activityMatches(entry)) { renderActivityFilter(); return; }
    var list = $("#activity-list");
    list.insertBefore(makeActivityNode(entry), list.firstChild);
    while (list.children.length > 400) { list.lastElementChild.remove(); }
    show($("#activity-empty"), false);
    renderActivityFilter();
  }

  /* ---------- settings drawer ---------- */

  function openDrawer() {
    if (state.drawer) { return; }
    state.returnFocus = document.activeElement;
    state.drawer = "settings";
    $("#scrim").hidden = false;
    var node = $("#drawer-settings");
    node.hidden = false;
    var first = node.querySelector("button, select, input");
    if (first) { first.focus(); }
  }

  function closeDrawer() {
    if (!state.drawer) { return; }
    $("#drawer-settings").hidden = true;
    state.drawer = null;
    $("#scrim").hidden = true;
    if (state.returnFocus && state.returnFocus.focus) { state.returnFocus.focus(); }
  }

  function pressSegment(group, attribute, value) {
    var buttons = $$("button", group);
    for (var i = 0; i < buttons.length; i++) {
      var on = buttons[i].dataset[attribute] === value;
      if ((buttons[i].getAttribute("aria-pressed") === "true") !== on) {
        buttons[i].setAttribute("aria-pressed", on ? "true" : "false");
      }
    }
  }

  /* themeOf is the theme a setting names, as the buttons name it. */
  function themeOf(settings) {
    var theme = (settings || {}).theme;
    return theme === "light" || theme === "auto" ? theme : "dark";
  }

  /* paintSettings shows the saved settings. A server has no login of its
     own to start at and no browser to open, so it is offered babysitting
     every new background session instead, which only a server does. */
  function paintSettings(view) {
    var settings = view.settings || {};
    var headless = !!(view.env || {}).headless;
    show($("#set-desktop"), !headless);
    show($("#set-server"), headless);
    $("#set-autostart").checked = !!settings.autostart;
    $("#set-open-browser").checked = !!settings.autoOpenBrowser;
    $("#set-auto-babysit").checked = !!settings.autoBabysit;
    var supported = view.autostartSupported !== false;
    $("#set-autostart").disabled = !supported;
    show($("#set-autostart-note"), !supported);
    pressSegment($("#set-theme-seg"), "themeMode", themeOf(settings));
  }

  function settingsError(message) {
    setText($("#set-error"), message);
    show($("#set-error"), !!message);
  }

  function openSettings() {
    settingsError("");
    paintSettings(state.view || {});
    openDrawer();
  }

  /* putSetting sends one change and answers with what went wrong, or with
     nothing when it was saved. */
  function putSetting(change) {
    return fetch("/api/settings", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(change)
    }).then(function (res) {
      return res.json().catch(function () { return null; }).then(function (body) {
        if (!res.ok || !body || body.ok === false) { return body && body.message ? body.message : statusMessage(res.status); }
        return "";
      });
    }, function () {
      return "CC Babysitter did not answer. It may have stopped running.";
    });
  }

  /* saveSetting saves one setting the moment it is changed. When the
     server says no, the control goes back to what it was and the reason
     is shown in Settings, where the change was made. */
  function saveSetting(name, value, undo) {
    var change = {};
    change[name] = value;
    state.saving++;
    settingsError("");
    state.saveChain = state.saveChain.then(function () {
      return putSetting(change);
    }).then(function (problem) {
      if (problem) {
        undo();
        settingsError(problem);
      } else if (state.view && state.view.settings) {
        state.view.settings[name] = value;
      }
    }).then(null, function () {
      settingsError("Something went wrong in the page while doing that.");
    }).then(function () {
      state.saving--;
      render();
    });
  }

  function onSettingBox(selector, name) {
    $(selector).addEventListener("change", function (event) {
      var box = event.currentTarget;
      var value = box.checked;
      saveSetting(name, value, function () { box.checked = !value; });
    });
  }

  /* A theme is painted the moment it is pressed, and put back with its
     button if it could not be saved. */
  function onThemeButton(event) {
    var button = event.target.closest("button[data-theme-mode]");
    if (!button) { return; }
    var group = $("#set-theme-seg");
    var pressed = group.querySelector('[aria-pressed="true"]');
    var before = pressed ? pressed.dataset.themeMode : themeOf((state.view || {}).settings);
    var next = button.dataset.themeMode;
    if (next === before) { return; }
    pressSegment(group, "themeMode", next);
    applyTheme(next);
    saveSetting("theme", next, function () {
      pressSegment(group, "themeMode", before);
      applyTheme(before);
    });
  }

  /* ---------- the event stream ---------- */

  var stream = null;
  var backoff = 1000;
  /* wasDropped is set when the stream goes down, so that coming back up is
     told apart from connecting for the first time. */
  var wasDropped = false;
  /* A page that says "Reconnecting" forever will not tell you the one thing
     you need to know, which is that the program is not running any more. A
     few attempts are ordinary; five are not. */
  var attempts = 0;
  var PATIENCE = 5;
  /* keyRefused is set while the server answers but does not take this
     page's key, which no number of attempts will change. */
  var keyRefused = false;
  /* quitHere is set once this page has asked CC Babysitter to quit, so the
     pill says how to start it again instead of counting attempts. It is
     cleared when the stream comes back, which is CC Babysitter running
     again. */
  var quitHere = false;
  var QUIT_SAYS = "CC Babysitter has quit. Run ccbabysitter to start it again.";

  function reconnectSays(text, dots) {
    setText($("#reconnect-text"), text);
    show($("#reconnect-dots"), dots);
  }

  /* keyRefusal is what the page says when asking for the state was
     answered with status and body: the server's own words when it is
     running but does not take this page's key, and "" for any other
     answer. */
  function keyRefusal(status, body) {
    if (status !== 401) { return ""; }
    return body && body.message ? body.message : statusMessage(401);
  }

  /* askWhyDown asks for the state while the stream is down. A server that
     answers 401 is running, so the pill says the page needs its key rather
     than calling it not running. A server that does not answer at all is
     left to the count of attempts. */
  function askWhyDown() {
    fetch("/api/state").then(function (res) {
      if (res.status !== 401) { return ""; }
      return res.json().then(null, function () { return null; }).then(function (body) {
        return keyRefusal(401, body);
      });
    }).then(function (text) {
      if (!wasDropped) { return; }
      keyRefused = !!text;
      if (text) { reconnectSays(text, false); }
    }, function () { /* not answering is what the attempts count */ });
  }

  function connect() {
    stream = new EventSource("/api/events");
    stream.addEventListener("open", function () {
      show($("#reconnect"), false);
      backoff = 1000;
      attempts = 0;
      keyRefused = false;
      quitHere = false;
      reconnectSays("Reconnecting", true);
      if (wasDropped) {
        wasDropped = false;
        /* Anything written while the stream was down never reached this
           page, and the stream only carries what happens from now on. */
        loadActivity();
      }
    });
    stream.addEventListener("view", function (event) {
      /* A copy that is quitting sends one last view on its way out, which
         must not hide the sentence saying it has quit. */
      if (!quitHere) { show($("#reconnect"), false); }
      backoff = 1000;
      try { applyView(JSON.parse(event.data)); } catch (err) { /* a truncated frame is dropped */ }
    });
    stream.addEventListener("activity", function (event) {
      try { addActivity(JSON.parse(event.data)); } catch (err) { /* same */ }
    });
    stream.addEventListener("error", function () {
      if (stream) { stream.close(); stream = null; }
      wasDropped = true;
      attempts++;
      show($("#reconnect"), true);
      if (quitHere) {
        reconnectSays(QUIT_SAYS, false);
      } else if (attempts >= PATIENCE && !keyRefused) {
        reconnectSays("Not running. Start CC Babysitter to reconnect.", false);
      }
      askWhyDown();
      var wait = backoff;
      backoff = Math.min(backoff * 2, 15000);
      window.setTimeout(connect, wait);
    });
  }

  /* ---------- wiring ---------- */

  function onAction(container, handle) {
    container.addEventListener("click", function (event) {
      var button = event.target.closest("button[data-act]");
      if (!button || !container.contains(button)) { return; }
      var item = button.closest("[data-key]");
      if (item) { handle(button.dataset.act, item.dataset.key); }
    });
  }

  function onDialog(dialog, go) {
    dialog.addEventListener("click", function (event) {
      var button = event.target.closest("button[data-act]");
      if (!button) { return; }
      if (button.dataset.act === "cancel") { closeDialog(dialog); }
      if (button.dataset.act === "go") { go(button); }
    });
  }

  function wire() {
    onAction($("#watches"), function (act, id) {
      var w = watchById(id);
      if (!w) { return; }
      if (act === "unbabysit" || act === "forget") {
        run(id, act, function () { return send("POST", "/api/sessions/" + id + "/unbabysit", {}); });
      }
      if (act === "resume") {
        run(id, act, function () { return send("POST", "/api/sessions/" + id + "/resume-watch", {}); });
      }
      if (act === "stop") { openStop(id); }
      if (act === "back") { openBack(id); }
      if (act === "open-terminal") {
        run(id, act, function () { return send("POST", "/api/sessions/" + id + "/open-terminal", {}); });
      }
      if (act === "copy-attach") { copyText(w.attachCmd || ""); }
      if (act === "copy-ssh") { copyText(w.sshAttachCmd || ""); }
    });
    onAction($("#sessions"), function (act, id) {
      var s = sessionById(id);
      if (!s) { return; }
      if (act === "babysit") { openBabysit(id); }
      if (act === "stop") { openStop(id); }
      if (act === "copy-attach") { copyText(s.attachCmd || ""); }
      if (act === "copy-ssh") { copyText(s.sshAttachCmd || ""); }
    });
    onAction($("#past"), function (act, id) {
      var p = pastById(id);
      if (!p) { return; }
      if (act === "copy-cmd") { copyText(pastCommand(p)); }
      if (act === "copy-ssh") { copyText(p.sshAttachCmd || ""); }
    });
    $('[data-act="copy-start"]', $("#watches-hero")).addEventListener("click", function () {
      copyText(START_CMD);
    });

    $("#search").addEventListener("input", function (event) {
      state.search = event.target.value.trim().toLowerCase();
      render();
    });

    $("#toggle-activity").addEventListener("click", function () { setActivity($("#activity").hidden); });
    $("#close-activity").addEventListener("click", function () { setActivity(false); });
    $("#activity-filter").addEventListener("change", function (event) {
      state.filter = event.target.value;
      renderActivity();
    });

    $("#open-settings").addEventListener("click", openSettings);
    $("#scrim").addEventListener("click", closeDrawer);
    $$('[data-close="drawer"]').forEach(function (button) { button.addEventListener("click", closeDrawer); });
    $("#set-theme-seg").addEventListener("click", onThemeButton);
    onSettingBox("#set-autostart", "autostart");
    onSettingBox("#set-open-browser", "autoOpenBrowser");
    onSettingBox("#set-auto-babysit", "autoBabysit");

    onDialog($("#dlg-babysit"), doBabysit);
    onDialog($("#dlg-stop"), doStop);
    onDialog($("#dlg-quit"), doQuit);
    $("#open-quit").addEventListener("click", function () { openDialog($("#dlg-quit")); });
    onDialog($("#dlg-back"), doBack);
    $('[data-act="copy-resume"]', $("#dlg-back")).addEventListener("click", function () {
      copyText(f($("#dlg-back"), "resume").textContent);
    });

    /* Escape is closed by hand, so that it behaves the same whichever of
       a dialog or the drawer is in front and whatever the browser would
       have done with the key on its own. */
    document.addEventListener("keydown", function (event) {
      if (event.key !== "Escape") { return; }
      var dialog = document.querySelector("dialog[open]");
      if (dialog) {
        event.preventDefault();
        closeDialog(dialog);
        return;
      }
      if (state.drawer) { closeDrawer(); }
    });
  }

  /* start connects the stream before asking for anything, so nothing
     written in between is missed; what the stream has already delivered is
     then merged with the history rather than overwritten by it. */
  function start() {
    wire();
    setActivity(activityWasOpen());
    connect();
    /* A page that comes up blank with nothing said is the worst thing to
       be handed, so a refusal here is shown rather than swallowed. The
       stream may still bring a view along afterwards. */
    getJSON("/api/state").then(applyView, function (err) {
      toast(err && err.message ? err.message : statusMessage(0), true);
    });
    loadActivity();
    tickTimes();
  }

  /* loadActivity asks for the history and folds it into whatever the
     stream has already delivered. */
  function loadActivity() {
    return getJSON("/api/activity?n=200").then(mergeActivity, function () { /* the stream will bring the new ones */ });
  }

  window.ccb = {
    state: state,
    render: render,
    toast: toast
  };

  start();

}());
