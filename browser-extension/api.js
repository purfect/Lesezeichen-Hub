// Shared helpers for popup, options page and background (Chrome service worker / Firefox event page).
const ext = globalThis.browser ?? globalThis.chrome;
const DEFAULT_HUB_URL = "http://127.0.0.1:3333";

// The extension only holds host permissions for local hubs.
function normalizeHubUrl(raw) {
  try {
    const url = new URL(String(raw || "").trim());
    if (url.protocol !== "http:") return "";
    if (url.hostname !== "127.0.0.1" && url.hostname !== "localhost") return "";
    return `${url.protocol}//${url.host}`;
  } catch {
    return "";
  }
}

async function getHubUrl() {
  const { hubUrl } = await ext.storage.local.get("hubUrl");
  return normalizeHubUrl(hubUrl) || DEFAULT_HUB_URL;
}

async function hubRequest(path, { method = "GET", body } = {}) {
  const base = await getHubUrl();
  let response;
  try {
    response = await fetch(base + path, {
      method,
      headers: { "Accept": "application/json", "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw new Error(`Hub nicht erreichbar unter ${base}. Läuft der Server?`);
  }
  const text = (await response.text()).replace(/^\uFEFF/, "");
  let payload = null;
  try {
    payload = text ? JSON.parse(text) : null;
  } catch {
    payload = null;
  }
  if (!response.ok) throw new Error(payload?.error || `HTTP ${response.status}`);
  return payload;
}

async function loadGroups() {
  const state = await hubRequest("/api/state");
  return Array.isArray(state?.groups) ? state.groups : [];
}

function realGroups(groups) {
  return groups.filter((group) => group.id > 0);
}

async function preferredGroupId(groups) {
  const { lastGroupId } = await ext.storage.local.get("lastGroupId");
  const candidates = realGroups(groups);
  return (candidates.find((group) => group.id === lastGroupId) || candidates[0])?.id ?? 0;
}

function findBookmarkByUrl(groups, url) {
  for (const group of groups) {
    const bookmark = (group.bookmarks || []).find((item) => item.url === url);
    if (bookmark) return { group, bookmark };
  }
  return null;
}

function parseTags(raw) {
  return [...new Set(String(raw || "").split(",").map((tag) => tag.trim()).filter(Boolean))];
}

function isSavableUrl(url) {
  return /^https?:\/\//i.test(String(url || ""));
}

function noteContent(selection, url) {
  const text = String(selection || "").trim();
  return text ? `${text}\n\nQuelle: ${url}` : `Quelle: ${url}`;
}

function bookmarkPayload({ groupId, title, url, notes = "", tags = [], favorite = false, pinned = false, remindAt = "" }) {
  return {
    group_id: groupId,
    title,
    url,
    notes,
    tags,
    favorite,
    pinned,
    archived: false,
    sort_order: 0,
    remind_at: remindAt,
  };
}
