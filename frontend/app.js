// Vectrabot PC app: a client of Vectra Cloud (sign-in, workspaces, desktop
// handoff) and of the workspace host inside each sandbox (bots, threads,
// streaming chat, terminal, preview, desktop frames).

// A page served from this machine may point at a local cloud for testing.
// Anywhere else it always signs in to the real cloud.
const LOCAL_PAGE = ["localhost", "127.0.0.1"].includes(location.hostname);
const CLOUD = (LOCAL_PAGE && new URLSearchParams(location.search).get("cloud")) || "https://infra.xibeai.in";
const $ = (s, root = document) => root.querySelector(s);
const $$ = (s, root = document) => [...root.querySelectorAll(s)];
const enc = encodeURIComponent;
const store = sessionStorage; // the sign-in lasts for this window only
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const state = {
  token: store.getItem("vb.token") || "", email: store.getItem("vb.email") || "",
  ws: null, tab: "chat",
  bots: [], botId: "", threadId: "",
  messages: new Map(), more: new Map(), sessions: new Map(),
  routines: [], runs: [], models: [], query: "",
};
const icon = (name, cls = "") => `<svg class="i ${cls}"><use href="#i-${name}"/></svg>`;

// Open http(s) URLs in the system browser when running inside the Tauri
// desktop client. In a regular browser, fall back to window.open.
const nativeOpen = window.open.bind(window);
function isHttpUrl(value) {
  try { const u = new URL(String(value || ""), location.href); return u.protocol === "http:" || u.protocol === "https:"; }
  catch { return false; }
}
async function openExternal(url) {
  const href = String(url || "").trim();
  if (!isHttpUrl(href)) return null;
  const absolute = new URL(href, location.href).href;
  const openUrl = window.__TAURI__?.opener?.openUrl;
  if (typeof openUrl === "function") {
    await openUrl(absolute);
    return true;
  }
  return nativeOpen(absolute, "_blank", "noopener,noreferrer");
}
if (window.__TAURI__) {
  window.open = (url, target, features) => {
    if (isHttpUrl(url)) { openExternal(url); return null; }
    return nativeOpen(url, target, features);
  };
}
document.addEventListener("click", (event) => {
  if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
  const a = event.target?.closest?.("a[href]");
  if (!a || a.hasAttribute("download")) return;
  const href = a.getAttribute("href");
  if (!href || href.startsWith("#") || href.startsWith("javascript:")) return;
  if (!isHttpUrl(href)) return;
  if (!window.__TAURI__ && a.target !== "_blank") return;
  event.preventDefault();
  openExternal(href);
}, true);

function h(tag, attrs = {}, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v == null || v === false) continue;
    if (k === "class") el.className = v;
    else if (k === "html") el.innerHTML = v;
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else if (k === "style" && typeof v === "object") for (const [p, x] of Object.entries(v)) { if (p.startsWith("--")) el.style.setProperty(p, x); else el.style[p] = x; }
    else el.setAttribute(k, v === true ? "" : v);
  }
  for (const kid of kids.flat()) if (kid != null && kid !== false) el.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
  return el;
}
function escapeHtml(s) { return String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c])); }
function toast(text) {
  const t = h("div", { class: "toast", role: "status" }, text); document.body.append(t);
  setTimeout(() => { t.style.transition = "opacity 200ms"; t.style.opacity = "0"; setTimeout(() => t.remove(), 220); }, 1800);
}
function timeAgo(ms) {
  if (!ms) return "";
  const s = (Date.now() - ms) / 1000;
  if (s < 60) return "now";
  if (s < 3600) return Math.floor(s / 60) + "m";
  if (s < 86400) return Math.floor(s / 3600) + "h";
  if (s < 604800) return Math.floor(s / 86400) + "d";
  return new Date(ms).toLocaleDateString(undefined, { month: "short", day: "numeric" });
}
const clock = (ms) => new Date(ms).toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
const initial = (name) => (String(name || "?").trim()[0] || "?").toUpperCase();
// ---------- bot faces ----------
// A bot's face is a generated pattern (a 15-bit 5x5 mirrored grid the host
// derives from the bot's glyph seed), its initial, an emoji or an uploaded
// image, on its colour and cropped to its shape. Same rules as the phone app.
function glyphOf(seed) {
  let x = 0x811c9dc5;
  for (const b of new TextEncoder().encode(String(seed))) { x ^= b; x = Math.imul(x, 0x01000193) >>> 0; }
  let g = x & 0x7fff;
  const on = (v) => { let n = 0; for (; v; v &= v - 1) n++; return n; };
  while (on(g) < 4 || on(g) > 11) g = (g * 31 + 0x2b5d) & 0x7fff;
  return g;
}
function glyphSvg(bits) {
  let cells = "";
  for (let r = 0; r < 5; r++) for (let c = 0; c < 3; c++) {
    if (!(bits >> (r * 3 + c) & 1)) continue;
    for (const x of c === 2 ? [2] : [c, 4 - c]) cells += `<rect x="${6 + x * 4.4}" y="${6 + r * 4.4}" width="4" height="4" rx="1.1"/>`;
  }
  return `<svg class="glyph" viewBox="0 0 34 34" aria-hidden="true"><defs><linearGradient id="gg" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#fff" stop-opacity=".18"/><stop offset="1" stop-color="#000" stop-opacity=".12"/></linearGradient></defs><rect width="34" height="34" fill="var(--c)"/><rect width="34" height="34" fill="url(#gg)"/><g fill="#fff" fill-opacity=".93">${cells}</g></svg>`;
}
const faceCache = new Map(); // host avatar path -> Promise<object URL>
function faceSrc(path) {
  const key = state.ws?.id + path;
  if (!faceCache.has(key)) {
    const p = fetch(state.ws.host + path, { headers: { authorization: "Bearer " + state.ws.session } })
      .then((r) => { if (!r.ok) throw new Error("avatar " + r.status); return r.blob(); })
      .then((b) => URL.createObjectURL(b));
    p.catch(() => faceCache.delete(key));
    faceCache.set(key, p);
  }
  return faceCache.get(key);
}
function avatar(el, bot, busy) {
  el.classList.remove("rounded", "square", "image", "emoji");
  el.style.setProperty("--c", bot?.color || "#3a3a40");
  if (bot?.avatarShape === "rounded" || bot?.avatarShape === "square") el.classList.add(bot.avatarShape);
  el.replaceChildren();
  const kind = bot?.avatarKind || "generated";
  const glyph = () => { if (typeof bot?.glyph === "number") el.innerHTML = glyphSvg(bot.glyph); else el.textContent = initial(bot?.name); };
  if (kind === "image" && (bot.avatarUrl || bot.avatarPreview)) {
    el.classList.add("image");
    const img = h("img", { alt: "", draggable: "false" }); el.append(img);
    if (bot.avatarPreview) img.src = bot.avatarPreview;
    else faceSrc(bot.avatarUrl).then((u) => { img.src = u; }).catch(() => { el.classList.remove("image"); glyph(); });
  } else if (kind === "emoji" && bot.emoji) { el.classList.add("emoji"); el.textContent = bot.emoji; }
  else if (kind === "initial") el.textContent = initial(bot?.name);
  else glyph();
  el.title = bot?.name || "";
  if (busy) el.append(h("span", { class: "busy", title: "Working" }));
}
// The order every client lists bots in: main, pinned, then latest activity.
function sortBots(list) {
  const t = (b) => Math.max(b.lastAt || 0, b.createdAt || 0);
  return [...list].sort((a, b) => (b.main === true) - (a.main === true) || (b.pinned === true) - (a.pinned === true) || t(b) - t(a) || (a.createdAt || 0) - (b.createdAt || 0));
}

// ---------- cloud + host calls ----------
async function cloud(path, options = {}) {
  const headers = { accept: "application/json", ...(options.body ? { "content-type": "application/json" } : {}), ...(state.token ? { authorization: "Bearer " + state.token } : {}) };
  const res = await fetch(CLOUD + path, { ...options, headers });
  const body = await res.json().catch(() => ({}));
  if (res.status === 401 && state.token) { signOut("Your sign-in expired. Sign in again."); throw new Error("Signed out"); }
  if (!res.ok) throw new Error(body.error || `Cloud request failed (${res.status})`);
  return body;
}
function providerSave(path) { return path === "/api/providers/save" || path === "/api/providers/image/save"; }
async function host(path, options = {}, retried = false, attempt = 0) {
  if (!state.ws) throw new Error("No workspace open");
  const raw = options.raw;
  const headers = { accept: "application/json", authorization: "Bearer " + state.ws.session, ...(options.body && !raw ? { "content-type": "application/json" } : {}) };
  const res = await fetch(state.ws.host + path, { ...options, headers });
  if (res.status === 401 && !retried) { await reattach(); return host(path, options, true, attempt); }
  if (providerSave(path) && (res.status === 502 || res.status === 503) && attempt < 2) {
    await new Promise((resolve) => setTimeout(resolve, 400 * (attempt + 1)));
    return host(path, options, retried, attempt + 1);
  }
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body.error || `Sandbox request failed (${res.status})`);
  return body;
}
const api = (path, method = "GET", body) => host(path, { method, ...(body !== undefined ? { body: JSON.stringify(body) } : {}) });
const inst = (p) => `/api/instances/${enc(state.ws.id)}${p}`;
function wsError(message) { $("#ws-error").textContent = message || ""; $("#ws-error").title = message || ""; $("#ws-error-wrap").hidden = !message; }
$("#ws-error-close").onclick = () => wsError("");
function setStatus(dot, label, text, kind) { label.textContent = text; dot.className = "dot" + (kind ? " " + kind : ""); }
const setWs = (text, kind) => setStatus($("#ws-dot"), $("#ws-state"), text, kind);

// ---------- sign in / list ----------
function show(screen) { for (const id of ["login", "workspaces", "workspace"]) $("#" + id).hidden = id !== screen; }
function signOut(message = "") {
  closeWorkspace(); state.token = ""; store.removeItem("vb.token"); store.removeItem("vb.email");
  $("#login-error").textContent = message; show("login");
}
$("#login-form").onsubmit = async (e) => {
  e.preventDefault(); $("#login-error").textContent = "";
  const email = $("#email").value.trim(), password = $("#password").value;
  if (!email || !password) { $("#login-error").textContent = "Enter your email and password."; return; }
  const btn = $("#signin"); btn.disabled = true; btn.classList.add("loading");
  try {
    const signed = await cloud("/api/auth/login", { method: "POST", body: JSON.stringify({ email, password }) });
    if (!signed.token) throw new Error("The cloud did not return a session");
    state.token = signed.token; state.email = email; store.setItem("vb.token", signed.token); store.setItem("vb.email", email); $("#password").value = "";
    await loadList();
  } catch (err) { $("#login-error").textContent = err.message; }
  finally { btn.disabled = false; btn.classList.remove("loading"); }
};
$("#signout").onclick = () => signOut();
$("#refresh").onclick = () => loadList();
async function loadList() {
  show("workspaces"); $("#list-error").textContent = ""; $("#list-email").textContent = state.email;
  const box = $("#list");
  box.replaceChildren(...[0, 1].map(() => h("div", { class: "ws" }, h("div", { class: "skeleton", style: { width: "40%", height: "14px" } }))));
  try {
    const { instances = [] } = await cloud("/api/instances");
    box.replaceChildren();
    if (!instances.length) { box.append(h("div", { class: "empty" }, h("span", { html: icon("grid") }), h("strong", {}, "No workspaces yet"), h("span", { html: `Create one on <a href="${escapeHtml(CLOUD)}" target="_blank" rel="noreferrer">${escapeHtml(CLOUD.replace(/^https?:\/\//, ""))}</a>, then refresh.` }))); return; }
    for (const item of instances) {
      const status = item.status || "unknown";
      const kind = status === "running" ? "ok" : status === "paused" ? "warn" : status === "error" ? "bad" : "";
      box.append(h("div", { class: "ws" },
        h("span", { class: "mark", html: icon("logo") }),
        h("div", { class: "grow" }, h("div", { class: "name" }, item.name || "Workspace"), h("div", { class: "sub" }, item.runtime || "supercharge")),
        h("span", { class: "tag " + kind }, status),
        h("button", { class: "btn sm", type: "button", onclick: () => openAndLoad(item) }, "Open")));
    }
  } catch (err) { box.replaceChildren(); $("#list-error").textContent = err.message; }
}

// ---------- open / attach ----------
async function attach(id) {
  const a = await cloud(`/api/instances/${enc(id)}/attach`, { method: "POST", body: JSON.stringify({ device: "pc" }) });
  if (!a.url || !a.token) throw new Error("The sandbox did not return a session");
  return { host: a.url.replace(/\/$/, ""), session: a.token };
}
let reattaching = null;
async function reattach() {
  if (!state.ws) return;
  reattaching ||= attach(state.ws.id).then((s) => Object.assign(state.ws, s)).finally(() => { reattaching = null; });
  return reattaching;
}
async function openWorkspace(item) {
  show("workspace"); wsError(""); $("#ws-name").textContent = item.name || "Workspace";
  $("#me-email").textContent = state.email || "Signed in"; $("#me-avatar").textContent = initial(state.email);
  setWs(item.status === "paused" ? "Waking…" : "Connecting…", "warn pulse");
  state.ws = { id: item.id, name: item.name, host: "", session: "" };
  state.bots = []; state.messages.clear(); state.sessions.clear(); state.more.clear();
  renderBots(true); renderChat();
  try {
    Object.assign(state.ws, await attach(item.id));
    setWs("Running", "ok");
    await loadBots();
    connectEvents();
    selectTab(state.tab === "routines" ? "chat" : state.tab);
  } catch (err) { setWs("Unavailable", "bad"); wsError(err.message); }
}
function closeWorkspace() {
  stopLive(); endControl({ silent: true }); closeTerminal(); closeEvents();
  state.ws = null;
}
$("#back").onclick = () => { closeWorkspace(); loadList(); };
$$("[data-tab]").forEach((b) => (b.onclick = () => selectTab(b.dataset.tab)));
function selectTab(tab) {
  state.tab = tab;
  $$("[data-tab]").forEach((b) => b.classList.toggle("on", b.dataset.tab === tab));
  for (const v of ["chat", "desktop", "preview", "terminal", "routines"]) $("#" + v).hidden = v !== tab;
  $("#share").hidden = tab !== "chat";
  if (!state.ws?.session) return;
  if (tab === "desktop") startDesktop(); else stopLive();
  if (tab === "terminal") openTerminal();
  if (tab === "preview") refreshPorts();
  if (tab === "routines") loadRoutines();
  if (tab === "chat") $("#chat-prompt").focus();
  renderComputer();
}

// ---------- live events ----------
const events = { source: null, cursor: "", retry: 0, timer: null };
function connectEvents() {
  closeEvents();
  if (!state.ws?.session) return;
  const url = new URL(state.ws.host + "/api/events");
  url.searchParams.set("token", state.ws.session);
  if (events.cursor) url.searchParams.set("since", events.cursor);
  const source = new EventSource(url); events.source = source;
  source.onmessage = (e) => {
    let f; try { f = JSON.parse(e.data); } catch { return; }
    if (e.lastEventId) events.cursor = e.lastEventId;
    onFrame(f);
  };
  source.onopen = () => { events.retry = 0; };
  source.onerror = () => {
    if (events.source !== source) return;
    source.close(); events.source = null;
    const wait = Math.min(15000, 500 * 2 ** events.retry++);
    events.timer = setTimeout(async () => { try { await reattach(); } catch { /* retry later */ } connectEvents(); }, wait);
  };
}
function closeEvents() { clearTimeout(events.timer); events.source?.close(); events.source = null; }
function onFrame(f) {
  switch (f.kind) {
    case "hello":
      if (f.cursor) events.cursor = f.cursor;
      if (!f.resumed) { loadBots().catch(() => {}); if (state.threadId) loadThread(state.threadId, { quiet: true }); }
      break;
    case "message": upsertMessage(f.threadId, f.message); touchBotPreview(f.botId, f.threadId, f.message); break;
    case "session": state.sessions.set(f.threadId, f.session); if (f.threadId === state.threadId) renderComposer(); markBusy(f.botId, f.threadId, f.session.busy); break;
    case "turn": {
      const s = state.sessions.get(f.threadId) || {}; s.busy = f.busy; state.sessions.set(f.threadId, s);
      markBusy(f.botId, f.threadId, f.busy);
      if (f.threadId === state.threadId) { renderComposer(); renderWorking(); if (!f.busy) renderLibrary(); }
      break;
    }
    case "bot": mergeBot(f.bot); break;
    case "bot.deleted":
      state.bots = state.bots.filter((b) => b.id !== f.botId);
      if (state.botId === f.botId) selectBot((state.bots.find((b) => b.main) || state.bots[0])?.id); else renderBots();
      break;
    case "thread": {
      const bot = state.bots.find((b) => b.id === f.botId);
      if (bot) { const t = bot.tasks.find((x) => x.threadId === f.threadId); if (t) Object.assign(t, f.thread); else bot.tasks.unshift(f.thread); }
      if (f.botId === state.botId) { renderThreadTitle(); renderThreads(); }
      break;
    }
    case "routine": case "routine.deleted": if (state.tab === "routines" || $("#info").offsetWidth) loadRoutines(); break;
  }
}

// ---------- bots ----------
async function loadBots() {
  const { bots = [] } = await api("/api/bots");
  state.bots = bots;
  const keep = bots.find((b) => b.id === state.botId) ? state.botId : (bots.find((b) => b.id === store.getItem("vb.bot." + state.ws.id))?.id || bots.find((b) => b.main)?.id || bots[0]?.id);
  await selectBot(keep, { keepThread: keep === state.botId });
}
function mergeBot(bot) {
  const i = state.bots.findIndex((b) => b.id === bot.id);
  if (i >= 0) state.bots[i] = bot; else state.bots.push(bot);
  renderBots();
  if (bot.id === state.botId) { renderBotHeader(); renderThreads(); renderSettings(); }
}
function touchBotPreview(botId, threadId, m) {
  const bot = state.bots.find((b) => b.id === botId); if (!bot) return;
  if ((m.kind === "text" || m.kind === "notice") && m.text) { bot.preview = (m.role === "user" ? "You: " : "") + m.text.replace(/\s+/g, " ").slice(0, 80); bot.lastAt = m.at; }
  const t = bot.tasks?.find((x) => x.threadId === threadId); if (t) t.updatedAt = m.at;
  renderBots();
}
function markBusy(botId, threadId, busy) {
  const bot = state.bots.find((b) => b.id === botId); if (!bot) return;
  const t = bot.tasks?.find((x) => x.threadId === threadId); if (t) t.busy = !!busy;
  bot.busy = !!bot.tasks?.some((x) => x.busy);
  renderBots(); if (botId === state.botId) renderThreads();
}
const currentBot = () => state.bots.find((b) => b.id === state.botId);
function renderBots(loading = false) {
  const list = $("#bot-list"); list.replaceChildren();
  if (loading) {
    for (let i = 0; i < 3; i++) list.append(h("div", { class: "bot-row skeleton-row" }, h("span", { class: "avatar skeleton" }), h("div", { class: "meta" }, h("div", { class: "bar skeleton", style: { width: "50%" } }), h("div", { class: "bar skeleton", style: { width: "80%", marginTop: "6px" } }))));
    return;
  }
  const q = state.query.toLowerCase();
  const bots = sortBots(state.bots).filter((b) => !q || b.name.toLowerCase().includes(q) || (b.preview || "").toLowerCase().includes(q) || (b.title || "").toLowerCase().includes(q));
  $("#bot-count").textContent = state.bots.length || "";
  for (const b of bots) {
    const av = h("span", { class: "avatar" }); avatar(av, b, b.busy);
    list.append(h("button", { class: "bot-row" + (b.id === state.botId ? " on" : ""), type: "button", "data-bot": b.id, onclick: () => selectBot(b.id) }, av,
      h("div", { class: "meta" },
        h("div", { class: "top" }, h("span", { class: "name" }, b.name), b.main ? h("span", { class: "tag", title: "Your main bot" }, "Main") : null, b.pinned && !b.main ? h("span", { html: icon("pin", "pin") }) : null, h("span", { class: "time" }, timeAgo(b.lastAt))),
        h("div", { class: "preview" + (b.busy ? " working" : "") }, b.busy ? "Working…" : (b.preview || b.title || "No messages yet")))));
  }
  if (!bots.length && q) list.append(h("div", { class: "empty" }, "No bots match “" + state.query + "”."));
}
$("#search-toggle").onclick = () => { const w = $("#search-wrap"); w.hidden = !w.hidden; if (!w.hidden) $("#bot-search").focus(); else { state.query = ""; $("#bot-search").value = ""; renderBots(); } };
$("#bot-search").oninput = (e) => { state.query = e.target.value.trim(); renderBots(); };
$("#bot-search").onkeydown = (e) => { if (e.key === "Escape") $("#search-toggle").click(); };
async function selectBot(id, { keepThread = false } = {}) {
  const bot = state.bots.find((b) => b.id === id); if (!bot) return;
  state.botId = id; store.setItem("vb.bot." + state.ws.id, id);
  const thread = keepThread && bot.tasks.some((t) => t.threadId === state.threadId) ? state.threadId : bot.threadId || bot.tasks[0]?.threadId;
  renderBots(); renderBotHeader(); renderSettings(); loadFiles(); renderRoutinesCard();
  document.querySelector(".app").classList.remove("side-open");
  if (state.tab !== "chat" && state.tab !== "routines") { /* keep the tool view */ } else if (state.tab === "routines") selectTab("chat");
  await openThread(thread);
}
function renderBotHeader() {
  const bot = currentBot(); if (!bot) return;
  avatar($("#top-avatar"), bot); $("#top-bot").textContent = bot.name;
  avatar($("#info-avatar"), bot); $("#info-name").textContent = bot.name; $("#info-title").textContent = bot.title || (bot.main ? "Main bot" : "Bot");
  $("#info-desc").textContent = bot.description || ""; $("#info-desc").hidden = !bot.description;
  renderThreadTitle();
}
function renderThreadTitle() {
  const t = currentBot()?.tasks.find((x) => x.threadId === state.threadId);
  $("#thread-title").textContent = t?.title || "New thread";
}

// ---------- threads ----------
async function openThread(threadId) {
  if (!threadId) return;
  state.threadId = threadId; renderThreadTitle(); renderThreads(); renderChat(); renderComposer(); renderLibrary();
  if (!state.messages.has(threadId)) await loadThread(threadId);
  if (!state.sessions.get(threadId)?.open) api(`/api/threads/${enc(threadId)}/open`, "POST", {}).then((s) => { state.sessions.set(threadId, s); if (threadId === state.threadId) renderComposer(); }).catch(() => {});
}
async function loadThread(threadId, { quiet = false } = {}) {
  try {
    const page = await api(`/api/threads/${enc(threadId)}/messages?limit=150`);
    state.messages.set(threadId, page.messages || []); state.more.set(threadId, !!page.hasMore);
    if (page.session) state.sessions.set(threadId, page.session);
    if (threadId === state.threadId) { renderChat(); renderComposer(); renderLibrary(); }
  } catch (err) { if (!quiet) wsError(err.message); }
}
async function loadOlder() {
  const list = state.messages.get(state.threadId) || []; if (!list.length) return;
  const page = await api(`/api/threads/${enc(state.threadId)}/messages?limit=150&before=${enc(list[0].id)}`);
  state.messages.set(state.threadId, [...(page.messages || []), ...list]); state.more.set(state.threadId, !!page.hasMore);
  const scroller = $("#chat-scroll"), before = scroller.scrollHeight;
  renderChat({ keepScroll: true }); scroller.scrollTop = scroller.scrollHeight - before;
}
async function newThread() {
  const bot = currentBot(); if (!bot) return;
  try { const out = await api(`/api/bots/${enc(bot.id)}/tasks`, "POST", {}); mergeBot(out.bot); state.messages.set(out.threadId, []); await openThread(out.threadId); $("#chat-prompt").focus(); }
  catch (err) { wsError(err.message); }
}
$("#info-new-thread").onclick = newThread;
function renderThreads() {
  const bot = currentBot(); const list = $("#thread-list"); list.replaceChildren(); if (!bot) return;
  const threads = bot.tasks.filter((t) => !t.archivedAt);
  $("#thread-count").textContent = threads.length;
  for (const t of threads.slice(0, 12)) list.append(threadItem(bot, t));
  if (!threads.length) list.append(h("div", { class: "empty" }, "No threads"));
  const menu = $("#thread-menu"); menu.replaceChildren(
    h("button", { type: "button", onclick: () => { menu.hidden = true; newThread(); } }, h("span", { html: icon("plus") }), "New thread", h("span", { class: "sub" }, "")),
    h("hr"),
    ...threads.map((t) => h("button", { type: "button", class: t.threadId === state.threadId ? "on" : "", onclick: () => { menu.hidden = true; openThread(t.threadId); } },
      t.busy ? h("span", { class: "spinner" }) : h("span", { html: icon(t.pinned ? "pin" : "chat", "sm") }),
      h("span", { class: "grow", style: { overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" } }, t.title || "New thread"), h("span", { class: "sub" }, timeAgo(t.updatedAt)))));
}
function threadItem(bot, t) {
  const rename = async (e) => { e.stopPropagation(); const title = window.prompt("Rename thread", t.title || ""); if (title == null) return; await api(`/api/bots/${enc(bot.id)}/tasks/${enc(t.threadId)}`, "PATCH", { title }).catch((err) => wsError(err.message)); t.title = title; renderThreads(); renderThreadTitle(); };
  const pin = async (e) => { e.stopPropagation(); await api(`/api/bots/${enc(bot.id)}/tasks/${enc(t.threadId)}`, "PATCH", { pinned: !t.pinned }).catch((err) => wsError(err.message)); };
  const del = async (e) => { e.stopPropagation(); if (!confirm(`Delete “${t.title || "this thread"}”? Its messages are removed.`)) return; try { await api(`/api/bots/${enc(bot.id)}/tasks/${enc(t.threadId)}`, "DELETE"); state.messages.delete(t.threadId); } catch (err) { wsError(err.message); } };
  return h("button", { class: "li" + (t.threadId === state.threadId ? " on" : ""), type: "button", onclick: () => openThread(t.threadId) },
    t.busy ? h("span", { class: "spinner" }) : h("span", { class: "faint", html: icon(t.pinned ? "pin" : "chat", "sm") }),
    h("span", { class: "t" }, t.title || "New thread"),
    h("span", { class: "s" }, timeAgo(t.updatedAt)),
    h("span", { class: "acts" },
      h("span", { class: "btn ghost icon sm", title: "Rename", onclick: rename, html: icon("edit", "sm") }),
      h("span", { class: "btn ghost icon sm", title: t.pinned ? "Unpin" : "Pin", onclick: pin, html: icon("pin", "sm") }),
      h("span", { class: "btn ghost icon sm", title: "Delete", onclick: del, html: icon("trash", "sm") })));
}
$("#thread-btn").onclick = (e) => { e.stopPropagation(); const m = $("#thread-menu"); m.hidden = !m.hidden; };
document.addEventListener("click", (e) => {
  for (const id of ["thread-menu", "profile-menu", "mode-menu"]) { const m = $("#" + id); if (!m.hidden && !m.contains(e.target)) m.hidden = true; }
});

// ---------- markdown ----------
function inline(s) {
  return s
    .replace(/`([^`\n]+)`/g, (_, c) => `<code>${c}</code>`)
    .replace(/\*\*([^*\n]+)\*\*/g, "<strong>$1</strong>")
    .replace(/(^|[^*\w])\*([^*\n]+)\*(?!\w)/g, "$1<em>$2</em>")
    .replace(/\[([^\]\n]+)\]\((https?:\/\/[^\s)]+)\)/g, '<a href="$2" target="_blank" rel="noreferrer noopener">$1</a>')
    .replace(/(^|[\s(])(https?:\/\/[^\s<)]+)/g, '$1<a href="$2" target="_blank" rel="noreferrer noopener">$2</a>');
}
function markdown(src, { streaming = false } = {}) {
  const out = []; const parts = String(src || "").split(/^```/m);
  parts.forEach((part, i) => {
    if (i % 2) {
      const nl = part.indexOf("\n"); const lang = nl >= 0 ? part.slice(0, nl).trim() : ""; const code = nl >= 0 ? part.slice(nl + 1) : "";
      out.push(`<div class="code"><div class="code-head"><span class="grow">${escapeHtml(lang || "text")}</span><button class="btn ghost icon sm" type="button" data-copy title="Copy">${icon("copy", "sm")}</button></div><pre><code>${escapeHtml(code.replace(/\n$/, ""))}</code></pre></div>`);
      return;
    }
    const lines = escapeHtml(part.replace(/^[^\n]*\n?/, i ? "" : (m) => m)).split("\n");
    let para = [], list = null;
    const flush = () => { if (para.length) { out.push(`<p>${inline(para.join("<br>"))}</p>`); para = []; } if (list) { out.push(`<${list.tag}>${list.items.map((x) => `<li>${inline(x)}</li>`).join("")}</${list.tag}>`); list = null; } };
    for (let j = 0; j < lines.length; j++) {
      const line = lines[j];
      let m;
      if (!line.trim()) { flush(); continue; }
      if ((m = line.match(/^(#{1,4})\s+(.*)$/))) { flush(); const n = Math.min(3, m[1].length); out.push(`<h${n}>${inline(m[2])}</h${n}>`); continue; }
      if (/^(-{3,}|\*{3,})$/.test(line.trim())) { flush(); out.push("<hr>"); continue; }
      if ((m = line.match(/^&gt;\s?(.*)$/))) { flush(); out.push(`<blockquote>${inline(m[1])}</blockquote>`); continue; }
      if (line.includes("|") && lines[j + 1] && /^\s*\|?\s*:?-{2,}/.test(lines[j + 1])) {
        flush(); const row = (l) => l.trim().replace(/^\||\|$/g, "").split("|").map((c) => c.trim());
        const head = row(line); j++; const body = [];
        while (lines[j + 1] && lines[j + 1].includes("|")) body.push(row(lines[++j]));
        out.push(`<table><thead><tr>${head.map((c) => `<th>${inline(c)}</th>`).join("")}</tr></thead><tbody>${body.map((r) => `<tr>${r.map((c) => `<td>${inline(c)}</td>`).join("")}</tr>`).join("")}</tbody></table>`);
        continue;
      }
      if ((m = line.match(/^\s*([-*+]|\d+[.)])\s+(.*)$/))) {
        const tag = /\d/.test(m[1]) ? "ol" : "ul";
        if (para.length) { out.push(`<p>${inline(para.join("<br>"))}</p>`); para = []; }
        if (!list || list.tag !== tag) { if (list) flush(); list = { tag, items: [] }; }
        list.items.push(m[2].replace(/^\[ \]\s/, "☐ ").replace(/^\[x\]\s/i, "☑ ")); continue;
      }
      if (list && /^\s{2,}\S/.test(line)) { list.items[list.items.length - 1] += " " + line.trim(); continue; }
      if (list) flush();
      para.push(line);
    }
    flush();
  });
  let html = out.join("");
  if (streaming) html = html.replace(/(<\/(p|li|code)>)(?![\s\S]*<\/(p|li|code)>)/, '<span class="caret"></span>$1');
  return html;
}
document.addEventListener("click", (e) => {
  const b = e.target.closest("[data-copy]"); if (!b) return;
  navigator.clipboard?.writeText(b.closest(".code").querySelector("code").textContent); toast("Copied");
});

// ---------- diff ----------
function lineDiff(a, b) {
  const A = a ? a.split("\n") : [], B = b ? b.split("\n") : [];
  if (A.length * B.length > 250000) return [...A.map((t) => ["d", t]), ...B.map((t) => ["a", t])];
  const n = A.length, m = B.length, dp = Array.from({ length: n + 1 }, () => new Uint32Array(m + 1));
  for (let i = n - 1; i >= 0; i--) for (let j = m - 1; j >= 0; j--) dp[i][j] = A[i] === B[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
  const out = []; let i = 0, j = 0;
  while (i < n && j < m) { if (A[i] === B[j]) { out.push([" ", A[i]]); i++; j++; } else if (dp[i + 1][j] >= dp[i][j + 1]) out.push(["d", A[i++]]); else out.push(["a", B[j++]]); }
  while (i < n) out.push(["d", A[i++]]); while (j < m) out.push(["a", B[j++]]);
  return out;
}
function diffView(d) {
  const lines = lineDiff(d.oldText || "", d.newText || "");
  const add = lines.filter((l) => l[0] === "a").length, del = lines.filter((l) => l[0] === "d").length;
  const body = h("div", { class: "diff-lines" });
  // Show changed lines with three lines of context.
  const keep = lines.map((l, i) => l[0] !== " " || lines.slice(Math.max(0, i - 3), i + 4).some((x) => x[0] !== " "));
  let gap = false;
  lines.forEach((l, i) => {
    if (!keep[i]) { if (!gap) body.append(h("div", { class: "gap" }, "⋯")); gap = true; return; }
    gap = false; body.append(h("div", { class: l[0] === "a" ? "a" : l[0] === "d" ? "d" : "" }, (l[0] === " " ? "  " : l[0] === "a" ? "+ " : "- ") + l[1]));
  });
  return h("div", { class: "diff" }, h("div", { class: "diff-head" }, h("span", { html: icon("file", "sm") }), h("span", {}, shortPath(d.path)),
    h("span", { class: "stat" }, h("span", { class: "plus" }, "+" + add), " ", h("span", { class: "minus" }, "−" + del))), body);
}

// ---------- chat ----------
const toolIcons = { read: "file", edit: "fileedit", delete: "trash", move: "fileedit", search: "search", execute: "terminal", think: "brain", fetch: "globe", switch_mode: "map", other: "tool" };
function upsertMessage(threadId, m) {
  const list = state.messages.get(threadId); if (!list) return;
  const i = list.findIndex((x) => x.id === m.id);
  if (i >= 0) list[i] = m; else list.push(m);
  if (threadId !== state.threadId) return;
  const el = document.getElementById("m-" + m.id);
  const scroller = $("#chat-scroll"); const atBottom = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 80;
  if (el) el.replaceWith(renderMessage(m, list, list.indexOf(m)));
  else { $("#chat-empty")?.remove(); const label = needsLabel(list, list.length - 1) ? botLabel(m) : null; if (label) $("#chat-log").insertBefore(label, $("#working")); $("#chat-log").insertBefore(renderMessage(m, list, list.length - 1), $("#working")); }
  renderWorking();
  if (atBottom || m.role === "user") scroller.scrollTop = scroller.scrollHeight;
  if (m.kind === "changes") renderLibrary();
}
function needsLabel(list, i) { const m = list[i]; if (m.role !== "bot" || m.kind === "notice") return false; const prev = list[i - 1]; return !prev || prev.role === "user"; }
function botLabel(m) {
  const bot = currentBot(); const av = h("span", { class: "avatar xs" }); avatar(av, bot);
  return h("div", { class: "label" }, av, h("strong", {}, bot?.name || "Bot"), h("span", {}, clock(m.at)));
}
function renderChat({ keepScroll = false } = {}) {
  const log = $("#chat-log"); log.replaceChildren();
  const list = state.messages.get(state.threadId);
  const bot = currentBot();
  if (!list) { for (let i = 0; i < 3; i++) log.append(h("div", { class: "skeleton", style: { height: i === 1 ? "56px" : "36px", width: i === 1 ? "70%" : "45%", alignSelf: i === 2 ? "flex-end" : "flex-start", borderRadius: "16px", margin: "8px 0" } })); return; }
  if (state.more.get(state.threadId)) log.append(h("button", { class: "btn sm ghost load-more", type: "button", onclick: loadOlder }, "Load earlier messages"));
  if (!list.length && bot) {
    const av = h("span", { class: "avatar xl" }); avatar(av, bot);
    const tips = ["Summarize this workspace", "Start a Next.js app on port 3000", "Plan a refactor before changing code"];
    log.append(h("div", { class: "chat-empty", id: "chat-empty" }, av, h("h2", {}, `Chat with ${bot.name}`),
      h("p", {}, bot.title || bot.description || "Runs Supercharge inside this workspace: code, terminal, browser and desktop."),
      h("div", { class: "suggestions" }, tips.map((t) => h("button", { type: "button", onclick: () => { $("#chat-prompt").value = t; updateComposer(); $("#chat-prompt").focus(); } }, t)))));
  }
  list.forEach((m, i) => { if (needsLabel(list, i)) log.append(botLabel(m)); log.append(renderMessage(m, list, i)); });
  log.append(h("div", { id: "working", class: "working", hidden: true }, h("span", { class: "dots" }, h("i"), h("i"), h("i")), h("span", { id: "working-text" }, "Working")));
  renderWorking();
  if (!keepScroll) requestAnimationFrame(() => { $("#chat-scroll").scrollTop = $("#chat-scroll").scrollHeight; });
}
let workingSince = 0, workingTimer = null;
function renderWorking() {
  const w = $("#working"); if (!w) return;
  const s = state.sessions.get(state.threadId); const list = state.messages.get(state.threadId) || [];
  const last = list[list.length - 1];
  const busy = !!s?.busy;
  const streaming = busy && last && last.role === "bot" && (last.kind === "text" || (last.kind === "tool" && last.tool?.status !== "completed"));
  const waiting = last?.kind === "request" && last.request?.state === "pending";
  w.hidden = !busy || streaming || waiting;
  if (busy && !workingSince) workingSince = Date.now();
  if (!busy) workingSince = 0;
  clearInterval(workingTimer);
  if (!w.hidden) { const tick = () => { $("#working-text").textContent = `Working · ${Math.round((Date.now() - (workingSince || Date.now())) / 1000)}s`; }; tick(); workingTimer = setInterval(tick, 1000); }
  $$(".msg.agent .caret").forEach((c) => c.remove());
  if (busy && last?.kind === "text" && last.role === "bot") { const el = document.getElementById("m-" + last.id); const body = el?.querySelector(".body"); if (body) body.innerHTML = markdown(last.text, { streaming: true }); }
}
function renderMessage(m, list, i) {
  const wrap = (cls, ...kids) => h("div", { class: "msg " + cls, id: "m-" + m.id }, ...kids);
  if (m.role === "user") return wrap("user", h("div", { class: "user-col" }, h("div", { class: "bubble" }, m.text), h("div", { class: "meta-line" }, clock(m.at))));
  switch (m.kind) {
    case "text": return wrap("agent", h("div", { class: "bubble" }, h("div", { class: "body", html: markdown(m.text) })));
    case "thought": return wrap("agent", h("details", { class: "thought" }, h("summary", { html: icon("chevron", "sm") + "Thinking" }), h("div", { class: "body" }, m.text)));
    case "notice": return wrap("agent" + (m.error ? " error" : ""), h("div", { class: "notice" + (m.error ? " error" : "") }, m.error ? h("span", { html: icon("alert", "sm") }) : null, h("span", { class: m.error ? "grow" : "" }, m.text),
      m.error && (i === list.length - 1 || m.action === "model" || m.action === "providers") ? noticeActions(m, list, i === list.length - 1) : null));
    case "tool": return wrap("agent card-msg", toolCard(m.tool));
    case "todo": return wrap("agent card-msg", todoCard(m.entries || []));
    case "changes": return wrap("agent card-msg", changesCard(m.changes || []));
    case "request": return wrap("agent card-msg", requestCard(m.request));
  }
  return wrap("agent", h("div", { class: "bubble" }, m.text || ""));
}
function noticeActions(m, list, last) {
  const lastUser = [...list].reverse().find((x) => x.role === "user");
  const retry = lastUser && last ? h("button", { class: "btn sm", type: "button", "data-retry": "", onclick: () => { prompt.value = lastUser.text; updateComposer(); $("#chat-form").requestSubmit(); } }, h("span", { html: icon("refresh", "sm") }), "Retry") : null;
  if (m.action === "providers") return h("span", { class: "row" }, h("button", { class: "btn sm primary", type: "button", onclick: () => openProviders() }, h("span", { html: icon("key", "sm") }), "Providers"), retry);
  if (m.action === "model") return h("span", { class: "row" }, h("button", { class: "btn sm primary", type: "button", onclick: (e) => openOptionMenu("model", e.currentTarget) }, "Choose model"), retry);
  return retry;
}
const openTools = new Set();
function toolCard(t) {
  const running = t.status === "pending" || t.status === "in_progress";
  const stateEl = running ? h("span", { class: "spinner" }) : t.status === "failed" ? h("span", { class: "state-bad", html: icon("x", "sm") }) : h("span", { class: "state-ok", html: icon("check", "sm") });
  const card = h("div", { class: "tool" + (openTools.has(t.id) ? " open" : "") });
  const title = (t.title || t.name || "Tool").replace(/\/home\/user\/\.supercharge\/\S*\/([^/\s]+)/g, "$1").replaceAll(WORKSPACE + "/", "");
  const head = h("button", { class: "tool-head", type: "button", onclick: () => { card.classList.toggle("open"); openTools[card.classList.contains("open") ? "add" : "delete"](t.id); body.hidden = !card.classList.contains("open"); } },
    h("span", { class: "faint", html: icon(toolIcons[t.kind] || "tool", "sm") }), h("span", { class: "t", html: inline(escapeHtml(title)) }),
    t.diffs?.length ? h("span", { class: "tag" }, t.diffs.length + (t.diffs.length > 1 ? " files" : " file")) : null, stateEl, h("span", { class: "chev", html: icon("chevron", "sm") }));
  const body = h("div", { class: "tool-body", hidden: !openTools.has(t.id) });
  if (t.input && !t.diffs?.length) body.append(h("div", {}, h("h4", {}, "Input"), h("pre", {}, prettyJSON(t.input))));
  for (const d of t.diffs || []) body.append(diffView(d));
  const out = toolOutput(t.output);
  // An edit's diff already shows what changed; only show its output on failure.
  if (out && (!t.diffs?.length || t.status === "failed")) body.append(h("div", {}, h("h4", {}, t.status === "failed" ? "Error" : "Output"), h("pre", {}, out)));
  if (!body.childNodes.length) body.append(h("div", { class: "faint" }, running ? "Running…" : "No output."));
  // Edits open by default so the diff is visible without a click.
  if (t.diffs?.length && !openTools.has("closed:" + t.id) && !card.classList.contains("open")) { card.classList.add("open"); body.hidden = false; }
  card.append(head, body); return card;
}
// Tool results are often JSON envelopes; prefer their human summary.
function toolOutput(s) {
  if (!s) return "";
  try {
    const v = JSON.parse(s);
    if (v && typeof v === "object") return String(v.tool_output_for_prompt_concise || v.tool_output_for_prompt || v.output || v.stdout || v.message || JSON.stringify(v, null, 2)).replaceAll(WORKSPACE + "/", "");
  } catch { /* plain text */ }
  return s.replaceAll(WORKSPACE + "/", "");
}
const WORKSPACE = "/home/user/workspace";
// Paths read relative to the workspace; agent-internal files show their name.
function shortPath(p) {
  if (!p) return "";
  if (p.startsWith(WORKSPACE + "/")) return p.slice(WORKSPACE.length + 1);
  if (p.includes("/.supercharge/")) return p.split("/").pop();
  return p;
}
function prettyJSON(s) { try { return JSON.stringify(JSON.parse(s), null, 2); } catch { return s; } }
function todoCard(entries) {
  const done = entries.filter((e) => e.status === "completed").length;
  return h("div", { class: "todo" }, h("div", { class: "row faint", style: { fontSize: "12px" } }, h("span", { html: icon("list", "sm") }), `Plan · ${done}/${entries.length} done`),
    entries.map((e) => h("div", { class: "ti" }, h("span", { class: e.status === "completed" ? "state-ok" : "faint", html: icon(e.status === "completed" ? "check" : e.status === "in_progress" ? "dotcircle" : "circle", "sm") }),
      h("span", { class: e.status === "completed" ? "done" : e.status === "in_progress" ? "doing" : "muted" }, e.content))));
}
function changesCard(changes) {
  const sym = { added: "+", modified: "~", deleted: "−" };
  return h("div", { class: "changes" }, h("div", { class: "row faint", style: { fontSize: "12px" } }, h("span", { html: icon("fileedit", "sm") }), `${changes.length} file${changes.length === 1 ? "" : "s"} changed`),
    changes.slice(0, 20).map((c) => h("div", { class: "ch" }, h("span", { class: "k " + c.kind }, sym[c.kind] || "~"), h("span", { class: "grow" }, c.path))),
    changes.length > 20 ? h("div", { class: "faint" }, `and ${changes.length - 20} more`) : null);
}

// ---------- requests: permission, questions, plan ----------
async function respond(req, answer, btn) {
  if (btn) { btn.classList.add("loading"); btn.disabled = true; }
  try { await api(`/api/threads/${enc(state.threadId)}/respond`, "POST", { requestId: req.id, answer }); }
  catch (err) { wsError(err.message); if (btn) { btn.classList.remove("loading"); btn.disabled = false; } }
}
function requestCard(r) {
  const pending = r.state === "pending";
  const card = h("div", { class: "ask" + (pending ? " pending" : ""), "data-request": r.type });
  if (r.type === "permission") {
    card.append(h("div", { class: "ask-head" }, h("span", { html: icon("shield") }), h("strong", {}, "Permission needed")), h("div", { class: "q", html: inline(escapeHtml((r.title || "").replaceAll(WORKSPACE + "/", ""))) }));
    if (pending) {
      const kinds = { allow_once: "primary", allow_always: "", reject_once: "danger", reject_always: "danger" };
      card.append(h("div", { class: "ask-actions" }, (r.options || []).map((o) => { const b = h("button", { class: "btn sm " + (kinds[o.kind] ?? ""), type: "button", "data-option": o.optionId, onclick: () => respond(r, { optionId: o.optionId }, b) }, o.name || o.optionId); return b; })));
    } else card.append(answered(r, r.answer?.optionId ? (r.options || []).find((o) => o.optionId === r.answer.optionId)?.name || r.answer.optionId : null));
    return card;
  }
  if (r.type === "plan") {
    card.append(h("div", { class: "ask-head" }, h("span", { html: icon("map") }), h("strong", {}, "Plan ready for review"), h("span", { class: "grow" }), pending ? h("span", { class: "tag warn" }, "Waiting for you") : null),
      h("div", { class: "plan-doc body", html: markdown(r.plan || "") }));
    if (pending) {
      const fb = h("textarea", { class: "input", rows: "3", placeholder: "What should change in the plan?", hidden: true });
      const approve = h("button", { class: "btn sm primary", type: "button", "data-plan": "approve", onclick: () => respond(r, { outcome: "approved" }, approve) }, "Approve and run");
      const revise = h("button", { class: "btn sm", type: "button", "data-plan": "revise", onclick: () => {
        if (fb.hidden) { fb.hidden = false; fb.focus(); revise.textContent = "Send feedback"; return; }
        respond(r, { outcome: "cancelled", feedback: fb.value.trim() }, revise);
      } }, "Revise");
      const discard = h("button", { class: "btn sm ghost danger", type: "button", "data-plan": "reject", onclick: () => respond(r, { outcome: "abandoned" }, discard) }, "Reject");
      card.append(fb, h("div", { class: "ask-actions" }, approve, revise, h("span", { class: "grow" }), discard));
    } else {
      const o = r.answer?.outcome; card.append(answered(r, o === "approved" ? "Approved" : o === "abandoned" ? "Rejected" : o === "cancelled" ? "Sent back for revision" + (r.answer?.feedback ? `: “${r.answer.feedback}”` : "") : null));
    }
    return card;
  }
  // questions
  card.append(h("div", { class: "ask-head" }, h("span", { html: icon("help") }), h("strong", {}, (r.questions || []).length > 1 ? `${r.questions.length} questions` : "Question")));
  const picks = (r.questions || []).map(() => new Set());
  const others = [];
  (r.questions || []).forEach((q, qi) => {
    const opts = h("div", { class: "opts" });
    const other = h("input", { class: "input", placeholder: "Other answer…", disabled: !pending });
    others.push(other);
    (q.options || []).forEach((o) => {
      const b = h("button", { class: "opt" + (q.multiSelect ? " multi" : ""), type: "button", disabled: !pending, "data-label": o.label, onclick: () => {
        if (q.multiSelect) { picks[qi].has(o.label) ? picks[qi].delete(o.label) : picks[qi].add(o.label); b.classList.toggle("on"); }
        else { picks[qi].clear(); picks[qi].add(o.label); $$(".opt", opts).forEach((x) => x.classList.toggle("on", x === b)); }
      } }, h("span", { class: "box" }), h("span", {}, h("div", { class: "ol" }, o.label), o.description ? h("div", { class: "od" }, o.description) : null));
      opts.append(b);
    });
    card.append(h("div", {}, h("div", { class: "q" }, q.question), opts, pending ? h("div", { style: { marginTop: "6px" } }, other) : null));
  });
  const collect = () => { const answers = {}; (r.questions || []).forEach((q, qi) => { const list = [...picks[qi]]; const extra = others[qi].value.trim(); if (extra) list.push(extra); if (list.length) answers[q.question] = list; }); return answers; };
  if (pending) {
    const submit = h("button", { class: "btn sm primary", type: "button", "data-answer": "submit", onclick: () => { const a = collect(); if (!Object.keys(a).length) { toast("Pick an answer first"); return; } respond(r, { outcome: "accepted", answers: a }, submit); } }, "Submit");
    const chat = h("button", { class: "btn sm", type: "button", onclick: () => respond(r, { outcome: "chat_about_this", answers: collect() }, chat) }, "Chat about this");
    const skip = h("button", { class: "btn sm ghost", type: "button", onclick: () => respond(r, { outcome: "skip_interview", answers: collect() }, skip) }, "Skip");
    const planExtras = r.mode === "plan";
    card.append(h("div", { class: "ask-actions" }, submit, planExtras ? chat : null, h("span", { class: "grow" }), planExtras ? skip : h("button", { class: "btn sm ghost", type: "button", onclick: (e) => respond(r, { outcome: "cancelled" }, e.currentTarget) }, "Dismiss")));
  } else {
    const a = r.answer || {};
    const text = a.answers && Object.keys(a.answers).length ? Object.entries(a.answers).map(([q, v]) => `${v.join(", ")}`).join(" · ") : a.outcome === "skip_interview" ? "Skipped" : a.outcome === "chat_about_this" ? "Chose to discuss" : null;
    card.append(answered(r, text));
  }
  return card;
}
function answered(r, text) {
  return h("div", { class: "answered" }, r.state === "answered" ? h("span", { class: "state-ok", html: icon("check", "sm") }) : h("span", { class: "faint", html: icon("x", "sm") }), r.state === "answered" ? (text || "Answered") : "No longer waiting");
}

// ---------- composer ----------
const prompt = $("#chat-prompt");
function sessionOf() { return state.sessions.get(state.threadId) || {}; }
function renderComposer() {
  const s = sessionOf();
  $("#chat-stop").hidden = !s.busy; $("#chat-send").hidden = !!s.busy && !prompt.value.trim();
  updateComposer();
  const foot = $("#composer-foot"); foot.replaceChildren();
  const opts = Array.isArray(s.configOptions) ? s.configOptions : [];
  const parts = [];
  for (const o of opts) {
    if (o.type && o.type !== "select") continue;
    const cur = (o.options || []).find((x) => x.value === o.currentValue);
    const label = o.id === "permission_mode" ? ({ ask: "Ask before edits", auto: "Auto", "always-approve": "Always approve" }[o.currentValue] || cur?.name) : o.id === "context_window" ? (o.currentValue === "auto" ? "Context auto" : `Context ${cur?.name || o.currentValue}`) : (cur?.name || o.currentValue);
    parts.push(h("button", { class: "foot-opt", type: "button", "data-option": o.id, title: o.name || o.id, onclick: (e) => { e.stopPropagation(); openOptionMenu(o.id, e.currentTarget); } }, label, h("span", { html: icon("down", "sm") })));
  }
  if (!opts.length && s.models?.currentModelId) parts.push(h("span", {}, s.models.currentModelId));
  if (s.usage?.usedTokens && s.usage?.maxTokens) parts.push(h("span", { title: `${s.usage.usedTokens} of ${s.usage.maxTokens} tokens` }, `${Math.round((100 * s.usage.usedTokens) / s.usage.maxTokens)}% context`));
  if (s.queued) parts.push(h("span", {}, `${s.queued} queued`));
  parts.forEach((p, i) => { if (i) foot.append(h("span", { class: "sep" })); foot.append(p); });
  foot.append(h("span", { class: "grow" }), h("span", { class: "kbd-hint", html: "<kbd>Enter</kbd> send · <kbd>Shift</kbd>+<kbd>Enter</kbd> new line · <kbd>/</kbd> commands" }));
}
function updateComposer() {
  prompt.style.height = "auto"; prompt.style.height = Math.min(prompt.scrollHeight, 220) + "px";
  const busy = !!sessionOf().busy, has = !!prompt.value.trim();
  $("#chat-send").disabled = !has || !state.threadId; $("#chat-send").hidden = busy && !has; $("#chat-stop").hidden = !busy || has;
  $("#chat-send").title = busy ? "Queue after this turn (Enter)" : "Send (Enter)";
  renderPalette();
}
prompt.addEventListener("input", updateComposer);
prompt.addEventListener("keydown", (e) => {
  const pal = $("#palette");
  if (!pal.hidden && ["ArrowDown", "ArrowUp", "Tab"].includes(e.key) || (!pal.hidden && e.key === "Enter" && !e.shiftKey && palette.items.length && prompt.value.trim().split(/\s/).length === 1)) {
    e.preventDefault();
    if (e.key === "ArrowDown") palette.index = (palette.index + 1) % palette.items.length;
    else if (e.key === "ArrowUp") palette.index = (palette.index - 1 + palette.items.length) % palette.items.length;
    else { pickCommand(palette.items[palette.index]); return; }
    renderPalette(true); return;
  }
  if (e.key === "Escape") { if (!pal.hidden) { pal.hidden = true; return; } if (sessionOf().busy) stopTurn(); return; }
  if (e.key === "Enter" && !e.shiftKey && !e.isComposing) { e.preventDefault(); $("#chat-form").requestSubmit(); }
});
$("#chat-form").onsubmit = async (e) => {
  e.preventDefault();
  const text = prompt.value.trim(); const bot = currentBot(); if (!text || !bot || !state.threadId) return;
  prompt.value = ""; updateComposer(); $("#palette").hidden = true;
  try {
    const out = await api(`/api/bots/${enc(bot.id)}/messages`, "POST", { text, threadId: state.threadId });
    if (out.queued) toast("Queued. It runs when this turn ends.");
  } catch (err) { prompt.value = text; updateComposer(); wsError(err.message); }
  prompt.focus();
};
// Session options (model, permissions, context window, reasoning effort) come
// from the agent's configOptions and change with session/set_config_option.
function openOptionMenu(id, anchor) {
  const o = (sessionOf().configOptions || []).find((x) => x.id === id); if (!o) return;
  const menu = $("#mode-menu");
  menu.replaceChildren(h("div", { class: "side-label", style: { padding: "6px 10px 4px" } }, o.name || id), ...(o.options || []).map((x) => h("button", { type: "button", class: x.value === o.currentValue ? "on" : "", "data-value": x.value, onclick: async () => {
    menu.hidden = true;
    try { const st = await api(`/api/threads/${enc(state.threadId)}/config`, "POST", { configId: id, value: x.value }); state.sessions.set(state.threadId, st); renderComposer(); toast(`${o.name || id}: ${x.name || x.value}`); }
    catch (err) { wsError(err.message); }
  } }, h("span", { class: "grow" }, x.name || x.value), x.value === o.currentValue ? h("span", { class: "sub", html: icon("check", "sm") }) : null)));
  const col = $(".composer-col").getBoundingClientRect(), r = anchor.getBoundingClientRect();
  menu.style.left = Math.max(0, r.left - col.left) + "px"; menu.style.bottom = "calc(100% + 6px)";
  menu.hidden = false;
}
async function stopTurn() {
  const bot = currentBot(); if (!bot) return;
  $("#chat-stop").classList.add("loading");
  try { await api(`/api/bots/${enc(bot.id)}/interrupt`, "POST", { threadId: state.threadId }); } catch (err) { wsError(err.message); }
  finally { $("#chat-stop").classList.remove("loading"); }
}
$("#chat-stop").onclick = stopTurn;
// slash commands come from the agent's available_commands_update
const palette = { items: [], index: 0 };
function commandList() { const c = sessionOf().commands; return Array.isArray(c) ? c : []; }
function renderPalette(keepIndex = false) {
  const pal = $("#palette"); const v = prompt.value;
  if (!v.startsWith("/") || v.includes("\n") || /\s/.test(v.trim().slice(1)) && v.trim().split(/\s/).length > 1) { pal.hidden = true; return; }
  const q = v.slice(1).toLowerCase();
  palette.items = commandList().filter((c) => c.name.toLowerCase().startsWith(q) || (q.length > 1 && c.name.toLowerCase().includes(q)));
  if (!keepIndex) palette.index = 0;
  if (!palette.items.length) { pal.hidden = commandList().length > 0 ? false : true; pal.replaceChildren(h("div", { class: "empty", style: { padding: "12px" } }, commandList().length ? "No matching command" : "Commands load when the session opens")); if (!commandList().length) pal.hidden = true; return; }
  pal.replaceChildren(...palette.items.map((c, i) => h("button", { type: "button", class: i === palette.index ? "on" : "", "data-cmd": c.name, onmousedown: (e) => { e.preventDefault(); pickCommand(c); } },
    h("span", { class: "cmd" }, "/" + c.name), c.input?.hint ? h("span", { class: "faint", style: { font: "12px var(--mono)" } }, c.input.hint) : null, h("span", { class: "desc grow" }, c.description || ""))));
  pal.hidden = false;
  pal.querySelector(".on")?.scrollIntoView({ block: "nearest" });
}
function pickCommand(c) { prompt.value = "/" + c.name + (c.input ? " " : ""); $("#palette").hidden = true; updateComposer(); prompt.focus(); if (!c.input) $("#palette").hidden = true; }
// dictation
const Speech = window.SpeechRecognition || window.webkitSpeechRecognition;
let recog = null;
if (!Speech) { $("#mic").disabled = true; $("#mic").title = "Voice input isn't available in this browser"; }
$("#mic").onclick = () => {
  if (recog) { recog.stop(); return; }
  recog = new Speech(); recog.interimResults = true; recog.continuous = false; const base = prompt.value;
  recog.onresult = (e) => { prompt.value = (base ? base + " " : "") + [...e.results].map((r) => r[0].transcript).join(""); updateComposer(); };
  recog.onend = () => { recog = null; $("#mic").classList.remove("on"); prompt.focus(); };
  recog.onerror = (e) => { if (e.error !== "aborted") toast("Voice input: " + e.error); };
  $("#mic").classList.add("on"); recog.start();
};
// attach uploads into the bot's library and mentions it in the message
$("#attach").onclick = () => $("#attach-input").click();
$("#attach-input").onchange = async (e) => {
  const files = [...e.target.files]; e.target.value = "";
  const names = await uploadFiles(files);
  if (names.length) { prompt.value = (prompt.value ? prompt.value + "\n" : "") + names.map((n) => `Attached: ${n} (in your files)`).join("\n"); updateComposer(); prompt.focus(); }
};
// share copies the thread as Markdown
$("#share").onclick = async () => {
  const bot = currentBot(); const list = state.messages.get(state.threadId) || [];
  const md = list.map((m) => m.role === "user" ? `**You:** ${m.text}` : m.kind === "text" ? `**${bot?.name || "Bot"}:** ${m.text}` : m.kind === "tool" ? `> ${m.tool?.title || "tool"} (${m.tool?.status})` : m.kind === "request" && m.request?.plan ? `### Plan\n${m.request.plan}` : m.kind === "notice" ? `_${m.text}_` : "").filter(Boolean).join("\n\n");
  try { await navigator.clipboard.writeText(md); toast("Thread copied as Markdown"); } catch { toast("Copy failed"); }
};

// ---------- info pane ----------
const app = $("#workspace");
$("#info-toggle").onclick = () => {
  if (matchMedia("(max-width: 1180px)").matches) app.classList.toggle("show-info"); else app.classList.toggle("no-info");
};
$("#side-toggle").onclick = () => app.classList.toggle("side-open");
$(".main").addEventListener("pointerdown", (e) => { if (!matchMedia("(max-width: 820px)").matches || e.target.closest("#side-toggle, #info-toggle")) return; app.classList.remove("side-open", "show-info"); });
$$("[data-info]").forEach((b) => (b.onclick = () => {
  $$("[data-info]").forEach((x) => x.classList.toggle("on", x === b));
  $("#info-activity").hidden = b.dataset.info !== "activity"; $("#info-library").hidden = b.dataset.info !== "library";
  if (b.dataset.info === "library") { loadFiles(); renderLibrary(); }
}));
async function loadModels() {
  try { const { modelState } = await api("/api/models"); state.models = modelState?.availableModels || []; state.currentModel = modelState?.currentModelId || ""; }
  catch { state.models = []; }
  renderSettings();
}
function renderSettings() {
  const bot = currentBot(); if (!bot) return;
  const sel = $("#set-model"); const want = bot.settings?.model || "";
  sel.replaceChildren(h("option", { value: "" }, "Workspace default" + (state.currentModel ? ` (${state.models.find((m) => m.modelId === state.currentModel)?.name || state.currentModel})` : "")),
    ...state.models.map((m) => h("option", { value: m.modelId }, m.name || m.modelId)));
  if (want && !state.models.some((m) => m.modelId === want)) sel.append(h("option", { value: want }, want));
  sel.value = want;
  $("#set-permission").value = bot.settings?.permissionMode || "ask";
  $("#set-mode").value = bot.settings?.mode || "default";
}
$("#settings-form").onsubmit = async (e) => {
  e.preventDefault(); const bot = currentBot(); if (!bot) return;
  const btn = e.submitter; btn?.classList.add("loading");
  const body = { model: $("#set-model").value, permissionMode: $("#set-permission").value, mode: $("#set-mode").value };
  try {
    const out = await api(`/api/bots/${enc(bot.id)}/settings`, "PATCH", body); mergeBot(out.bot);
    // The open thread follows the new model and permissions right away.
    const s = sessionOf();
    if (s.open) {
      if (body.model) await api(`/api/threads/${enc(state.threadId)}/config`, "POST", { configId: "model", value: body.model }).then((st) => state.sessions.set(state.threadId, st)).catch(() => {});
      await api(`/api/threads/${enc(state.threadId)}/config`, "POST", { configId: "permission_mode", value: body.permissionMode }).then((st) => state.sessions.set(state.threadId, st)).catch(() => {});
      renderComposer();
    }
    $("#settings-msg").textContent = "Saved"; setTimeout(() => { $("#settings-msg").textContent = ""; }, 1500);
  } catch (err) { wsError(err.message); }
  finally { btn?.classList.remove("loading"); }
};
function renderComputer() {
  const box = $("#computer-card"); if (!box) return;
  const ctl = !!desk.control;
  box.replaceChildren(
    h("div", { class: "kv" }, h("span", {}, "Workspace"), h("span", { class: "status" }, h("span", { class: $("#ws-dot").className }), h("b", {}, $("#ws-state").textContent))),
    h("div", { class: "kv" }, h("span", {}, "Desktop"), h("b", {}, ctl ? "You have control" : "Agent in control")),
    h("div", { class: "row" }, h("button", { class: "btn sm grow", type: "button", onclick: () => selectTab("desktop") }, h("span", { html: icon("monitor", "sm") }), "Open desktop"), h("button", { class: "btn sm", type: "button", onclick: () => selectTab("terminal") }, h("span", { html: icon("terminal", "sm") }), "Terminal")));
}
async function loadFiles() {
  const bot = currentBot(); const list = $("#file-list"); if (!bot || !state.ws?.session) return;
  try {
    const { files = [] } = await api(`/api/bots/${enc(bot.id)}/files`);
    list.replaceChildren(...files.map((f) => h("div", { class: "li" }, h("span", { class: "faint", html: icon("file", "sm") }), h("span", { class: "t" }, f.name), h("span", { class: "s" }, fmtSize(f.size)),
      h("span", { class: "acts", style: { display: "flex" } },
        h("button", { class: "btn ghost icon sm", type: "button", title: "Download", onclick: () => downloadFile(bot, f.name), html: icon("download", "sm") }),
        h("button", { class: "btn ghost icon sm", type: "button", title: "Delete", onclick: async () => { if (!confirm(`Delete ${f.name}?`)) return; await api(`/api/bots/${enc(bot.id)}/files/${enc(f.name)}`, "DELETE").catch((err) => wsError(err.message)); loadFiles(); }, html: icon("trash", "sm") })))));
    if (!files.length) list.append(h("div", { class: "empty" }, "Files you add here are readable by this bot."));
  } catch { /* the library is optional */ }
}
const fmtSize = (n) => n < 1024 ? n + " B" : n < 1048576 ? (n / 1024).toFixed(1) + " KB" : (n / 1048576).toFixed(1) + " MB";
async function downloadFile(bot, name) {
  const res = await fetch(`${state.ws.host}/api/bots/${enc(bot.id)}/files/${enc(name)}`, { headers: { authorization: "Bearer " + state.ws.session } });
  if (!res.ok) { wsError("Download failed"); return; }
  const url = URL.createObjectURL(await res.blob()); const a = h("a", { href: url, download: name }); a.click(); setTimeout(() => URL.revokeObjectURL(url), 2000);
}
async function uploadFiles(files) {
  const bot = currentBot(); const done = [];
  for (const f of files) {
    try { await host(`/api/bots/${enc(bot.id)}/files/${enc(f.name)}`, { method: "PUT", body: f, raw: true }); done.push(f.name); }
    catch (err) { wsError(`${f.name}: ${err.message}`); }
  }
  if (done.length) { toast(`Added ${done.length} file${done.length > 1 ? "s" : ""}`); loadFiles(); }
  return done;
}
$("#lib-upload").onclick = () => { const i = h("input", { type: "file", multiple: true }); i.onchange = () => uploadFiles([...i.files]); i.click(); };
function renderLibrary() {
  const list = $("#changed-list"); if (!list) return;
  const changed = new Map();
  for (const m of state.messages.get(state.threadId) || []) for (const c of m.changes || []) changed.set(c.path, c.kind);
  list.replaceChildren(...[...changed].map(([p, k]) => h("div", { class: "li" }, h("span", { class: "k " + k, style: { fontFamily: "var(--mono)", width: "14px", color: k === "added" ? "var(--ok)" : k === "deleted" ? "var(--bad)" : "var(--warn)" } }, k === "added" ? "+" : k === "deleted" ? "−" : "~"), h("span", { class: "t", style: { fontFamily: "var(--mono)", fontSize: "12px" } }, p))));
  if (!changed.size) list.append(h("div", { class: "empty" }, "No file changes in this thread yet."));
}

// ---------- bot editor ----------
const COLORS = ["#22a06b", "#3b82f6", "#ef6461", "#f08c3a", "#8b5cf6", "#06b6d4", "#ec4899", "#d4a017", "#14b8a6", "#f97362"];
const EMOJIS = ["🤖", "🚀", "🧠", "🛠️", "🔍", "📈", "🎨", "✍️", "🧪", "🐛", "📦", "🛰️", "⚡", "🔥", "🌱", "🦊", "🐙", "🦉", "🐝", "🐳", "🎯", "📚", "💡", "🧭"];
const MAX_FACE = 2 << 20;
let editing = null;
// draft is the look being edited; image holds a picked file until saved.
let draft = {};
const randomSeed = () => Math.random().toString(36).slice(2, 10);
function freshColor() { const worn = new Set(state.bots.map((b) => (b.color || "").toLowerCase())); const free = COLORS.filter((c) => !worn.has(c)); const pool = free.length ? free : COLORS; return pool[Math.floor(Math.random() * pool.length)]; }
function openBotDialog(bot) {
  editing = bot || null;
  draft = bot
    ? { color: bot.color, avatarKind: bot.avatarKind || "generated", emoji: bot.emoji || "", avatarShape: bot.avatarShape || "circle", glyph: bot.glyph, glyphSeed: null, avatarUrl: bot.avatarUrl, image: null, removeImage: false }
    : (() => { const seed = randomSeed(); return { color: freshColor(), avatarKind: "generated", emoji: "", avatarShape: "circle", glyphSeed: seed, glyph: glyphOf(seed), avatarUrl: null, image: null, removeImage: false }; })();
  $("#bot-dialog-title").textContent = bot ? `Edit ${bot.name}` : "New bot";
  $("#bot-save").textContent = bot ? "Save" : "Create bot";
  $("#agent-name").value = bot?.name || ""; $("#agent-title").value = bot?.title || ""; $("#agent-instructions").value = bot?.instructions || "";
  $("#agent-description").value = bot?.description || ""; $("#agent-pinned").checked = !!bot?.pinned;
  $("#pin-wrap").hidden = !bot || bot.main; $("#bot-main-tag").hidden = !bot?.main;
  $("#agent-msg").textContent = ""; $("#bot-delete").hidden = !bot || bot.main; $("#bot-duplicate").hidden = !bot;
  $("#emoji-custom").value = draft.emoji && !EMOJIS.includes(draft.emoji) ? draft.emoji : "";
  renderLook(); $("#bot-dialog").showModal(); $("#agent-name").focus();
}
function previewBot() {
  const hasImage = !draft.removeImage && (draft.image || draft.avatarUrl);
  return { name: $("#agent-name").value || "?", color: draft.color, avatarKind: draft.avatarKind === "image" && !hasImage ? "generated" : draft.avatarKind, emoji: draft.emoji, avatarShape: draft.avatarShape, glyph: draft.glyph, avatarUrl: draft.removeImage ? null : draft.avatarUrl, avatarPreview: draft.image?.url };
}
function renderLook() {
  $("#swatches").replaceChildren(...COLORS.map((c) => h("button", { type: "button", class: c === draft.color ? "on" : "", style: { "--c": c }, title: c, "aria-label": "Color " + c, onclick: () => { draft.color = c; renderLook(); } })));
  $$("#icon-kinds button").forEach((b) => b.classList.toggle("on", b.dataset.kind === draft.avatarKind));
  $$("#icon-shapes button").forEach((b) => b.classList.toggle("on", b.dataset.shape === draft.avatarShape));
  $("#icon-emoji").hidden = draft.avatarKind !== "emoji"; $("#icon-image").hidden = draft.avatarKind !== "image";
  $("#emoji-grid").replaceChildren(...EMOJIS.map((e) => h("button", { type: "button", class: e === draft.emoji ? "on" : "", onclick: () => { draft.emoji = e; $("#emoji-custom").value = ""; renderLook(); } }, e)));
  const hasImage = !draft.removeImage && (draft.image || draft.avatarUrl);
  $("#avatar-remove").hidden = !hasImage; $("#avatar-upload").lastChild.textContent = hasImage ? "Replace image…" : "Choose image…";
  $("#avatar-hint").textContent = draft.image ? draft.image.name : "PNG, JPEG, GIF or WebP";
  avatar($("#bot-preview-avatar"), previewBot());
  $("#bot-preview-avatar").title = draft.avatarKind === "generated" ? "Click to shuffle the pattern" : "";
}
$("#bot-preview-avatar").onclick = () => { if (draft.avatarKind !== "generated") return; draft.glyphSeed = randomSeed(); draft.glyph = glyphOf(draft.glyphSeed); renderLook(); };
$$("#icon-kinds button").forEach((b) => (b.onclick = () => {
  draft.avatarKind = b.dataset.kind;
  if (draft.avatarKind === "emoji" && !draft.emoji) draft.emoji = EMOJIS[0];
  if (draft.avatarKind === "image" && !draft.image && (draft.removeImage || !draft.avatarUrl)) $("#avatar-file").click();
  renderLook();
}));
$$("#icon-shapes button").forEach((b) => (b.onclick = () => { draft.avatarShape = b.dataset.shape; renderLook(); }));
$("#emoji-custom").oninput = (e) => { const v = e.target.value.trim(); if (v) { draft.emoji = v; renderLook(); } };
$("#avatar-upload").onclick = () => $("#avatar-file").click();
$("#avatar-remove").onclick = () => { if (draft.image) URL.revokeObjectURL(draft.image.url); draft.image = null; draft.removeImage = true; draft.avatarKind = "generated"; renderLook(); };
// Big pictures are scaled down to 512px before upload; small ones (and GIFs
// that fit) go as they are so animation survives.
async function prepareFace(file) {
  if (!/^image\/(png|jpeg|gif|webp)$/.test(file.type)) throw new Error("Choose a PNG, JPEG, GIF or WebP image.");
  if (file.size <= 512 * 1024 || (file.type === "image/gif" && file.size <= MAX_FACE)) return file;
  const bmp = await createImageBitmap(file);
  const scale = Math.min(1, 512 / Math.max(bmp.width, bmp.height));
  const c = document.createElement("canvas"); c.width = Math.round(bmp.width * scale); c.height = Math.round(bmp.height * scale);
  c.getContext("2d").drawImage(bmp, 0, 0, c.width, c.height);
  const blob = await new Promise((r) => c.toBlob(r, "image/webp", 0.9)) || await new Promise((r) => c.toBlob(r, "image/png"));
  if (!blob || blob.size > MAX_FACE) throw new Error("That image is too large. Try one under 2 MB.");
  return blob;
}
$("#avatar-file").onchange = async (e) => {
  const file = e.target.files?.[0]; e.target.value = ""; if (!file) { if (!draft.image && !draft.avatarUrl) { draft.avatarKind = "generated"; renderLook(); } return; }
  try {
    const blob = await prepareFace(file);
    if (draft.image) URL.revokeObjectURL(draft.image.url);
    draft.image = { blob, url: URL.createObjectURL(blob), name: file.name }; draft.removeImage = false; draft.avatarKind = "image"; $("#agent-msg").textContent = "";
  } catch (err) { $("#agent-msg").textContent = err.message; }
  renderLook();
};
$("#agent-name").oninput = renderLook;
$("#new-bot").onclick = () => openBotDialog(null);
$("#edit-bot").onclick = () => openBotDialog(currentBot());
async function uploadFace(botId, blob) {
  return host(`/api/bots/${enc(botId)}/avatar`, { method: "PUT", body: blob, raw: true });
}
$("#bot-form").onsubmit = async (e) => {
  if (e.submitter?.value !== "save") return;
  e.preventDefault();
  const name = $("#agent-name").value.trim(); if (!name) { $("#agent-msg").textContent = "Give the bot a name."; return; }
  const wantsImage = draft.avatarKind === "image";
  const body = { name, title: $("#agent-title").value.trim(), description: $("#agent-description").value.trim(), instructions: $("#agent-instructions").value.trim(),
    color: draft.color, emoji: draft.emoji || "", avatarShape: draft.avatarShape, avatarKind: wantsImage ? undefined : draft.avatarKind };
  if (draft.glyphSeed) body.glyphSeed = draft.glyphSeed;
  if (editing && !editing.main) body.pinned = $("#agent-pinned").checked;
  if (wantsImage && !draft.image && (draft.removeImage || !draft.avatarUrl)) { $("#agent-msg").textContent = "Choose an image, or pick another icon."; return; }
  const btn = $("#bot-save"); btn.classList.add("loading");
  try {
    let out = editing ? await api(`/api/bots/${enc(editing.id)}/profile`, "PATCH", body) : await api("/api/bots", "POST", body);
    const id = out.bot.id;
    if (draft.image) out = await uploadFace(id, draft.image.blob);
    else if (draft.removeImage && editing?.avatarUrl) out = await api(`/api/bots/${enc(id)}/avatar`, "DELETE");
    if (wantsImage && out.bot.avatarKind !== "image") out = await api(`/api/bots/${enc(id)}/profile`, "PATCH", { avatarKind: "image" });
    mergeBot(out.bot); $("#bot-dialog").close();
    if (!editing) await selectBot(id); else renderBotHeader();
  } catch (err) { $("#agent-msg").textContent = err.message; }
  finally { btn.classList.remove("loading"); }
};
$("#bot-duplicate").onclick = async () => {
  if (!editing) return;
  try { const out = await api(`/api/bots/${enc(editing.id)}/duplicate`, "POST"); mergeBot(out.bot); $("#bot-dialog").close(); await selectBot(out.bot.id); toast(`Made ${out.bot.name}`); }
  catch (err) { $("#agent-msg").textContent = err.message; }
};
$("#bot-delete").onclick = async () => {
  if (!editing || editing.main || !confirm(`Delete ${editing.name}? Its threads, files and routines are removed.`)) return;
  try { await api(`/api/bots/${enc(editing.id)}`, "DELETE"); $("#bot-dialog").close(); state.bots = state.bots.filter((b) => b.id !== editing.id); await selectBot((state.bots.find((b) => b.main) || state.bots[0])?.id); }
  catch (err) { $("#agent-msg").textContent = err.message; }
};

// ---------- routines ----------
async function loadRoutines() {
  if (!state.ws?.session) return;
  try { const out = await api("/api/routines"); state.routines = out.routines || []; state.runs = out.runs || []; } catch { return; }
  renderRoutinesCard(); renderRoutinesPage();
}
function scheduleText(s) {
  const days = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
  if (s.type === "daily") return `Daily at ${s.time}` + (s.weekdays?.length ? ` · ${s.weekdays.map((d) => days[d]).join(", ")}` : "");
  if (s.type === "interval") return `Every ${s.everyMinutes} min`;
  return `Once · ${new Date(s.at).toLocaleString()}`;
}
function routineRow(r, { compact = false } = {}) {
  const bot = state.bots.find((b) => b.id === r.botId);
  const toggle = async (e) => { e.stopPropagation(); await api(`/api/routines/${enc(r.id)}`, "PATCH", { enabled: !r.enabled }).catch((err) => wsError(err.message)); loadRoutines(); };
  const run = async (e) => { e.stopPropagation(); try { const { run } = await api(`/api/routines/${enc(r.id)}/run`, "POST"); toast("Routine started"); loadRoutines(); if (run?.threadId && r.botId === state.botId) { await loadBots(); openThread(run.threadId); selectTab("chat"); } } catch (err) { wsError(err.message); } };
  if (compact) return h("button", { class: "li", type: "button", onclick: () => openRoutineDialog(r) }, h("span", { class: "dot " + (r.enabled ? "ok" : "") }), h("span", { class: "t" }, r.name), h("span", { class: "s" }, r.enabled ? (r.nextRunAt ? timeAgoFuture(r.nextRunAt) : "") : "Off"),
    h("span", { class: "acts" }, h("span", { class: "btn ghost icon sm", title: "Run now", onclick: run, html: icon("play", "sm") })));
  const av = h("span", { class: "avatar sm" }); avatar(av, bot);
  return h("div", { class: "ws" }, av, h("div", { class: "grow" }, h("div", { class: "name" }, r.name), h("div", { class: "sub" }, `${bot?.name || "Deleted bot"} · ${scheduleText(r.schedule)}` + (r.enabled && r.nextRunAt ? ` · next ${new Date(r.nextRunAt).toLocaleString()}` : ""))),
    h("span", { class: "tag " + (r.enabled ? "ok" : "") }, r.enabled ? "On" : "Off"),
    h("button", { class: "btn sm ghost", type: "button", onclick: toggle }, r.enabled ? "Pause" : "Resume"),
    h("button", { class: "btn sm", type: "button", onclick: run }, h("span", { html: icon("play", "sm") }), "Run now"),
    h("button", { class: "btn ghost icon sm", type: "button", title: "Edit", onclick: () => openRoutineDialog(r), html: icon("edit", "sm") }));
}
function timeAgoFuture(ms) { const s = (ms - Date.now()) / 1000; if (s < 60) return "soon"; if (s < 3600) return `in ${Math.round(s / 60)}m`; if (s < 86400) return `in ${Math.round(s / 3600)}h`; return `in ${Math.round(s / 86400)}d`; }
function renderRoutinesCard() {
  const list = $("#bot-routines"); const mine = state.routines.filter((r) => r.botId === state.botId);
  list.replaceChildren(...mine.map((r) => routineRow(r, { compact: true })));
  if (!mine.length) list.append(h("div", { class: "empty" }, h("span", {}, "No routines. "), h("button", { class: "btn sm ghost", type: "button", onclick: () => openRoutineDialog(null) }, "Add one")));
}
function renderRoutinesPage() {
  const list = $("#routine-list"); list.replaceChildren(...state.routines.map((r) => routineRow(r)));
  if (!state.routines.length) list.append(h("div", { class: "empty" }, h("span", { html: icon("clock") }), h("strong", {}, "No routines yet"), "Schedule a prompt: a daily summary, a check every hour, a one-off reminder."));
  const runs = $("#run-list"); runs.replaceChildren(...state.runs.slice(0, 15).map((r) => h("div", { class: "li" }, h("span", { class: "dot " + (r.status === "failed" ? "bad" : "ok") }), h("span", { class: "t" }, r.routineName + (r.manual ? " (manual)" : "")), h("span", { class: "s" }, r.status + " · " + new Date(r.startedAt).toLocaleString()))));
  if (!state.runs.length) runs.append(h("div", { class: "empty" }, "No runs yet."));
}
let editingRoutine = null;
const DAYS = ["S", "M", "T", "W", "T", "F", "S"]; let pickedDays = new Set();
function openRoutineDialog(r) {
  editingRoutine = r || null;
  $("#routine-dialog-title").textContent = r ? "Edit routine" : "New routine";
  $("#rt-bot").replaceChildren(...state.bots.map((b) => h("option", { value: b.id }, b.name)));
  $("#rt-name").value = r?.name || ""; $("#rt-prompt").value = r?.prompt || ""; $("#rt-bot").value = r?.botId || state.botId;
  const s = r?.schedule || { type: "daily", time: "09:00" };
  $("#rt-type").value = s.type; $("#rt-time").value = s.time || "09:00"; $("#rt-every").value = s.everyMinutes || 60;
  const at = s.at ? new Date(s.at) : new Date(Date.now() + 3600e3); at.setSeconds(0, 0);
  $("#rt-at").value = new Date(at.getTime() - at.getTimezoneOffset() * 60e3).toISOString().slice(0, 16);
  pickedDays = new Set(s.weekdays || []); renderDays(); syncRoutineType();
  $("#rt-msg").textContent = ""; $("#rt-delete").hidden = !r; $("#routine-dialog").showModal();
}
function renderDays() { $("#rt-days").replaceChildren(...DAYS.map((d, i) => h("button", { type: "button", class: "chip" + (pickedDays.has(i) ? " on" : ""), onclick: () => { pickedDays.has(i) ? pickedDays.delete(i) : pickedDays.add(i); renderDays(); } }, d))); }
function syncRoutineType() { const t = $("#rt-type").value; $("#rt-time-wrap").hidden = t !== "daily"; $("#rt-days-wrap").hidden = t !== "daily"; $("#rt-every-wrap").hidden = t !== "interval"; $("#rt-at-wrap").hidden = t !== "once"; }
$("#rt-type").onchange = syncRoutineType;
$("#routine-new").onclick = () => openRoutineDialog(null);
$("#bot-routine-add").onclick = () => openRoutineDialog(null);
$("#routine-form").onsubmit = async (e) => {
  if (e.submitter?.value !== "save") return;
  e.preventDefault();
  const t = $("#rt-type").value;
  const schedule = t === "daily" ? { type: t, time: $("#rt-time").value, weekdays: [...pickedDays].sort() } : t === "interval" ? { type: t, everyMinutes: Number($("#rt-every").value) } : { type: t, at: new Date($("#rt-at").value).getTime() };
  const body = { name: $("#rt-name").value.trim(), prompt: $("#rt-prompt").value.trim(), botId: $("#rt-bot").value, schedule };
  if (!body.name || !body.prompt) { $("#rt-msg").textContent = "Name and prompt are required."; return; }
  const btn = $("#rt-save"); btn.classList.add("loading");
  try { editingRoutine ? await api(`/api/routines/${enc(editingRoutine.id)}`, "PATCH", body) : await api("/api/routines", "POST", body); $("#routine-dialog").close(); loadRoutines(); }
  catch (err) { $("#rt-msg").textContent = err.message; }
  finally { btn.classList.remove("loading"); }
};
$("#rt-delete").onclick = async () => { if (!editingRoutine || !confirm(`Delete ${editingRoutine.name}?`)) return; await api(`/api/routines/${enc(editingRoutine.id)}`, "DELETE").catch((err) => wsError(err.message)); $("#routine-dialog").close(); loadRoutines(); };

// ---------- profile menu ----------
$("#profile").onclick = (e) => { e.stopPropagation(); const m = $("#profile-menu"); m.hidden = !m.hidden; };
$("#profile-menu").onclick = (e) => {
  const act = e.target.closest("[data-act]")?.dataset.act; if (!act) return;
  $("#profile-menu").hidden = true;
  if (act === "providers") openProviders();
  if (act === "routines") selectTab("routines");
  if (act === "workspaces") $("#back").click();
  if (act === "signout") signOut();
};
$("#open-providers").onclick = () => openProviders();

// ---------- providers (the CLI's own setup over ACP) ----------
const prov = { status: null, pick: null, poll: null };
async function openProviders() {
  $("#prov-dialog").showModal();
  $("#prov-detail").replaceChildren(h("div", { class: "empty" }, h("span", { class: "spinner" }), "Loading providers…"));
  try { prov.status = await api("/api/providers"); } catch (err) { $("#prov-detail").replaceChildren(h("div", { class: "empty" }, h("span", { class: "state-bad", html: icon("alert") }), h("strong", {}, "Couldn't load providers"), err.message)); return; }
  const s = prov.status; const cur = (s.providers || []).find((p) => p.id === s.providerId);
  $("#prov-current").textContent = s.configured ? `Current: ${cur?.name || (s.providerId === "custom" ? "Custom endpoint" : s.providerId)}${s.baseUrl ? " · " + s.baseUrl.replace(/^https?:\/\//, "") : ""}` : "Not configured";
  renderProvList(); selectProvider(cur?.id || (s.providerId === "custom" ? "__custom" : (s.providers || [])[0]?.id));
}
$("#prov-close").onclick = () => { stopProvPoll(); $("#prov-dialog").close(); };
$("#prov-dialog").addEventListener("close", stopProvPoll);
function stopProvPoll() { clearTimeout(prov.poll); prov.poll = null; }
const signInKind = (p) => (p.authMethods || []).find((m) => m === "browser" || m === "device");
function renderProvList() {
  const s = prov.status; const list = $("#prov-list"); list.replaceChildren();
  const groups = [["Sign in", (p) => signInKind(p)], ["API key", (p) => !signInKind(p) && !p.keyOptional], ["Local", (p) => !signInKind(p) && p.keyOptional]];
  for (const [label, test] of groups) {
    const items = (s.providers || []).filter(test); if (!items.length) continue;
    list.append(h("div", { class: "side-label", style: { padding: "10px 8px 4px" } }, label));
    for (const p of items) {
      const active = s.providerId === p.id; const signed = p.signedIn === true || s.oauth?.[p.id]?.signedIn;
      list.append(h("button", { class: "li" + (prov.pick === p.id ? " on" : ""), type: "button", "data-provider": p.id, onclick: () => selectProvider(p.id) }, h("span", { class: "t" }, p.name), active ? h("span", { class: "tag ok" }, "In use") : signed ? h("span", { class: "tag" }, "Signed in") : null));
    }
  }
  list.append(h("div", { class: "side-label", style: { padding: "10px 8px 4px" } }, "Other"),
    h("button", { class: "li" + (prov.pick === "__custom" ? " on" : ""), type: "button", "data-provider": "__custom", onclick: () => selectProvider("__custom") }, h("span", { class: "t" }, "Custom endpoint"), s.providerId === "custom" ? h("span", { class: "tag ok" }, "In use") : null));
}
function selectProvider(id) {
  stopProvPoll(); prov.pick = id; renderProvList();
  const s = prov.status; const custom = id === "__custom";
  const p = custom ? { id: "", name: "Custom endpoint", description: "Any OpenAI- or Anthropic-compatible API.", baseUrl: s.providerId === "custom" ? s.baseUrl || "" : "", curated: [], authMethods: ["api_key"] } : (s.providers || []).find((x) => x.id === id);
  if (!p) return;
  const inUse = custom ? s.providerId === "custom" : s.providerId === p.id;
  const d = $("#prov-detail"); d.replaceChildren();
  d.append(h("div", {}, h("h4", {}, p.name), h("div", { class: "muted", style: { fontSize: "13px", marginTop: "2px" } }, p.description || "")));
  const msg = h("div", { class: "error-text" });
  const modelSel = h("select", { class: "input", id: "prov-model" });
  const fillModels = (models) => {
    const ids = models.length ? models : (p.curated || []).map((m) => ({ id: m, name: m }));
    modelSel.replaceChildren(...ids.map((m) => h("option", { value: m.id }, m.name && m.name !== m.id ? `${m.id} · ${m.name}` : m.id)));
    if (p.defaultModel && ids.some((m) => m.id === p.defaultModel)) modelSel.value = p.defaultModel;
  };
  fillModels([]);
  const kind = signInKind(p);
  const key = h("input", { class: "input", id: "prov-key", type: "password", autocomplete: "off", spellcheck: "false", placeholder: inUse && s.hasApiKey ? "Saved key kept unless you paste a new one" : p.keyOptional ? "Optional" : "Paste your API key" });
  const base = h("input", { class: "input", id: "prov-base", value: p.baseUrl || "", placeholder: "https://api.example.com/v1", spellcheck: "false" });
  const format = h("select", { class: "input", id: "prov-format" }, h("option", { value: "openai" }, "OpenAI-compatible (Chat Completions)"), h("option", { value: "anthropic" }, "Anthropic Messages"));
  const params = () => {
    const out = {}; if (!custom) out.providerId = p.id;
    const b = base.value.trim(); if (custom || (b && b !== p.baseUrl)) out.baseUrl = b;
    const k = key.value.trim(); if (k) out.apiKey = k;
    if (custom) out.format = format.value;
    return out;
  };
  const loadBtn = h("button", { class: "btn sm", type: "button", onclick: async () => {
    loadBtn.classList.add("loading"); msg.textContent = "";
    try { const { models = [] } = await api("/api/providers/models", "POST", params()); fillModels(models); if (!models.length) msg.textContent = "The provider returned no models; pick a suggested one or type an id."; }
    catch (err) { msg.textContent = err.message; } finally { loadBtn.classList.remove("loading"); }
  } }, h("span", { html: icon("refresh", "sm") }), "Load models");
  const customModel = h("input", { class: "input", placeholder: "Or type a model id", spellcheck: "false" });
  const save = h("button", { class: "btn primary sm", type: "button", "data-save": "provider", onclick: async () => {
    const modelId = customModel.value.trim() || modelSel.value; if (!modelId) { msg.textContent = "Choose a model."; return; }
    if (!kind && !p.keyOptional && !key.value.trim() && !(inUse && s.hasApiKey)) { msg.textContent = "Paste an API key."; return; }
    save.classList.add("loading"); msg.textContent = "";
    try {
      const out = await api("/api/providers/save", "POST", { ...params(), modelId });
      key.value = ""; toast(`Using ${p.name} · ${out.modelId || modelId}`);
      await loadModels(); prov.status = await api("/api/providers"); renderProvList();
      $("#prov-current").textContent = `Current: ${p.name}`;
      if (state.threadId) api(`/api/threads/${enc(state.threadId)}/session`).then((st) => { state.sessions.set(state.threadId, st); renderComposer(); }).catch(() => {});
    } catch (err) { msg.textContent = err.message; } finally { save.classList.remove("loading"); }
  } }, inUse ? "Save changes" : "Use this provider");

  if (kind === "browser" || kind === "device") {
    const signed = p.signedIn === true || s.oauth?.[p.id]?.signedIn;
    const area = h("div", { style: { display: "grid", gap: "10px" } });
    const start = h("button", { class: "btn sm primary", type: "button", onclick: async () => {
      start.classList.add("loading"); msg.textContent = "";
      try {
        const out = await api("/api/providers/auth/start", "POST", { providerId: p.id, kind });
        area.replaceChildren();
        if (out.userCode) area.append(h("div", { class: "code-box" }, h("span", {}, out.userCode), h("button", { class: "btn ghost sm", type: "button", onclick: () => navigator.clipboard?.writeText(out.userCode) }, "Copy")));
        const link = out.url || out.verificationUriComplete || out.verificationUri;
        if (link) {
          area.append(h("button", { class: "btn sm", type: "button", onclick: () => openExternal(link) }, h("span", { html: icon("external", "sm") }), "Open sign-in page"));
          openExternal(link);
        }
        const waiting = h("div", { class: "row muted", style: { fontSize: "13px" } }, h("span", { class: "spinner" }), "Waiting for you to finish signing in…");
        const cancel = h("button", { class: "btn sm ghost", type: "button", onclick: async () => { stopProvPoll(); await api("/api/providers/auth/cancel", "POST", { loginId: out.loginId }).catch(() => {}); selectProvider(p.id); } }, "Cancel");
        area.append(h("div", { class: "row" }, waiting, h("span", { class: "grow" }), cancel));
        const poll = async () => {
          try {
            const r = await api("/api/providers/auth/poll", "POST", { loginId: out.loginId });
            const st = String(r.status || "").toLowerCase();
            if (st === "done" || st === "complete" || st === "completed" || st === "success" || r.signedIn === true) { stopProvPoll(); toast(`Signed in to ${p.name}`); prov.status = await api("/api/providers"); selectProvider(p.id); return; }
            if (st === "expired" || st === "error" || st === "denied" || st === "cancelled" || st === "failed") { stopProvPoll(); msg.textContent = r.error || r.message || `Sign-in ${st}.`; return; }
            prov.poll = setTimeout(poll, Math.max(2, Number(r.interval || out.interval || 3)) * 1000);
          } catch (err) { msg.textContent = err.message; prov.poll = setTimeout(poll, 5000); }
        };
        prov.poll = setTimeout(poll, Math.max(2, Number(out.interval || 3)) * 1000);
      } catch (err) { msg.textContent = err.message; } finally { start.classList.remove("loading"); }
    } }, h("span", { html: icon(kind === "browser" ? "external" : "key", "sm") }), signed ? "Sign in again" : kind === "browser" ? "Sign in with browser" : "Sign in with a code");
    area.append(h("div", { class: "row" }, signed ? h("span", { class: "tag ok" }, "Signed in") : h("span", { class: "tag" }, "Not signed in"), h("span", { class: "grow" }), start));
    d.append(area);
  } else {
    d.append(h("label", { class: "field" }, h("span", {}, "API key"), key, h("span", { class: "hint" }, "Stored in the workspace's Supercharge config, readable only by you. It is never shown again.")));
  }
  d.append(h("label", { class: "field" }, h("span", {}, "Base URL"), base, h("span", { class: "hint" }, custom ? "Your provider's API root." : "Change only to use a proxy or a regional endpoint.")));
  if (custom) d.append(h("label", { class: "field" }, h("span", {}, "API format"), format));
  d.append(h("div", { class: "field" }, h("span", {}, "Model"), h("div", { class: "row" }, h("div", { class: "grow" }, modelSel), loadBtn), customModel));
  d.append(msg, h("div", { class: "row" }, h("span", { class: "grow faint", style: { fontSize: "12px" } }, inUse ? "This provider is in use." : ""), save));
}

// ---------- desktop: live view + take control ----------
const desk = { live: false, version: 0, url: "", control: null, rfb: null, timer: null };
const setDesk = (text, kind) => { setStatus($("#desk-dot"), $("#desk-state"), text, kind); renderComputer(); };
function stagePlaceholder(text) { const p = $("#desk-placeholder"); p.lastChild.textContent = text; p.hidden = !text; }
async function startDesktop() {
  if (desk.control) return;
  try { const status = await cloud(inst("/computer/status?botId=main")); if (status.handoff && !desk.control) setDesk("Someone else has control", "warn"); } catch { /* informational */ }
  startLive();
}
async function startLive() {
  if (desk.live || desk.control) return;
  desk.live = true; setDesk("Live view", "ok");
  $("#vnc").hidden = true;
  while (desk.live && state.ws && !document.hidden) {
    try {
      const res = await fetch(`${state.ws.host}/v1/computer/frame?after=${desk.version}`, { headers: { authorization: "Bearer " + state.ws.session } });
      if (!desk.live) break;
      if (res.status === 401) { await reattach(); continue; }
      if (res.status === 200) {
        desk.version = Number(res.headers.get("x-frame-version")) || desk.version + 1;
        const url = URL.createObjectURL(await res.blob());
        const img = $("#live"); img.src = url; img.hidden = false; stagePlaceholder("");
        if (desk.url) URL.revokeObjectURL(desk.url); desk.url = url;
      } else if (res.status !== 204) {
        const body = await res.json().catch(() => ({}));
        stagePlaceholder(body.error || "The desktop is starting…"); await sleep(1500);
      }
    } catch { stagePlaceholder("Reconnecting to the desktop…"); await sleep(1500); }
  }
  desk.live = false;
}
function stopLive() { desk.live = false; }
document.addEventListener("visibilitychange", () => { if (!document.hidden && state.tab === "desktop" && state.ws) startLive(); });
async function vncTicket() {
  const t = await cloud(inst("/computer/vnc-ticket"), { method: "POST", body: JSON.stringify({ handoffId: desk.control.id, botId: "main" }) });
  if (!t.wsUrl) throw new Error(t.error || "Desktop connection unavailable");
  return t.wsUrl;
}
let RFB;
async function connectVnc() {
  RFB ||= globalThis.NovncRFB?.default || globalThis.NovncRFB;
  if (typeof RFB !== "function") throw new Error("Desktop viewer failed to load");
  desk.rfb?.disconnect();
  const view = $("#vnc"); view.replaceChildren(); view.hidden = false; $("#live").hidden = true; stagePlaceholder("");
  const rfb = new RFB(view, await vncTicket(), { shared: true, credentials: {} });
  desk.rfb = rfb;
  rfb.scaleViewport = true; rfb.resizeSession = false; rfb.qualityLevel = 8; rfb.compressionLevel = 2; rfb.focusOnClick = true;
  rfb.addEventListener("connect", () => { setDesk("You have control", "ok"); $("#reconnect").hidden = true; rfb.focus(); });
  rfb.addEventListener("disconnect", () => { if (desk.rfb === rfb && desk.control) { setDesk("Disconnected", "bad"); $("#reconnect").hidden = false; } });
  rfb.addEventListener("clipboard", (e) => { $("#clip-text").value = e.detail.text; });
}
$("#takeover").onclick = async () => {
  if (desk.control) return;
  const btn = $("#takeover"); btn.classList.add("loading"); wsError("");
  const id = crypto.randomUUID().replace(/-/g, "");
  try {
    const r = await cloud(inst("/computer/handoff"), { method: "POST", body: JSON.stringify({ action: "start", minutes: 15, handoffId: id, botId: "main" }) });
    if (r.ok !== true || r.handoff !== true) throw new Error(r.error || "Takeover could not be confirmed");
    stopLive();
    desk.control = { id, expiresAt: Number(r.expiresAt) > 0 ? Number(r.expiresAt) : Date.now() + 15 * 60_000 };
    $("#return-control").hidden = false; btn.hidden = true; $("#clip").hidden = false; $("#control-banner").hidden = false; $("#stage").classList.add("control");
    setDesk("Connecting…", "warn pulse");
    const tick = () => {
      const left = Math.max(0, desk.control.expiresAt - Date.now());
      $("#desk-timer").textContent = `${Math.floor(left / 60000)}:${String(Math.floor(left / 1000) % 60).padStart(2, "0")} left`;
      if (!left) endControl({ expired: true });
    };
    tick(); desk.timer = setInterval(tick, 1000);
    desk.clipSeen = null; desk.clipTimer = setInterval(pullClipboard, 1500); pullClipboard();
    await connectVnc();
  } catch (err) { wsError(err.message); }
  finally { btn.classList.remove("loading"); }
};
$("#reconnect").onclick = () => connectVnc().catch((err) => wsError(err.message));
async function endControl({ silent = false, expired = false } = {}) {
  const control = desk.control; if (!control) return;
  desk.control = null; clearInterval(desk.timer); clearInterval(desk.clipTimer); desk.rfb?.disconnect(); desk.rfb = null;
  $("#vnc").replaceChildren(); $("#vnc").hidden = true; $("#desk-timer").textContent = "";
  $("#return-control").hidden = true; $("#reconnect").hidden = true; $("#takeover").hidden = false; $("#clip").hidden = true; $("#control-banner").hidden = true; $("#stage").classList.remove("control");
  if (state.ws) {
    try { await fetch(CLOUD + inst("/computer/handoff"), { method: "POST", keepalive: true, headers: { "content-type": "application/json", authorization: "Bearer " + state.token }, body: JSON.stringify({ action: "stop", handoffId: control.id, botId: "main" }) }); }
    catch (err) { if (!silent) wsError(err.message); }
  }
  if (!silent) {
    toast(expired ? "Control expired · returned to the agent" : "Control returned to the agent");
    if (state.tab === "desktop") startLive(); else setDesk("Agent in control", "ok");
  }
}
$("#return-control").onclick = () => endControl();
addEventListener("pagehide", () => endControl({ silent: true }));
async function pullClipboard() {
  if (!desk.control || !state.ws) return;
  try {
    const { text } = await host("/v1/computer/clipboard");
    if (typeof text !== "string" || text === desk.clipSeen) return;
    desk.clipSeen = text;
    if (document.activeElement !== $("#clip-text")) $("#clip-text").value = text;
  } catch { /* the clipboard is a convenience */ }
}
$("#clip-send").onclick = async () => {
  const text = $("#clip-text").value, btn = $("#clip-send");
  try { await host("/v1/computer/clipboard", { method: "POST", body: JSON.stringify({ text }) }); desk.clipSeen = text; btn.textContent = "Sent"; }
  catch { desk.rfb?.clipboardPasteFrom(text); btn.textContent = "Sent"; }
  setTimeout(() => { btn.textContent = "Send to desktop"; }, 1500);
};
$("#clip-copy").onclick = () => { navigator.clipboard?.writeText($("#clip-text").value); toast("Copied"); };
$("#fullscreen").onclick = () => { const s = $("#stage"); document.fullscreenElement ? document.exitFullscreen() : s.requestFullscreen?.(); };

// ---------- preview ----------
let previewUrl = "";
async function refreshPorts() {
  try {
    const { ports = [] } = await host("/v1/ports");
    const chips = $("#ports"); chips.replaceChildren();
    for (const p of ports) chips.append(h("button", { type: "button", class: "chip" + (String(p) === $("#port").value && previewUrl ? " on" : ""), onclick: () => { $("#port").value = p; $("#preview-form").requestSubmit(); } }, ":" + p));
    if (!ports.length) chips.append(h("span", { class: "faint", style: { fontSize: "12.5px" } }, "No app is listening yet"));
  } catch { /* chips are a convenience */ }
}
$("#preview-form").onsubmit = async (e) => {
  e.preventDefault();
  const port = Number($("#port").value); const note = $("#preview-note");
  if (!Number.isInteger(port) || port < 1 || port > 65535) { note.hidden = false; note.textContent = "Enter a port between 1 and 65535."; return; }
  const btn = e.submitter; btn?.classList.add("loading");
  try {
    const check = await fetch(`${state.ws.host}/v1/preview?port=${port}`, { headers: { authorization: "Bearer " + state.ws.session } }).then((r) => r.json());
    const { host: url } = await cloud(inst(`/preview?port=${port}`));
    previewUrl = !url ? "" : /^https?:\/\//i.test(url) ? url : "https://" + url;
    note.hidden = !!check.listening; note.textContent = check.listening ? "" : `Nothing is listening on port ${port} yet. Start your app in the terminal or ask a bot, then reload.`;
    $("#preview-frame").src = previewUrl; $("#preview-empty").hidden = !!previewUrl;
    refreshPorts();
  } catch (err) { note.hidden = false; note.textContent = err.message; }
  finally { btn?.classList.remove("loading"); }
};
$("#preview-reload").onclick = () => { if (previewUrl) $("#preview-frame").src = previewUrl + (previewUrl.includes("?") ? "&" : "?") + "_r=" + Date.now(); };
$("#preview-open").onclick = () => { if (previewUrl) openExternal(previewUrl); };

// ---------- terminal ----------
const term = { xterm: null, fit: null, socket: null, retry: 0, timer: null, wanted: false };
const setTerm = (text, kind) => setStatus($("#term-dot"), $("#term-state"), text, kind);
function terminalId() {
  const key = "vb.term." + state.ws.id;
  let id = sessionStorage.getItem(key);
  if (!id) { id = crypto.randomUUID().replace(/-/g, ""); sessionStorage.setItem(key, id); }
  return id;
}
async function openTerminal() {
  if (!term.xterm) {
    const Terminal = globalThis.Terminal;
    const FitAddon = globalThis.FitAddon?.FitAddon || globalThis.FitAddon;
    if (!Terminal || !FitAddon) throw new Error("Terminal failed to load");
    term.xterm = new Terminal({ cursorBlink: true, fontSize: 13, lineHeight: 1.25, fontFamily: "JetBrains Mono, ui-monospace, SFMono-Regular, Menlo, monospace", theme: { background: "#08080a", foreground: "#e6e6e9", cursor: "#24b469", selectionBackground: "#2b3a31", black: "#1a1a1d", brightBlack: "#5a5a62", green: "#33c57a", brightGreen: "#5ee39a", red: "#ef6461", yellow: "#e2a640", blue: "#6aa1ff", magenta: "#b392f0", cyan: "#4fc3d0" }, scrollback: 5000 });
    term.fit = new FitAddon(); term.xterm.loadAddon(term.fit); term.xterm.open($("#term"));
    term.xterm.onData((d) => { if (term.socket?.readyState === 1) term.socket.send(d); });
    term.xterm.onResize(({ cols, rows }) => { if (term.socket?.readyState === 1) term.socket.send(JSON.stringify({ type: "resize", cols, rows })); });
    new ResizeObserver(() => { if (!$("#terminal").hidden) term.fit.fit(); }).observe($("#term"));
  }
  term.wanted = true;
  requestAnimationFrame(() => term.fit.fit());
  if (!term.socket || term.socket.readyState > 1) connectTerminal();
  term.xterm.focus();
}
function connectTerminal() {
  clearTimeout(term.timer);
  if (!state.ws?.session) return;
  const url = new URL(state.ws.host); url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  url.pathname = "/v1/terminal"; url.search = `token=${enc(state.ws.session)}&session=${enc(terminalId())}`;
  term.socket?.close();
  const socket = new WebSocket(url); socket.binaryType = "arraybuffer"; term.socket = socket;
  setTerm("Connecting…", "warn pulse");
  socket.onmessage = (e) => {
    if (typeof e.data === "string") {
      const msg = JSON.parse(e.data);
      if (msg.type === "attached") {
        term.retry = 0; setTerm(msg.resumed ? "Connected · resumed" : "Connected", "ok");
        if (!msg.resumed) term.xterm.clear(); else term.xterm.reset();
        socket.send(JSON.stringify({ type: "resize", cols: term.xterm.cols, rows: term.xterm.rows }));
      } else if (msg.type === "exit") { setTerm("Shell exited", "warn"); sessionStorage.removeItem("vb.term." + state.ws.id); }
      return;
    }
    term.xterm.write(new Uint8Array(e.data));
  };
  socket.onclose = (e) => {
    if (term.socket !== socket) return;
    if (e.reason === "opened on another screen") { setTerm("Opened in another window", "warn"); return; }
    setTerm("Disconnected", "bad");
    if (term.wanted && state.ws) { const wait = Math.min(15000, 500 * 2 ** term.retry++); term.timer = setTimeout(connectTerminal, wait); setTerm(`Reconnecting in ${Math.round(wait / 1000)}s`, "warn"); }
  };
}
function closeTerminal() { term.wanted = false; clearTimeout(term.timer); const s = term.socket; term.socket = null; s?.close(); term.xterm?.reset(); }
$("#term-reconnect").onclick = () => { term.retry = 0; term.wanted = true; connectTerminal(); };
$("#term-new").onclick = () => { if (state.ws) sessionStorage.removeItem("vb.term." + state.ws.id); term.xterm?.reset(); term.retry = 0; term.wanted = true; connectTerminal(); };

// ---------- keyboard ----------
document.addEventListener("keydown", (e) => {
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k" && !$("#workspace").hidden) { e.preventDefault(); $("#search-wrap").hidden = false; $("#bot-search").focus(); }
});

// ---------- start ----------
// Models and routines load once the workspace is open.
const baseOpen = openWorkspace;
async function openAndLoad(item) { await baseOpen(item); if (state.ws?.session) { loadModels(); loadRoutines(); renderComputer(); } }
window.__openWorkspace = openAndLoad;
if (state.token) loadList(); else show("login");
