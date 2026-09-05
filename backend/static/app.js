// app.js — voter identity bootstrap, htmx glue, TTS, live updates.

// ---------- Toasts ----------
// Small transient messages (errors from votes, rate limits, "submitted").
function toast(message, kind = "info", ttl = 4000) {
  const host = document.getElementById("toasts");
  if (!host) return;
  const el = document.createElement("div");
  el.className = `toast toast--${kind}`;
  el.textContent = message;
  host.appendChild(el);
  requestAnimationFrame(() => el.classList.add("show"));
  setTimeout(() => {
    el.classList.remove("show");
    el.addEventListener("transitionend", () => el.remove(), { once: true });
    setTimeout(() => el.remove(), 600); // fallback if no transition fires
  }, ttl);
}

// ---------- Voter identity ----------
// Load FingerprintJS OSS and attach visitorId to every htmx request. The very
// first request to the API seeds the voter cookie from this header, so nothing
// may hit the API (list load, websocket) until the fingerprint is resolved —
// otherwise the cookie is seeded randomly and fingerprint recovery is lost.
const identityReady = (async function () {
  const FP_URL = "https://openfpcdn.io/fingerprintjs/v4";
  let visitorId = null;
  try {
    // Cap the wait so a slow/blocked CDN can't hold up the first list load.
    const timeout = new Promise((_, reject) =>
      setTimeout(() => reject(new Error("fingerprint timeout")), 3000)
    );
    const result = await Promise.race([
      import(FP_URL).then((FingerprintJS) => FingerprintJS.load()).then((fp) => fp.get()),
      timeout,
    ]);
    visitorId = result.visitorId;
  } catch (err) {
    console.warn("FingerprintJS unavailable, using local id:", err.message || err);
  }
  if (!visitorId) {
    // Fallback: persist a random id in localStorage so the user can still vote.
    try {
      visitorId = localStorage.getItem("names_visitor_id");
      if (!visitorId) {
        visitorId = crypto.randomUUID();
        localStorage.setItem("names_visitor_id", visitorId);
      }
    } catch {
      visitorId = crypto.randomUUID();
    }
  }
  window.__voterFingerprint = visitorId;

  document.body.addEventListener("htmx:configRequest", (evt) => {
    evt.detail.headers["X-Voter-Fingerprint"] = visitorId;
  });
  return visitorId;
})();

// ---------- htmx response handling ----------
// htmx 2 refuses to swap 4xx/5xx responses by default, which would leave a
// rejected submission with no feedback. Route errors to the right place:
//   * submit form  → the inline #submit-error span (server already retargets)
//   * anything else → a toast (vote on a removed name, rate limit, …)
// JSON bodies ({"error": "..."}) are unwrapped into plain text first.
document.addEventListener("htmx:beforeSwap", (evt) => {
  const xhr = evt.detail.xhr;
  if (!xhr || xhr.status < 400) return;
  let msg = xhr.responseText || "";
  const ctype = xhr.getResponseHeader("Content-Type") || "";
  if (ctype.includes("application/json")) {
    try {
      msg = JSON.parse(msg).error || msg;
    } catch {}
  }
  if (!msg) msg = xhr.status === 429 ? "Slow down a little." : "Something went wrong.";

  const src = evt.detail.requestConfig && evt.detail.requestConfig.elt;
  if (src && src.closest("#submit-form")) {
    evt.detail.shouldSwap = true;
    evt.detail.serverResponse = msg;
    evt.detail.target = document.getElementById("submit-error");
    return;
  }
  evt.detail.shouldSwap = false;
  toast(msg, "error");
});

// Network-level failures (offline, server restarting) never reach beforeSwap.
document.addEventListener("htmx:sendError", () => {
  toast("Couldn't reach the server — check your connection.", "error");
});

// Submission accepted: the server fires names:submitted with the accepted text.
document.body.addEventListener("names:submitted", (evt) => {
  const form = document.getElementById("submit-form");
  if (form) form.reset();
  const input = document.getElementById("submit-input");
  if (input) input.focus();
  const text = evt.detail && evt.detail.text;
  toast(text ? `Added “${text}”` : "Submitted!", "success");
});

// Clear a stale error as soon as the user edits the input again.
document.addEventListener("input", (evt) => {
  if (evt.target && evt.target.id === "submit-input") {
    const err = document.getElementById("submit-error");
    if (err) err.textContent = "";
  }
});

// ---------- List status + paging ----------
// Keep a hidden `limit` on the filter form equal to the number of rows on
// screen, so a live-update refresh re-fetches everything the user has paged
// through instead of collapsing back to the first page. Reset on any filter
// change.
function syncListStatus() {
  const list = document.getElementById("names-list");
  const status = document.getElementById("list-status");
  const form = document.getElementById("filter-form");
  if (!list || !status || !form) return;
  const rows = list.querySelectorAll(".name-row").length;
  const more = !!list.querySelector(".load-more");
  let limitInput = form.querySelector("input[name='limit']");
  if (!limitInput) {
    limitInput = document.createElement("input");
    limitInput.type = "hidden";
    limitInput.name = "limit";
    form.appendChild(limitInput);
  }
  limitInput.value = rows > 50 ? String(rows) : "";
  if (rows === 0) {
    status.textContent = "";
  } else {
    status.textContent = `${rows}${more ? "+" : ""} name${rows === 1 ? "" : "s"}`;
  }
}
document.body.addEventListener("htmx:afterSwap", (evt) => {
  if (evt.target && (evt.target.id === "names-list" || evt.target.closest?.("#names-list"))) {
    syncListStatus();
  }
});
document.addEventListener("change", (evt) => {
  const form = document.getElementById("filter-form");
  if (form && evt.target && form.contains(evt.target) && evt.target.name !== "limit") {
    const limitInput = form.querySelector("input[name='limit']");
    if (limitInput) limitInput.value = "";
  }
});

// ---------- Keyboard shortcut ----------
// "/" focuses search (like GitHub); Escape clears it.
document.addEventListener("keydown", (evt) => {
  const tag = (evt.target.tagName || "").toLowerCase();
  const typing = tag === "input" || tag === "textarea" || evt.target.isContentEditable;
  if (evt.key === "/" && !typing && !evt.ctrlKey && !evt.metaKey && !evt.altKey) {
    evt.preventDefault();
    document.getElementById("search-input")?.focus();
  } else if (evt.key === "Escape" && evt.target.id === "search-input" && evt.target.value) {
    evt.target.value = "";
    evt.target.dispatchEvent(new Event("input", { bubbles: true }));
  }
});

// ---------- Text-to-speech ----------
// Single shared SpeechSynthesis controller. We always cancel before queueing
// new utterances so the latest click wins and we never overlap voices.
const TTS = (() => {
  const synth = window.speechSynthesis;
  const supported = !!synth;
  const stopBtn = () => document.getElementById("speak-stop");
  const allBtn = () => document.getElementById("speak-all");

  function showStop(show) {
    const s = stopBtn();
    const a = allBtn();
    if (s) s.hidden = !show;
    if (a) a.disabled = show;
  }

  function speak(texts) {
    if (!supported || !texts.length) return;
    synth.cancel();
    let remaining = texts.length;
    texts.forEach((t) => {
      const u = new SpeechSynthesisUtterance(String(t));
      u.rate = 1.0;
      u.pitch = 1.0;
      u.onend = () => {
        remaining--;
        if (remaining <= 0) showStop(false);
      };
      u.onerror = u.onend;
      synth.speak(u);
    });
    showStop(true);
  }

  function stop() {
    if (!supported) return;
    synth.cancel();
    showStop(false);
  }

  return { speak, stop, supported };
})();

document.addEventListener("click", (evt) => {
  const speakBtn = evt.target.closest(".speak");
  if (speakBtn) {
    evt.preventDefault();
    let text =
      speakBtn.dataset.speak ||
      speakBtn.closest(".name-row")?.dataset.name ||
      "";
    if (!text && speakBtn.id === "submit-preview") {
      text = (document.getElementById("submit-input")?.value || "").trim();
    }
    if (text) TTS.speak([text]);
    return;
  }
  if (evt.target.closest("#speak-all")) {
    evt.preventDefault();
    const names = Array.from(document.querySelectorAll("#names-list .name-row"))
      .map((row) => row.dataset.name)
      .filter(Boolean);
    TTS.speak(names);
    return;
  }
  if (evt.target.closest("#speak-stop")) {
    evt.preventDefault();
    TTS.stop();
    return;
  }
});

// Stop TTS whenever the list reloads so a stale read doesn't keep going.
document.addEventListener("htmx:beforeRequest", (evt) => {
  if (evt.target && evt.target.id === "filter-form") TTS.stop();
});

// ---------- Relative timestamps ----------
// Converts <time data-rel datetime="..."> elements into "5m ago", "2h ago",
// etc. Re-runs after every htmx swap so newly inserted rows get formatted.
function formatRelative(date) {
  const diffMs = Date.now() - date.getTime();
  const sec = Math.round(diffMs / 1000);
  if (sec < 45) return "just now";
  const min = Math.round(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.round(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const day = Math.round(hr / 24);
  if (day < 7) return `${day}d ago`;
  if (day < 30) return `${Math.round(day / 7)}w ago`;
  if (day < 365) return `${Math.round(day / 30)}mo ago`;
  return `${Math.round(day / 365)}y ago`;
}
function refreshRelativeTimes(root) {
  (root || document).querySelectorAll("time[data-rel]").forEach((el) => {
    const dt = el.getAttribute("datetime");
    if (!dt) return;
    const d = new Date(dt);
    if (isNaN(d.getTime())) return;
    el.textContent = formatRelative(d);
    // Hover shows the exact time in the viewer's own timezone.
    el.title = d.toLocaleString();
  });
}
document.body.addEventListener("htmx:afterSwap", (evt) =>
  refreshRelativeTimes(evt.target)
);
setInterval(() => refreshRelativeTimes(), 60_000);

// ---------- Live updates via WebSocket ----------
// The server pushes {"type":"names.changed"} whenever something is submitted,
// voted, flagged, or moderated. We coalesce rapid bursts into a single
// `names:refresh` event, which the filter-form already listens for and which
// re-runs the GET with the existing cookie/fingerprint so the per-voter
// projection (my_vote, my_flag) stays correct.
const live = (function () {
  let refreshTimer = null;
  function scheduleRefresh() {
    if (refreshTimer) return;
    refreshTimer = setTimeout(() => {
      refreshTimer = null;
      // Don't yank the list out from under someone mid-vote.
      if (document.querySelector("#names-list .htmx-request")) {
        scheduleRefresh();
        return;
      }
      document.body.dispatchEvent(new CustomEvent("names:refresh"));
    }, 250);
  }

  let backoff = 1000;
  let socket = null;
  let wasConnected = false;
  function connect() {
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const url = `${proto}//${location.host}/ws`;
    try {
      socket = new WebSocket(url);
    } catch (e) {
      scheduleReconnect();
      return;
    }
    socket.addEventListener("open", () => {
      backoff = 1000;
      // Catch up on anything missed while disconnected.
      if (wasConnected) scheduleRefresh();
      wasConnected = true;
    });
    socket.addEventListener("message", (evt) => {
      let data;
      try {
        data = JSON.parse(evt.data);
      } catch {
        return;
      }
      if (data && data.type === "names.changed") scheduleRefresh();
    });
    socket.addEventListener("close", scheduleReconnect);
    socket.addEventListener("error", () => {
      try {
        socket.close();
      } catch {}
    });
  }
  function scheduleReconnect() {
    setTimeout(connect, backoff);
    backoff = Math.min(backoff * 2, 15_000);
  }
  // Reconnect promptly when a backgrounded tab comes back.
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible" && socket && socket.readyState > 1) {
      backoff = 1000;
      connect();
    }
  });
  return { connect };
})();

// ---------- Page bootstrap ----------
document.addEventListener("DOMContentLoaded", () => {
  refreshRelativeTimes();

  // Offensive view toggle: a hidden footer disclosure flips the list into
  // "offensive" mode by mutating the hidden view input and re-triggering the
  // filter form. Always resets to off on load so the clean list is default.
  const toggle = document.getElementById("offensive-view-toggle");
  const viewInput = document.getElementById("view-input");
  if (toggle && viewInput) {
    toggle.checked = false;
    viewInput.value = "active";
    toggle.addEventListener("change", () => {
      viewInput.value = toggle.checked ? "offensive" : "active";
      document.body.dispatchEvent(new CustomEvent("names:refresh"));
    });
  }

  // Remember whether the guidelines box was collapsed.
  const guidelines = document.getElementById("guidelines");
  if (guidelines) {
    try {
      if (localStorage.getItem("names_guidelines") === "closed") guidelines.open = false;
    } catch {}
    guidelines.addEventListener("toggle", () => {
      try {
        localStorage.setItem("names_guidelines", guidelines.open ? "open" : "closed");
      } catch {}
    });
  }

  // Disable TTS controls if the browser can't do speech synthesis.
  if (!TTS.supported) {
    document
      .querySelectorAll(".speak, #speak-all, #speak-stop")
      .forEach((el) => {
        el.hidden = true;
      });
  }

  // First list load + live socket only once the voter identity is settled.
  identityReady.then(() => {
    document.body.dispatchEvent(new CustomEvent("names:refresh"));
    live.connect();
  });
});
