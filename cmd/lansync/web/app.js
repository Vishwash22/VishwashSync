"use strict";

const $ = (sel) => document.querySelector(sel);
const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const MODES = { sendreceive: "Send & receive", sendonly: "Send only", receiveonly: "Receive only" };

let state = { me: {}, folders: [], known: [], discovered: [], offers: [] };

async function api(method, path, body) {
  const res = await fetch(path, { method, headers: { "Content-Type": "application/json" }, body: body && JSON.stringify(body) });
  if (!res.ok) {
    const err = await res.json().catch(() => ({}));
    throw new Error(err.error || res.statusText);
  }
  return res.status === 204 || res.status === 202 ? null : res.json();
}

function size(bytes) {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (bytes >= 1024 && i < units.length - 1) { bytes /= 1024; i++; }
  return `${bytes.toFixed(i ? 1 : 0)} ${units[i]}`;
}

function ago(iso) {
  const t = Date.parse(iso);
  if (!t || t < 0) return "never";
  const s = Math.round((Date.now() - t) / 1000);
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  return new Date(t).toLocaleString();
}

function deviceName(id) {
  return state.known.find((d) => d.id === id)?.name || id.slice(0, 8);
}

function renderFolders() {
  const el = $("#folders");
  if (!state.folders.length) {
    el.innerHTML = `<div class="empty">No folders yet. Add one, or accept a folder another device offers.</div>`;
    return;
  }
  el.innerHTML = state.folders.map((f) => {
    const s = f.status;
    let status = `<span class="state ${s.state}">${s.state === "idle" ? "Up to date" : s.state === "error" ? "Error" : s.state === "scanning" ? "Scanning…" : `Syncing ${s.done}/${s.total}`}</span>`;
    const progress = s.state === "syncing" && s.total ? `<div class="progress"><div style="width:${(100 * s.done) / s.total}%"></div></div>` : "";
    const shared = f.devices.length ? f.devices.map(deviceName).map(esc).join(", ") : "nobody yet";
    return `<div class="card">
      <div>
        <div class="title">${esc(f.label)} <span class="badge">${MODES[f.mode]}</span> ${status}</div>
        <div class="sub">${esc(f.path)}</div>
        <div class="sub">${s.files} files · ${size(s.bytes)} · last sync ${ago(s.lastSync)} · shared with ${shared}</div>
        ${s.lastError ? `<div class="sub" style="color:var(--err)">${esc(s.lastError)}</div>` : ""}
        ${progress}
      </div>
      <div class="actions">
        <button class="primary" data-sync="${esc(f.id)}" ${s.state === "scanning" || s.state === "syncing" ? "disabled" : ""}>Sync now</button>
        <button data-edit="${esc(f.id)}">Edit</button>
        <button class="danger" data-remove-folder="${esc(f.id)}">Remove</button>
      </div>
    </div>`;
  }).join("");
}

function renderDevices() {
  $("#devices").innerHTML = state.known.length ? state.known.map((d) => `<div class="card">
      <div>
        <div class="title"><span class="dot ${d.online ? "on" : ""}"></span>${esc(d.name || "Unnamed")} <small class="sub">${d.online ? "online" : "offline"}</small></div>
        <div class="sub"><code>${esc(d.id)}</code></div>
        <div class="sub">${esc(d.seenAddr || d.addr || "address unknown")}</div>
      </div>
      <div class="actions"><button class="danger" data-remove-device="${esc(d.id)}">Remove</button></div>
    </div>`).join("") : `<div class="empty">No devices added yet.</div>`;

  $("#discovered").innerHTML = state.discovered.length ? state.discovered.map((d) => `<div class="card">
      <div>
        <div class="title"><span class="dot on"></span>${esc(d.name)}</div>
        <div class="sub"><code>${esc(d.id)}</code> · ${esc(d.seenAddr)}</div>
      </div>
      <div class="actions"><button class="primary" data-add-discovered="${esc(d.id)}">Add</button></div>
    </div>`).join("") : `<div class="empty">No other LanSync devices found yet. Make sure they're running on the same network.</div>`;
}

function renderOffers() {
  $("#offers").innerHTML = state.offers.map((o, i) => `<div class="card offer" style="margin-top:1rem">
      <div><div class="title">${esc(o.deviceName)} wants to share “${esc(o.label)}”</div><div class="sub">Folder ID ${esc(o.folderID)}</div></div>
      <div class="actions"><button class="primary" data-accept="${i}">Accept</button></div>
    </div>`).join("");
}

async function refresh() {
  try {
    const [me, folders, devices] = await Promise.all([api("GET", "/api/status"), api("GET", "/api/folders"), api("GET", "/api/devices")]);
    Object.assign(state, { me, folders, known: devices.known, discovered: devices.discovered });
    $("#me-name").textContent = me.deviceName;
    $("#me-id").textContent = me.deviceID;
    $("#me-port").textContent = `port ${me.peerPort}`;
    renderFolders();
    renderDevices();
    const log = await api("GET", "/api/log");
    const pre = $("#log");
    const atBottom = pre.scrollTop + pre.clientHeight >= pre.scrollHeight - 5;
    pre.textContent = log.join("\n");
    if (atBottom) pre.scrollTop = pre.scrollHeight;
  } catch (e) {
    console.error(e);
  }
}

async function refreshOffers() {
  try {
    state.offers = await api("GET", "/api/offers");
    renderOffers();
  } catch (e) { console.error(e); }
}

// ---- folder dialog ----
function openFolderDialog(folder, isNew) {
  const form = $("#folder-form");
  form.reset();
  $("#folder-error").textContent = "";
  $("#folder-title").textContent = isNew ? "Add folder" : `Edit “${folder.label}”`;
  form.elements.label.value = folder.label || "";
  form.elements.path.value = folder.path || "";
  form.elements.id.value = folder.id || "";
  form.elements.id.readOnly = !isNew || !!folder.id;
  form.querySelector(`input[name=mode][value=${folder.mode || "sendreceive"}]`).checked = true;
  const selected = new Set(folder.devices || []);
  $("#folder-devices").innerHTML = state.known.length
    ? state.known.map((d) => `<label class="check"><input type="checkbox" value="${esc(d.id)}" ${selected.has(d.id) ? "checked" : ""}> ${esc(d.name || d.id)}</label>`).join("")
    : `<div class="empty">Add a device first to share this folder.</div>`;
  $("#folder-dialog").showModal();
}

$("#folder-form").addEventListener("submit", async (ev) => {
  if (ev.submitter?.value !== "save") return;
  ev.preventDefault();
  const form = ev.target;
  const body = {
    id: form.elements.id.value, label: form.elements.label.value, path: form.elements.path.value,
    mode: form.querySelector("input[name=mode]:checked").value,
    devices: [...$("#folder-devices").querySelectorAll("input:checked")].map((c) => c.value),
  };
  try {
    await api("POST", "/api/folders", body);
    $("#folder-dialog").close();
    await refresh();
    refreshOffers();
  } catch (e) {
    $("#folder-error").textContent = e.message;
  }
});

// ---- device dialog ----
$("#device-form").addEventListener("submit", async (ev) => {
  if (ev.submitter?.value !== "save") return;
  ev.preventDefault();
  const form = ev.target;
  try {
    await api("POST", "/api/devices", { id: form.elements.id.value, name: form.elements.name.value, addr: form.elements.addr.value });
    $("#device-dialog").close();
    await refresh();
    refreshOffers();
  } catch (e) {
    $("#device-error").textContent = e.message;
  }
});

// ---- buttons ----
$("#add-folder").onclick = () => openFolderDialog({}, true);
$("#add-device").onclick = () => { $("#device-form").reset(); $("#device-error").textContent = ""; $("#device-dialog").showModal(); };
$("#refresh-devices").onclick = async () => { await api("POST", "/api/discover"); setTimeout(refresh, 800); refreshOffers(); };
$("#me-id").onclick = () => navigator.clipboard?.writeText(state.me.deviceID);
// ---- device name ----
function openNameDialog(firstRun) {
  const form = $("#name-form");
  $("#name-title").textContent = firstRun ? "Name this device" : "Rename this device";
  $("#name-cancel").textContent = firstRun ? "Later" : "Cancel";
  $("#name-error").textContent = "";
  form.elements.deviceName.value = state.me.deviceName || "";
  $("#name-dialog").showModal();
  form.elements.deviceName.select();
}
$("#rename").onclick = () => openNameDialog(false);
$("#name-form").addEventListener("submit", async (ev) => {
  if (ev.submitter?.value !== "save") return;
  ev.preventDefault();
  try {
    await api("PUT", "/api/status", { deviceName: ev.target.elements.deviceName.value });
    $("#name-dialog").close();
    refresh();
  } catch (e) {
    $("#name-error").textContent = e.message;
  }
});

document.body.addEventListener("click", async (ev) => {
  const b = ev.target.closest("button");
  if (!b) return;
  const d = b.dataset;
  try {
    if (d.sync) { b.disabled = true; await api("POST", `/api/folders/${encodeURIComponent(d.sync)}/sync`); setTimeout(refresh, 300); }
    if (d.edit) openFolderDialog(state.folders.find((f) => f.id === d.edit), false);
    if (d.removeFolder && confirm("Stop syncing this folder? Files on disk are kept.")) { await api("DELETE", `/api/folders/${encodeURIComponent(d.removeFolder)}`); refresh(); }
    if (d.removeDevice && confirm("Remove this device? It is also removed from all folders.")) { await api("DELETE", `/api/devices/${encodeURIComponent(d.removeDevice)}`); refresh(); }
    if (d.addDiscovered) {
      const dev = state.discovered.find((x) => x.id === d.addDiscovered);
      await api("POST", "/api/devices", { id: dev.id, name: dev.name });
      await refresh();
      refreshOffers();
    }
    if (d.accept) {
      const o = state.offers[+d.accept];
      openFolderDialog({ id: o.folderID, label: o.label, devices: [o.deviceID], mode: "sendreceive" }, true);
    }
  } catch (e) {
    alert(e.message);
  }
});

let askedName = false;
refresh().then(() => {
  if (!state.me.nameSet && !askedName) { askedName = true; openNameDialog(true); }
});
refreshOffers();
setInterval(refresh, 2000);
setInterval(refreshOffers, 15000);
