const els = {
  statsGrid: document.getElementById("stats-grid"),
  largestGroups: document.getElementById("largest-groups"),
  topTags: document.getElementById("top-tags"),
  reminderSummary: document.getElementById("reminder-summary"),
  archiveSummary: document.getElementById("archive-summary"),
  status: document.getElementById("status"),
};

init();

async function init() {
  try {
    setStatus("Lade Uebersicht...");
    const payload = await request("/api/state");
    const groups = payload.groups || [];
    renderOverview(groups);
    setStatus("Bereit.");
  } catch (error) {
    setStatus(error.message || "Fehler beim Laden der Uebersicht.", true);
  }
}

function renderOverview(groups) {
  const bookmarks = groups.flatMap((group) => group.bookmarks || []);
  const favorites = bookmarks.filter((bookmark) => Boolean(bookmark.favorite)).length;
  const pinned = bookmarks.filter((bookmark) => Boolean(bookmark.pinned)).length;
  const withNotes = bookmarks.filter((bookmark) => String(bookmark.notes || "").trim().length > 0).length;
  const archived = bookmarks.filter((bookmark) => Boolean(bookmark.archived)).length;
  const active = bookmarks.length - archived;
  const archiveRatio = bookmarks.length > 0 ? Math.round((archived / bookmarks.length) * 100) : 0;

  const reminders = bookmarks
    .filter((bookmark) => !bookmark.archived)
    .filter((bookmark) => parseBookmarkDate(bookmark.remind_at));
  const dueSoon = reminders.filter((bookmark) => getDaysUntil(parseBookmarkDate(bookmark.remind_at)) <= 3).length;
  const overdue = reminders.filter((bookmark) => getDaysUntil(parseBookmarkDate(bookmark.remind_at)) < 0).length;

  const allTags = bookmarks.flatMap((bookmark) => bookmark.tags || []).map((tag) => String(tag).trim()).filter(Boolean);
  const uniqueTagCount = new Set(allTags.map((tag) => tag.toLowerCase())).size;

  const stats = [
    { label: "Gruppen", value: groups.length },
    { label: "Lesezeichen", value: bookmarks.length },
    { label: "Aktiv", value: active },
    { label: "Archiv", value: archived },
    { label: "Archivquote", value: `${archiveRatio}%` },
    { label: "Favoriten", value: favorites },
    { label: "Angepinnt", value: pinned },
    { label: "Mit Notiz", value: withNotes },
    { label: "Mit Datum", value: reminders.length },
    { label: "Bald/faellig", value: dueSoon },
    { label: "Tags gesamt", value: uniqueTagCount },
  ];

  els.statsGrid.innerHTML = "";
  for (const item of stats) {
    const card = document.createElement("article");
    card.className = "stat-card";

    const label = document.createElement("p");
    label.textContent = item.label;

    const value = document.createElement("strong");
    value.textContent = String(item.value);

    card.appendChild(label);
    card.appendChild(value);
    els.statsGrid.appendChild(card);
  }

  renderLargestGroups(groups);
  renderTopTags(allTags);
  renderRemindersSummary(reminders.length, dueSoon, overdue);
  renderArchiveSummary(groups, archived, active, archiveRatio);
}

function renderLargestGroups(groups) {
  const largest = [...groups]
    .map((group) => ({
      name: group.name || "Unbenannt",
      count: (group.bookmarks || []).length,
    }))
    .sort((a, b) => b.count - a.count)
    .slice(0, 5);

  els.largestGroups.innerHTML = "";

  if (largest.length === 0) {
    const li = document.createElement("li");
    li.textContent = "Noch keine Gruppen vorhanden.";
    els.largestGroups.appendChild(li);
    return;
  }

  for (const item of largest) {
    const li = document.createElement("li");
    li.textContent = `${item.name} (${item.count})`;
    els.largestGroups.appendChild(li);
  }
}

function renderTopTags(allTags) {
  const counts = new Map();
  for (const tagRaw of allTags) {
    const key = tagRaw.toLowerCase();
    const existing = counts.get(key);
    if (existing) {
      existing.count += 1;
      continue;
    }
    counts.set(key, { display: tagRaw, count: 1 });
  }

  const top = [...counts.values()].sort((a, b) => b.count - a.count).slice(0, 8);
  els.topTags.innerHTML = "";

  if (top.length === 0) {
    const li = document.createElement("li");
    li.textContent = "Noch keine Tags vorhanden.";
    els.topTags.appendChild(li);
    return;
  }

  for (const tag of top) {
    const li = document.createElement("li");
    li.innerHTML = `<span>#${escapeHTML(tag.display)}</span> <small>${tag.count}x</small>`;
    els.topTags.appendChild(li);
  }
}

function renderRemindersSummary(total, dueSoon, overdue) {
  els.reminderSummary.innerHTML = "";

  const entries = [
    `Gesamt mit Datum: ${total}`,
    `Bald oder heute: ${Math.max(0, dueSoon - overdue)}`,
    `Ueberfaellig: ${overdue}`,
  ];

  for (const entry of entries) {
    const li = document.createElement("li");
    li.textContent = entry;
    els.reminderSummary.appendChild(li);
  }
}

function renderArchiveSummary(groups, archivedTotal, activeTotal, archiveRatio) {
  els.archiveSummary.innerHTML = "";

  const topArchivedGroups = [...groups]
    .map((group) => ({
      name: group.name || "Unbenannt",
      archived: (group.bookmarks || []).filter((bookmark) => Boolean(bookmark.archived)).length,
    }))
    .filter((group) => group.archived > 0)
    .sort((a, b) => b.archived - a.archived)
    .slice(0, 3)
    .map((group) => `${group.name}: ${group.archived}`);

  const entries = [
    `Archiviert gesamt: ${archivedTotal}`,
    `Aktiv gesamt: ${activeTotal}`,
    `Archivquote: ${archiveRatio}%`,
    `Top Archiv-Gruppen: ${topArchivedGroups.length > 0 ? topArchivedGroups.join(", ") : "keine"}`,
  ];

  for (const entry of entries) {
    const li = document.createElement("li");
    li.textContent = entry;
    els.archiveSummary.appendChild(li);
  }
}

function parseBookmarkDate(rawValue) {
  if (!rawValue) return null;
  const date = new Date(rawValue);
  if (Number.isNaN(date.getTime())) return null;
  return date;
}

function startOfToday() {
  const date = new Date();
  date.setHours(0, 0, 0, 0);
  return date;
}

function getDaysUntil(date) {
  if (!date) return Number.POSITIVE_INFINITY;
  const target = new Date(date);
  target.setHours(0, 0, 0, 0);
  const today = startOfToday();
  return Math.floor((target.getTime() - today.getTime()) / 86400000);
}

async function request(path, options = {}) {
  const response = await fetch(path, {
    headers: {
      "Content-Type": "application/json",
      ...(options.headers || {}),
    },
    ...options,
  });

  const isJSON = response.headers.get("content-type")?.includes("application/json");
  const payload = isJSON ? await response.json() : null;

  if (!response.ok) {
    throw new Error(payload?.error || `Fehler (${response.status})`);
  }

  return payload;
}

function setStatus(message, isError = false) {
  els.status.textContent = message;
  els.status.style.color = isError ? "#ffb3ba" : "#97a7bf";
}

function escapeHTML(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}
