// admin.js — vanilla JS, no htmx. Login, list, delete.
const $ = (s) => document.querySelector(s);

let token = null;

async function apiFetch(path, opts = {}) {
  opts.headers = Object.assign({}, opts.headers || {}, {
    "Content-Type": "application/json",
  });
  if (token) opts.headers["Authorization"] = "Bearer " + token;
  return fetch(path, opts);
}

async function loadNames() {
  await Promise.all([loadQueue(), loadAllNames()]);
}

async function loadQueue() {
  const r = await apiFetch("/api/admin/names/queue");
  if (r.status === 401) {
    logout();
    return;
  }
  const data = await r.json();
  const tbody = $("#queue-table tbody");
  tbody.innerHTML = "";
  const queue = data.queue || [];
  $("#queue-count").textContent = queue.length
    ? `(${queue.length})`
    : "(empty)";
  for (const n of queue) {
    const tr = document.createElement("tr");
    const when = n.last_flagged
      ? new Date(n.last_flagged).toLocaleString()
      : "—";
    tr.innerHTML = `
      <td>${n.id}</td>
      <td>${escapeHTML(n.text)}</td>
      <td>${n.flag_count}</td>
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
      const r = await apiFetch(
        "/api/admin/names/" + b.dataset.id + "/decision",
        {
          method: "POST",
          body: JSON.stringify({ action }),
        },
      );
      if (r.ok) loadNames();
    });
  });
}

async function loadAllNames() {
  const r = await apiFetch("/api/admin/names");
  if (r.status === 401) {
    logout();
    return;
  }
  const data = await r.json();
  const tbody = $("#names-table tbody");
  tbody.innerHTML = "";
  for (const n of data.names) {
    const tr = document.createElement("tr");
    tr.innerHTML = `
      <td>${n.id}</td>
      <td>${escapeHTML(n.text)}</td>
      <td>${n.status}</td>
      <td>${n.up}</td>
      <td>${n.down}</td>
      <td>${n.score}</td>
      <td><button data-id="${n.id}" class="del">Remove</button></td>
    `;
    tbody.appendChild(tr);
  }
  tbody.querySelectorAll("button.del").forEach((b) => {
    b.addEventListener("click", async () => {
      if (!confirm("Remove name #" + b.dataset.id + "?")) return;
      const r = await apiFetch("/api/admin/names/" + b.dataset.id, {
        method: "DELETE",
      });
      if (r.ok) loadNames();
    });
  });
}

function logout() {
  token = null;
  sessionStorage.removeItem("admin_token");
  $("#login-section").style.display = "";
  $("#admin-section").style.display = "none";
}

function showAdmin() {
  $("#login-section").style.display = "none";
  $("#admin-section").style.display = "";
  loadNames();
  connectLiveUpdates();
}

// ---------- Live updates ----------
// Same WebSocket the public page uses; on any change notice we reload the
// admin tables so moderators always see the freshest state.
let adminSocket = null;
let adminBackoff = 1000;
let adminReloadTimer = null;
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
  adminSocket.addEventListener("open", () => (adminBackoff = 1000));
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

document.addEventListener("DOMContentLoaded", () => {
  token = sessionStorage.getItem("admin_token");
  if (token) showAdmin();

  $("#login-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const fd = new FormData(e.target);
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
  });

  $("#logout-btn").addEventListener("click", async () => {
    await apiFetch("/api/admin/logout", { method: "POST" });
    logout();
  });
  $("#reload-btn").addEventListener("click", loadNames);
});
