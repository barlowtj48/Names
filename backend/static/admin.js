// admin.js — vanilla JS, no htmx. Login, review queue, list, moderate.
const $ = (s) => document.querySelector(s);

let token = null;
const PAGE = 100;
let namesOffset = 0;

function toast(message, kind = "info", ttl = 4000) {
  const host = $("#toasts");
  if (!host) return;
  const el = document.createElement("div");
  el.className = `toast toast--${kind}`;
  el.textContent = message;
  host.appendChild(el);
  requestAnimationFrame(() => el.classList.add("show"));
  setTimeout(() => {
    el.classList.remove("show");
    setTimeout(() => el.remove(), 300);
  }, ttl);
}

async function apiFetch(path, opts = {}) {
  opts.headers = Object.assign({}, opts.headers || {}, {
    "Content-Type": "application/json",
  });
  if (token) opts.headers["Authorization"] = "Bearer " + token;
  let r;
  try {
    r = await fetch(path, opts);
  } catch {
    toast("Couldn't reach the server.", "error");
    throw new Error("network");
  }
  if (r.status === 401) {
    logout();
    throw new Error("unauthorized");
  }
  if (!r.ok) {
    let msg = `Request failed (${r.status})`;
    try {
      msg = (await r.json()).error || msg;
    } catch {}
    toast(msg, "error");
    throw new Error(msg);
  }
  return r;
}

async function loadNames() {
  namesOffset = 0;
  await Promise.all([loadQueue(), loadAllNames(false)]);
}

function statusPill(status) {
  const labels = {
    active: "active",
    pending_review: "pending review",
    offensive: "offensive",
    removed: "removed",
  };
  return `<span class="status-pill ${escapeHTML(status)}">${labels[status] || escapeHTML(status)}</span>`;
}

async function loadQueue() {
  const r = await apiFetch("/api/admin/names/queue");
  const data = await r.json();
  const tbody = $("#queue-table tbody");
  tbody.innerHTML = "";
  const queue = data.queue || [];
  $("#queue-count").textContent = queue.length
    ? `(${queue.length})`
    : "(empty)";
  if (!queue.length) {
    tbody.innerHTML = `<tr><td colspan="5" class="muted">Nothing waiting for review.</td></tr>`;
    return;
  }
  for (const n of queue) {
    const tr = document.createElement("tr");
    const when = n.last_flagged
      ? new Date(n.last_flagged).toLocaleString()
      : "—";
    tr.innerHTML = `
      <td>${n.id}</td>
      <td>${escapeHTML(n.text)}</td>
      <td class="num">${n.flag_count}</td>
      <td>${when}</td>
      <td class="queue-actions">
        <button data-id="${n.id}" data-action="dismiss" class="dismiss">Dismiss</button>
        <button data-id="${n.id}" data-action="confirm" class="confirm">Confirm offensive</button>
        <button data-id="${n.id}" data-action="remove"  class="remove">Remove</button>
      </td>
    `;
    tbody.appendChild(tr);
  }
  tbody.querySelectorAll("button[data-action]").forEach((b) => {
    b.addEventListener("click", async () => {
      const action = b.dataset.action;
      const label =
        action === "dismiss"
          ? "Dismiss (restore to active)?"
          : action === "confirm"
            ? "Confirm offensive (hide from default list)?"
            : "Remove permanently?";
      if (!confirm(label)) return;
      b.disabled = true;
      try {
        await apiFetch("/api/admin/names/" + b.dataset.id + "/decision", {
          method: "POST",
          body: JSON.stringify({ action }),
        });
        toast(`Name #${b.dataset.id}: ${action}`, "success");
        loadNames();
      } catch {
        b.disabled = false;
      }
    });
  });
}

function namesQuery() {
  const p = new URLSearchParams();
  p.set("sort", $("#sort-select").value || "new");
  p.set("limit", String(PAGE));
  p.set("offset", String(namesOffset));
  const q = $("#names-filter").value.trim();
  if (q) p.set("q", q);
  return p.toString();
}

async function loadAllNames(append) {
  const r = await apiFetch("/api/admin/names?" + namesQuery());
  const data = await r.json();
  const tbody = $("#names-table tbody");
  if (!append) tbody.innerHTML = "";
  const statusFilter = $("#status-filter").value;
  let shown = 0;
  for (const n of data.names) {
    if (statusFilter && n.status !== statusFilter) continue;
    shown++;
    const tr = document.createElement("tr");
    tr.dataset.status = n.status;
    tr.innerHTML = `
      <td>${n.id}</td>
      <td>${escapeHTML(n.text)}</td>
      <td>${statusPill(n.status)}</td>
      <td title="${new Date(n.created_at).toLocaleString()}">${new Date(n.created_at).toLocaleDateString()}</td>
      <td class="num">${n.up}</td>
      <td class="num">${n.down}</td>
      <td class="num">${n.score}</td>
      <td>${n.status === "removed" ? "" : `<button data-id="${n.id}" class="del">Remove</button>`}</td>
    `;
    tbody.appendChild(tr);
  }
  namesOffset += data.names.length;
  $("#names-more").hidden = !data.has_more;
  const total = tbody.querySelectorAll("tr").length;
  $("#names-count").textContent = total ? `(${total}${data.has_more ? "+" : ""})` : "(none)";
  if (!total) {
    tbody.innerHTML = `<tr><td colspan="8" class="muted">No names match.</td></tr>`;
  }
  tbody.querySelectorAll("button.del").forEach((b) => {
    if (b.dataset.bound) return;
    b.dataset.bound = "1";
    b.addEventListener("click", async () => {
      if (!confirm("Remove name #" + b.dataset.id + "?")) return;
      b.disabled = true;
      try {
        await apiFetch("/api/admin/names/" + b.dataset.id, { method: "DELETE" });
        toast(`Removed #${b.dataset.id}`, "success");
        loadNames();
      } catch {
        b.disabled = false;
      }
    });
  });
}

function logout() {
  token = null;
  sessionStorage.removeItem("admin_token");
  $("#login-section").hidden = false;
  $("#admin-section").hidden = true;
}

function showAdmin() {
  $("#login-section").hidden = true;
  $("#admin-section").hidden = false;
  loadNames();
  connectLiveUpdates();
}

// ---------- Live updates ----------
// Same WebSocket the public page uses; on any change notice we reload the
// admin tables so moderators always see the freshest state.
let adminSocket = null;
let adminBackoff = 1000;
let adminReloadTimer = null;
function setLive(on) {
  const el = $("#live-indicator");
  if (!el) return;
  el.textContent = on ? "● live" : "○ reconnecting…";
  el.style.color = on ? "var(--up)" : "";
}
function scheduleAdminReload() {
  if (adminReloadTimer) return;
  adminReloadTimer = setTimeout(() => {
    adminReloadTimer = null;
    loadNames();
  }, 300);
}
function connectLiveUpdates() {
  if (adminSocket && adminSocket.readyState <= 1) return;
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  try {
    adminSocket = new WebSocket(`${proto}//${location.host}/ws`);
  } catch {
    setTimeout(connectLiveUpdates, adminBackoff);
    adminBackoff = Math.min(adminBackoff * 2, 15000);
    return;
  }
  adminSocket.addEventListener("open", () => {
    adminBackoff = 1000;
    setLive(true);
  });
  adminSocket.addEventListener("message", (evt) => {
    let data;
    try {
      data = JSON.parse(evt.data);
    } catch {
      return;
    }
    if (data && data.type === "names.changed") scheduleAdminReload();
  });
  adminSocket.addEventListener("close", () => {
    setLive(false);
    setTimeout(connectLiveUpdates, adminBackoff);
    adminBackoff = Math.min(adminBackoff * 2, 15000);
  });
  adminSocket.addEventListener("error", () => {
    try {
      adminSocket.close();
    } catch {}
  });
}

function escapeHTML(s) {
  return String(s).replace(
    /[&<>"']/g,
    (c) =>
      ({
        "&": "&amp;",
        "<": "&lt;",
        ">": "&gt;",
        '"': "&quot;",
        "'": "&#39;",
      })[c],
  );
}

function debounce(fn, ms) {
  let t;
  return (...a) => {
    clearTimeout(t);
    t = setTimeout(() => fn(...a), ms);
  };
}

document.addEventListener("DOMContentLoaded", () => {
  token = sessionStorage.getItem("admin_token");
  if (token) showAdmin();

  $("#login-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const fd = new FormData(e.target);
    const btn = e.target.querySelector("button[type=submit]");
    btn.disabled = true;
    $("#login-error").textContent = "";
    try {
      const r = await fetch("/api/admin/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          username: fd.get("username"),
          password: fd.get("password"),
        }),
      });
      if (!r.ok) {
        $("#login-error").textContent = "Invalid credentials";
        return;
      }
      const data = await r.json();
      token = data.token;
      sessionStorage.setItem("admin_token", token);
      showAdmin();
    } catch {
      $("#login-error").textContent = "Couldn't reach the server";
    } finally {
      btn.disabled = false;
    }
  });

  $("#logout-btn").addEventListener("click", async () => {
    try {
      await apiFetch("/api/admin/logout", { method: "POST" });
    } catch {}
    logout();
  });
  $("#reload-btn").addEventListener("click", loadNames);
  $("#names-more").addEventListener("click", () => loadAllNames(true));

  const refilter = () => {
    namesOffset = 0;
    loadAllNames(false);
  };
  $("#names-filter").addEventListener("input", debounce(refilter, 250));
  $("#status-filter").addEventListener("change", refilter);
  $("#sort-select").addEventListener("change", refilter);
});
