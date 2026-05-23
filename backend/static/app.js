// app.js — load FingerprintJS OSS, attach visitorId to every fetch/htmx request.
(async function () {
  const FP_URL = "https://openfpcdn.io/fingerprintjs/v4";
  let visitorId = null;
  try {
    const FingerprintJS = await import(FP_URL);
    const fp = await FingerprintJS.load();
    const result = await fp.get();
    visitorId = result.visitorId;
  } catch (err) {
    console.warn("FingerprintJS failed to load:", err);
  }
  if (!visitorId) {
    // Fallback: persist a random id in localStorage so the user can still vote.
    visitorId = localStorage.getItem("names_visitor_id");
    if (!visitorId) {
      visitorId = crypto.randomUUID();
      localStorage.setItem("names_visitor_id", visitorId);
    }
  }
  window.__voterFingerprint = visitorId;

  // htmx hook: add fingerprint header to every request.
  document.body.addEventListener("htmx:configRequest", (evt) => {
    evt.detail.headers["X-Voter-Fingerprint"] = visitorId;
  });

  // Trigger first load now that fingerprint is ready (form has hx-trigger="load" too,
  // but this guarantees it fires after the header hook is registered).
  document.body.dispatchEvent(new CustomEvent("names:refresh"));
})();

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
    texts.forEach((t, i) => {
      const u = new SpeechSynthesisUtterance(String(t));
      u.rate = 1.0;
      u.pitch = 1.0;
      // Tiny pause between names — done by a trailing space + onend tick.
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

// ---------- Offensive view toggle ----------
// A hidden footer disclosure flips the list into "offensive" mode by mutating
// the hidden view input and re-triggering the filter form. The toggle resets
// to off on page load so the family-friendly list is always the default.
document.addEventListener("DOMContentLoaded", () => {
  const toggle = document.getElementById("offensive-view-toggle");
  const viewInput = document.getElementById("view-input");
  const form = document.getElementById("filter-form");
  if (!toggle || !viewInput || !form) return;
  toggle.checked = false;
  viewInput.value = "active";
  toggle.addEventListener("change", () => {
    viewInput.value = toggle.checked ? "offensive" : "active";
    document.body.dispatchEvent(new CustomEvent("names:refresh"));
  });

  // Disable TTS controls if the browser can't do speech synthesis.
  if (!TTS.supported) {
    document
      .querySelectorAll(".speak, #speak-all, #speak-stop")
      .forEach((el) => {
        el.hidden = true;
      });
  }
});
