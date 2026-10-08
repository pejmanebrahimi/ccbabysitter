/* The docs pages' small behaviours: the sidebar and "On this page" are
   open on a screen wide enough to show them beside the page and folded on
   a narrower one, and "On this page" marks the section being read. The
   pages are whole without it: both start open in the HTML. */
"use strict";
(function () {
  // sectionAt is the index of the section being read: the last one whose
  // heading has reached the line, in pixels from the top of the window, or
  // -1 above the first.
  function sectionAt(tops, line) {
    var at = -1;
    for (var i = 0; i < tops.length; i++) {
      if (tops[i] <= line) at = i;
    }
    return at;
  }

  window.ccbDocs = { sectionAt: sectionAt };
  if (typeof document === "undefined" || !document.querySelector) return;

  // The widths at which each fold opens, as in docs.css.
  var folds = [
    { box: document.querySelector(".sidebox"), query: "(min-width: 761px)" },
    { box: document.querySelector(".toc"), query: "(min-width: 1101px)" }
  ];
  folds.forEach(function (f) {
    if (!f.box || !window.matchMedia) return;
    f.mq = window.matchMedia(f.query);
    function fit() { f.box.open = f.mq.matches; }
    fit();
    if (f.mq.addEventListener) f.mq.addEventListener("change", fit);
  });

  // On a narrow screen a fold opens over the page, so it closes again when
  // a link in it is chosen, on a click anywhere else, and on Escape.
  function folded() {
    return folds.filter(function (f) { return f.box && f.mq && !f.mq.matches && f.box.open; });
  }
  document.addEventListener("click", function (ev) {
    folded().forEach(function (f) {
      var link = ev.target.closest ? ev.target.closest("a") : null;
      if (!f.box.contains(ev.target) || (link && f.box.contains(link))) f.box.open = false;
    });
  });
  document.addEventListener("keydown", function (ev) {
    if (ev.key !== "Escape") return;
    folded().forEach(function (f) {
      var refocus = f.box.contains(document.activeElement);
      f.box.open = false;
      if (refocus) f.box.querySelector("summary").focus();
    });
  });

  var links = Array.prototype.slice.call(document.querySelectorAll(".toc-list a[href^='#']"));
  var heads = links.map(function (a) { return document.getElementById(a.getAttribute("href").slice(1)); });
  if (!links.length || heads.indexOf(null) >= 0) return;
  var queued = false;
  function mark() {
    queued = false;
    var at = sectionAt(heads.map(function (h) { return h.getBoundingClientRect().top; }), 90);
    links.forEach(function (a, i) {
      a.classList.toggle("here", i === at);
      if (i === at) a.setAttribute("aria-current", "location"); else a.removeAttribute("aria-current");
    });
  }
  window.addEventListener("scroll", function () {
    if (!queued) { queued = true; window.requestAnimationFrame(mark); }
  }, { passive: true });
  mark();
})();
