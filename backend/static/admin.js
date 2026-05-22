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
