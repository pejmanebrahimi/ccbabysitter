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

  // queryWords is a query as the words to look for: letters and digits in
  // any language, joined by ' . _ or -, lower case, with a phone's curly
  // apostrophe made straight and trailing punctuation dropped. One-letter
  // words are left out when there are longer ones.
  function queryWords(query) {
    var q = String(query).toLowerCase().replace(/[\u2018\u2019]/g, "'");
    var all = (q.match(/[\p{L}\p{N}][\p{L}\p{N}.'_-]*/gu) || []).map(function (w) {
      return w.replace(/[.'_-]+$/, "");
    }).filter(function (w, i, list) { return w && list.indexOf(w) === i; });
    var long = all.filter(function (w) { return w.length > 1; });
    return long.length ? long : all;
  }

  // flat is text in lower case with every character that cannot be in a
  // word turned into a space, one for one, and a space in front, so " "+w
  // finds w only at the start of a word.
  function flat(text) {
    return " " + text.toLowerCase().replace(/[^\p{L}\p{N}.'_-]/gu, " ");
  }

  // search finds the entries of the docs' search index that hold every
  // word of query at the start of a word, best first, at most max of them.
  // A word in a section's title counts most, then in the page's title, more
  // for the page itself than for its sections, then in the text, and a
  // tie goes to the entry where the words come up most. A section only
  // matches when its own title or text holds a word. When nothing matches,
  // the words are tried again without a plural s. Each result carries a
  // snippet of its text from just before the first word found in it.
  function search(index, query, max) {
    var words = queryWords(query);
    var found = searchWords(index, words);
    if (!found.length) {
      var singular = words.map(function (w) { return w.length > 3 ? w.replace(/s$/, "") : w; });
      if (singular.join(" ") !== words.join(" ")) found = searchWords(index, singular);
    }
    return found.slice(0, max);
  }

  function searchWords(index, words) {
    if (!words.length) return [];
    var found = [];
    index.forEach(function (e, order) {
      var section = flat(e.section || ""), page = flat(e.page), text = flat(e.text);
      var score = 0, own = 0, often = 0, at = -1;
      for (var i = 0; i < words.length; i++) {
        var w = " " + words[i], hit = 0;
        if (section.indexOf(w) >= 0) { hit += 4; own++; }
        if (page.indexOf(w) >= 0) hit += e.section ? 1 : 3;
        var t = text.indexOf(w);
        if (t >= 0) {
          hit += 1;
          own++;
          if (at < 0 || t < at) at = t;
          often += Math.min(text.split(w).length - 1, 10);
        }
        if (!hit) return;
        score += hit;
      }
      if (e.section && !own) return;
      found.push({ e: e, score: score + often / 100, order: order, at: at });
    });
    found.sort(function (a, b) { return b.score - a.score || a.order - b.order; });
    return found.map(function (f) {
      return { url: f.e.url, page: f.e.page, section: f.e.section || "", snippet: snippet(f.e.text, Math.max(f.at, 0)) };
    });
  }

  // snippet is about 160 characters of text from a little before at, cut
  // at spaces, with ... where it was cut.
  function snippet(text, at) {
    var start = 0;
    if (at > 60) {
      start = text.indexOf(" ", at - 60) + 1;
      if (start <= 0 || start > at) start = at;
    }
    var end = text.length;
    if (end - start > 160) {
      end = text.lastIndexOf(" ", start + 160);
      if (end <= start) end = start + 160;
    }
    return (start > 0 ? "... " : "") + text.slice(start, end) + (end < text.length ? " ..." : "");
  }

  window.ccbDocs = { sectionAt: sectionAt, search: search };

  // wireSearch makes the header's search box work: without the script, or
  // without fetch, it stays hidden. The index is read the first time the
  // box is used. Results are links: Arrow Down moves into them and between
  // them, Enter in the box follows the first, and Escape closes the list.
  // A status only screen readers hear says how many there are.
  function wireSearch(form) {
    if (!form || !window.fetch) return;
    var input = form.querySelector("input"), list = form.querySelector(".results"), status = form.querySelector("[role=status]");
    var loading = null;
    form.hidden = false;
    function load() {
      if (!loading) {
        loading = window.fetch("/docs/search.json" + version).then(function (r) {
          if (!r.ok) throw new Error("search index: " + r.status);
          return r.json();
        }).catch(function () { loading = null; return null; });
      }
      return loading;
    }
    function close() { list.hidden = true; }
    function say(text) { status.textContent = text; }
    function line(text) {
      var li = document.createElement("li");
      li.className = "none";
      li.textContent = text;
      list.appendChild(li);
    }
    function render() {
      var q = input.value;
      if (!q.trim()) { list.replaceChildren(); close(); say(""); return; }
      load().then(function (data) {
        if (input.value !== q) return;
        list.replaceChildren();
        if (!data) {
          line("Search is not available right now.");
          say("Search is not available right now.");
          list.hidden = false;
          return;
        }
        var found = search(data, q, 8);
        if (!found.length) line("Nothing found for " + q.trim());
        // Only an address within the docs is ever made a link.
        found = found.filter(function (r) { return /^\/docs\/[\w\/-]*(#[\w-]+)?$/.test(r.url); });
        found.forEach(function (r) {
          var li = document.createElement("li"), a = document.createElement("a");
          var where = document.createElement("span"), snip = document.createElement("span");
          a.href = r.url;
          where.className = "where";
          where.textContent = r.section || r.page;
          if (r.section) {
            var page = document.createElement("small");
            page.textContent = " in " + r.page;
            where.appendChild(page);
          }
          snip.className = "snip";
          snip.textContent = r.snippet;
          a.appendChild(where);
          a.appendChild(snip);
          li.appendChild(a);
          list.appendChild(li);
        });
        say(found.length ? found.length + (found.length === 1 ? " result" : " results") : "Nothing found for " + q.trim());
        list.hidden = false;
      });
    }
    input.addEventListener("focus", function () {
      load();
      if (list.children.length) list.hidden = false;
    });
    input.addEventListener("input", render);
    // A press on a result keeps the focus where it is, so a browser that
    // does not focus a link on a click, as Safari, still follows it.
    list.addEventListener("mousedown", function (ev) { ev.preventDefault(); });
    list.addEventListener("click", function (ev) {
      if (ev.target && ev.target.closest && ev.target.closest("a")) close();
    });
    form.addEventListener("submit", function (ev) {
      ev.preventDefault();
      var first = list.querySelector("a");
      if (first && !list.hidden) {
        close();
        window.location.href = first.href;
      }
    });
    form.addEventListener("keydown", function (ev) {
      var links = Array.prototype.slice.call(list.querySelectorAll("a"));
      var at = links.indexOf(document.activeElement);
      if (ev.key === "Escape" && !list.hidden) {
        ev.preventDefault();
        input.focus();
        close();
      } else if (ev.key === "ArrowDown" && links.length && !list.hidden) {
        ev.preventDefault();
        links[Math.min(at + 1, links.length - 1)].focus();
      } else if (ev.key === "ArrowUp" && at >= 0) {
        ev.preventDefault();
        if (at === 0) input.focus(); else links[at - 1].focus();
      }
    });
    document.addEventListener("click", function (ev) {
      if (!form.contains(ev.target)) close();
    });
    form.addEventListener("focusout", function (ev) {
      if (!form.contains(ev.relatedTarget)) close();
    });
  }

  if (typeof document === "undefined" || !document.querySelector) return;

  // The index is the one deployed with this script: the same ?v= the
  // page loaded it with, so a cached older one is never mixed in.
  var me = document.currentScript || document.querySelector("script[src*='/assets/docs.js']");
  var version = me && me.src.indexOf("?") >= 0 ? me.src.slice(me.src.indexOf("?")) : "";
  wireSearch(document.querySelector(".search"));

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
