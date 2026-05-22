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
