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

  // The script is here, so on a phone the folds may open over the page:
  // without it docs.css keeps them in the page's flow.
  if (document.documentElement.classList) document.documentElement.classList.add("folds");

  // The widths at which each fold opens, and the phone's, as in docs.css.
  var folds = [
    { box: document.querySelector(".sidebox"), query: "(min-width: 761px)" },
    { box: document.querySelector(".toc"), query: "(min-width: 1101px)" }
  ];
  var phone = window.matchMedia ? window.matchMedia("(max-width: 760px)") : null;
  folds.forEach(function (f) {
    if (!f.box || !window.matchMedia) return;
    f.mq = window.matchMedia(f.query);
    function fit() { f.box.open = f.mq.matches; }
    fit();
    if (f.mq.addEventListener) f.mq.addEventListener("change", fit);
    else if (f.mq.addListener) f.mq.addListener(fit);
  });

  // Beside the page, On this page is a label: its summary does not fold it
  // and takes no focus.
  var toc = folds[1];
  if (toc.box && toc.mq) {
    var label = toc.box.querySelector("summary");
    label.addEventListener("click", function (ev) { if (toc.mq.matches) ev.preventDefault(); });
    var still = function () { if (toc.mq.matches) label.setAttribute("tabindex", "-1"); else label.removeAttribute("tabindex"); };
    still();
    if (toc.mq.addEventListener) toc.mq.addEventListener("change", still);
    else if (toc.mq.addListener) toc.mq.addListener(still);
  }

  // On a phone the folds open over the page, so an open one closes again
  // when a link in it is chosen, on a click or focus anywhere else, and on
  // Escape. Wider, they sit in the page and stay as they are.
  function overPage() {
    if (!phone || !phone.matches) return [];
    return folds.filter(function (f) { return f.box && f.box.open; });
  }
  document.addEventListener("click", function (ev) {
    overPage().forEach(function (f) {
      var link = ev.target && ev.target.closest ? ev.target.closest("a") : null;
      if (!f.box.contains(ev.target) || (link && f.box.contains(link))) f.box.open = false;
    });
  });
  document.addEventListener("focusout", function (ev) {
    overPage().forEach(function (f) {
      if (f.box.contains(ev.target) && !f.box.contains(ev.relatedTarget)) f.box.open = false;
    });
  });
  document.addEventListener("keydown", function (ev) {
    if (ev.key !== "Escape") return;
    overPage().forEach(function (f) {
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
    // The line sits just below where a heading lands when its link is
    // followed: the header's 56px and the heading's scroll margin in docs.css.
    var at = sectionAt(heads.map(function (h) { return h.getBoundingClientRect().top; }), 80);
    // At the very end of the page the last section is the one being read,
    // even when its heading cannot scroll up to the line.
    if (window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 2) at = heads.length - 1;
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
