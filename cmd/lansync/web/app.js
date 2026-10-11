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

// Folders are grouped by the device(s) they're shared with, e.g. "Phone ⇄ This device".
function folderGroups() {
  const groups = new Map();
  const group = (ids) => {
    const devices = [...ids].sort();
    const key = devices.join(",");
    if (!groups.has(key)) groups.set(key, { key, devices, folders: [], offers: [] });
    return groups.get(key);
  };
  for (const f of state.folders) group(f.devices).folders.push(f);
  state.offers.forEach((o, index) => group([o.deviceID]).offers.push({ ...o, index }));
  const name = (g) => g.devices.map(deviceName).join(" + ");
  return [...groups.values()].sort((a, b) => (!a.devices.length) - (!b.devices.length) || name(a).localeCompare(name(b)));
}

function statusText(s) {
  if (s.state === "idle") return "Up to date";
  if (s.state === "error") return "Error";
  if (s.state === "scanning") return "Scanning…";
  if (s.waiting) return `${s.waiting} is receiving${s.total ? ` ${s.done}/${s.total}` : "…"}`;
  return `Syncing ${s.done}/${s.total}`;
}

function direction(f, remote) {
  if (f.mode === "sendonly") return `This device → ${remote}`;
  if (f.mode === "receiveonly") return `${remote} → This device`;
  return `This device ⇄ ${remote}`;
}

function renderFolder(f, remote) {
  const s = f.status;
  const busy = s.state === "scanning" || s.state === "syncing";
  const progress = s.state === "syncing" && s.total ? `<div class="progress"><div style="width:${(100 * s.done) / s.total}%"></div></div>` : "";
  return `<div class="folder">
      <div class="folder-main">
        <div class="title">${esc(f.label)} <span class="badge">${MODES[f.mode]}</span> <span class="state ${s.state}">${statusText(s)}</span></div>
        ${remote ? `<div class="direction">${esc(direction(f, remote))}</div>` : ""}
        <div class="sub">${esc(f.path)}</div>
        <div class="sub">${s.files} files · ${size(s.bytes)} · last sync ${ago(s.lastSync)}</div>
        ${s.lastError ? `<div class="sub" style="color:var(--err)">${esc(s.lastError)}</div>` : ""}
        ${progress}
      </div>
      <div class="actions">
        <button class="primary" data-sync="${esc(f.id)}" ${busy ? "disabled" : ""}>Sync now</button>
        <button data-edit="${esc(f.id)}">Edit</button>
        <button class="danger" data-remove-folder="${esc(f.id)}">Remove</button>
      </div>
    </div>`;
}

function renderGroup(g) {
  const remote = g.devices.map(deviceName).join(" + ");
  const peers = g.devices.map((id) => {
    const online = state.known.find((d) => d.id === id)?.online;
    return `<span class="peer"><span class="dot ${online ? "on" : ""}" title="${online ? "online" : "offline"}"></span>${esc(deviceName(id))}</span>`;
  }).join(`<span class="plus">+</span>`);
  const head = g.devices.length
    ? `<div class="pair">${peers}<span class="arrow">⇄</span><span class="peer">This device <small>${esc(state.me.deviceName)}</small></span></div>
       ${g.folders.length ? `<button data-sync-group="${esc(g.key)}">⟳ Sync all</button>` : ""}`
    : `<div class="pair"><span class="peer muted">Not shared with any device yet</span></div>`;
  const offers = g.offers.map((o) => `<div class="folder offer">
      <div class="folder-main"><div class="title">${esc(o.deviceName)} wants to share “${esc(o.label)}”</div><div class="sub">Accept it to choose where to keep it on this device.</div></div>
      <div class="actions"><button class="primary" data-accept="${o.index}">Accept</button></div>
    </div>`).join("");
  return `<div class="group"><div class="group-head">${head}</div>${offers}${g.folders.map((f) => renderFolder(f, remote)).join("")}</div>`;
}

function renderFolders() {
  const groups = folderGroups();
  $("#folders").innerHTML = groups.length
    ? groups.map(renderGroup).join("")
    : `<div class="empty">No folders yet. Add one, or accept a folder another device offers.</div>`;
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

async function refresh() {
  try {
    const [me, folders, devices] = await Promise.all([api("GET", "/api/status"), api("GET", "/api/folders"), api("GET", "/api/devices")]);
    Object.assign(state, { me, folders, known: devices.known, discovered: devices.discovered });
    $("#me-name").textContent = me.deviceName;
    $("#me-id").textContent = me.deviceID;
    $("#me-port").textContent = `port ${me.peerPort}`;
    $("#my-addr").innerHTML = me.addresses?.length
      ? `To add this device on another one, enter: ${me.addresses.map((a) => `<code>${esc(a)}</code>`).join(" or ")}`
      : "";
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
    renderFolders();
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

// ---- folder browser (the browser can't reveal real paths, so the core lists folders) ----
async function browse(path) {
  try {
    const r = await api("GET", `/api/browse?path=${encodeURIComponent(path || "")}`);
    $("#browse-error").textContent = "";
    $("#browse-path").value = r.path;
    $("#browse-up").disabled = !r.parent;
    $("#browse-up").dataset.path = r.parent;
    $("#browse-places").innerHTML = r.places.map((p) => `<button class="place ${p.path === r.path ? "current" : ""}" data-browse="${esc(p.path)}">${esc(p.name)}</button>`).join("");
    $("#browse-dirs").innerHTML = r.dirs.length
      ? r.dirs.map((d) => `<li><button data-browse="${esc(d.path)}">📁 ${esc(d.name)}</button></li>`).join("")
      : `<li class="empty">No folders here.</li>`;
  } catch (e) {
    $("#browse-error").textContent = e.message;
  }
}

$("#browse-btn").onclick = () => {
  const current = $("#folder-form").elements.path.value.trim();
  $("#browse-dialog").showModal();
  browse(current);
};
$("#browse-dialog").addEventListener("click", (ev) => {
  const b = ev.target.closest("button[data-browse]");
  if (b) browse(b.dataset.browse);
});
$("#browse-up").onclick = (ev) => browse(ev.currentTarget.dataset.path);
$("#browse-path").onkeydown = (ev) => { if (ev.key === "Enter") { ev.preventDefault(); browse(ev.target.value); } };
$("#browse-cancel").onclick = () => $("#browse-dialog").close();
$("#browse-select").onclick = () => {
  const form = $("#folder-form");
  const path = $("#browse-path").value;
  form.elements.path.value = path;
  if (!form.elements.label.value) form.elements.label.value = path.split(/[\\/]/).filter(Boolean).pop() || "";
  $("#browse-dialog").close();
};

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
    if (d.syncGroup !== undefined) {
      const group = folderGroups().find((g) => g.key === d.syncGroup);
      b.disabled = true;
      await Promise.all(group.folders.map((f) => api("POST", `/api/folders/${encodeURIComponent(f.id)}/sync`)));
      setTimeout(refresh, 300);
    }
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
      const home = state.home || "";
      const path = home && `${home}${state.sep}LanSync${state.sep}${o.label.replace(/[\\/:*?"<>|]/g, "_")}`;
      openFolderDialog({ id: o.folderID, label: o.label, path, devices: [o.deviceID], mode: "sendreceive" }, true);
    }
  } catch (e) {
    alert(e.message);
  }
});

// Home folder and path separator, used to suggest where to keep accepted folders.
api("GET", "/api/browse").then((r) => { state.home = r.path; state.sep = r.sep; }).catch(() => {});

let askedName = false;
refresh().then(() => {
  if (!state.me.nameSet && !askedName) { askedName = true; openNameDialog(true); }
});
refreshOffers();
setInterval(refresh, 2000);
setInterval(refreshOffers, 15000);
