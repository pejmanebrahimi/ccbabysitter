/* The page's small behaviours: the light and dark switch, the tabs of the
   install command, and copying a command. Each one works
   without the others. */
"use strict";
(function () {
  var KEY = "ccb.site.theme";
  var ENVS = [
    { name: "macOS &amp; Linux", prompt: "$", cmd: "curl -fsSL https://ccbabysitter.dev/install.sh | sh" },
    { name: "Windows", prompt: "&gt;", cmd: "irm https://ccbabysitter.dev/install.ps1 | iex" },
    { name: "Claude Code", prompt: "&gt;", cmd: "/plugin install ccbabysitter --marketplace pejmanebrahimi/ccbabysitter" },
    { name: "Go", prompt: "$", cmd: "go install ccbabysitter.dev/ccbabysitter/cmd/ccbabysitter@latest" }
  ];

  function readTheme(storage) {
    try { var v = storage.getItem(KEY); return v === "light" || v === "dark" ? v : ""; } catch (e) { return ""; }
  }
  function writeTheme(storage, v) {
    try { storage.setItem(KEY, v); } catch (e) { /* the choice lasts for this visit only */ }
  }
  function nextTheme(current, systemDark) {
    var dark = current ? current === "dark" : systemDark;
    return dark ? "light" : "dark";
  }
  // chooseTheme is the theme after one click. A choice equal to the
  // system's is not kept, so the page follows the system again from then
  // on, including when the system changes.
  function chooseTheme(current, systemDark) {
    var theme = nextTheme(current, systemDark), system = systemDark ? "dark" : "light";
    return theme === system ? { theme: theme, attr: "", store: "" } : { theme: theme, attr: theme, store: theme };
  }
  function commandFor(i) { return ENVS[((i % ENVS.length) + ENVS.length) % ENVS.length]; }

  // copyText copies with the clipboard API when it is there and allowed,
  // and otherwise selects the text so the visitor can copy it by hand.
  function copyText(text, doc, nav, selectNode) {
    function fallback() {
      try {
        if (selectNode && doc.createRange && doc.getSelection) {
          var range = doc.createRange(); range.selectNodeContents(selectNode);
          var sel = doc.getSelection(); sel.removeAllRanges(); sel.addRange(range);
        }
      } catch (e) { /* nothing more to do */ }
      return false;
    }
    try {
      if (nav && nav.clipboard && nav.clipboard.writeText) {
        return nav.clipboard.writeText(text).then(function () { return true; }, fallback);
      }
    } catch (e) { /* fall through */ }
    return { then: function (f) { return f(fallback()); } };
  }

  window.ccbSite = { readTheme: readTheme, writeTheme: writeTheme, nextTheme: nextTheme, chooseTheme: chooseTheme, commandFor: commandFor, copyText: copyText };
  if (typeof document === "undefined" || !document.querySelector) return;

  var root = document.documentElement;

  // switchTheme applies one click of the theme switch to the page and to
  // storage, and returns the theme the page now shows.
  function switchTheme() {
    var sys = false;
    try { sys = window.matchMedia("(prefers-color-scheme: dark)").matches; } catch (e) { /* no preference: light */ }
    var r = chooseTheme(root.getAttribute("data-theme") || "", sys);
    if (r.attr) root.setAttribute("data-theme", r.attr); else root.removeAttribute("data-theme");
    try {
      if (r.store) writeTheme(window.localStorage, r.store); else window.localStorage.removeItem(KEY);
    } catch (e) { /* the choice lasts for this visit only */ }
    return r.theme;
  }
  window.ccbSite.switchTheme = switchTheme;

  var themeButton = document.querySelector("[data-theme-switch]");
  if (themeButton) themeButton.addEventListener("click", function () { switchTheme(); });

  // The install tabs. The static HTML shows the first tab selected; a click
  // or an arrow key selects another and puts its command in the line.
  Array.prototype.forEach.call(document.querySelectorAll("[data-tabs]"), function (list) {
    var line = document.getElementById(list.getAttribute("data-tabs"));
    var tabs = list.querySelectorAll("[role=tab]");
    if (!line || !tabs.length) return;
    function select(k, focus) {
      var e = commandFor(k);
      for (var i = 0; i < tabs.length; i++) {
        tabs[i].setAttribute("aria-selected", i === k ? "true" : "false");
        tabs[i].setAttribute("tabindex", i === k ? "0" : "-1");
      }
      line.querySelector("[data-prompt]").innerHTML = e.prompt;
      line.querySelector("[data-cmd]").textContent = e.cmd;
      if (focus) tabs[k].focus();
    }
    Array.prototype.forEach.call(tabs, function (tab, i) {
      tab.addEventListener("click", function () { select(i, false); });
      tab.addEventListener("keydown", function (ev) {
        var moves = { ArrowRight: i + 1, ArrowLeft: i - 1, Home: 0, End: tabs.length - 1 };
        if (!(ev.key in moves)) return;
        ev.preventDefault();
        select((moves[ev.key] + tabs.length) % tabs.length, true);
      });
    });
  });

  Array.prototype.forEach.call(document.querySelectorAll("[data-copy]"), function (btn) {
    btn.addEventListener("click", function () {
      var cmd = btn.querySelector("[data-cmd]");
      var label = btn.querySelector("[data-copy-label]");
      copyText(cmd.textContent, document, navigator, cmd).then(function (ok) {
        if (!label) return;
        label.textContent = ok ? "Copied" : "Selected";
        setTimeout(function () { label.textContent = "Copy"; }, 1300);
      });
    });
  });
})();
