const els = {
  searchHistorySummary: document.getElementById("search-history-summary"),
  searchHistoryChart: document.getElementById("search-history-chart"),
  clearHistory: document.getElementById("clear-history"),
  groupsSummary: document.getElementById("groups-summary"),
  groupsChart: document.getElementById("groups-chart"),
  topTags: document.getElementById("top-tags"),
  allSearches: document.getElementById("all-searches"),
  status: document.getElementById("status"),
};

init();

async function init() {
  try {
    setStatus("Lade Statistiken...");

    const [payload] = await Promise.all([request("/api/state")]);
    const groups = payload.groups || [];

    renderSearchHistory();
    renderGroupsChart(groups);
    renderTopTags(groups);

    els.clearHistory.addEventListener("click", () => {
      if (!confirm("Suchverlauf wirklich loeschen?")) return;
      localStorage.removeItem("lsz_search_history");
      renderSearchHistory();
    });

    setStatus("Bereit.");
  } catch (error) {
    setStatus(error.message || "Fehler beim Laden der Statistiken.", true);
  }
}

// ─── Suchverlauf ────────────────────────────────────────────────────────────

function loadSearchHistory() {
  try {
    const raw = localStorage.getItem("lsz_search_history");
    const parsed = JSON.parse(raw || "[]");
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

function renderSearchHistory() {
  const history = loadSearchHistory();

  if (history.length === 0) {
    els.searchHistorySummary.textContent = "Noch keine Suchen aufgezeichnet. Suche auf der Startseite, um Daten zu erzeugen.";
    els.searchHistoryChart.innerHTML = '<p class="search-info" style="padding:8px 0;">Keine Daten vorhanden.</p>';
    els.allSearches.innerHTML = '<li>Kein Verlauf vorhanden.</li>';
    return;
  }

  const totalSearches = history.reduce((sum, e) => sum + e.count, 0);
  els.searchHistorySummary.textContent = `${history.length} verschiedene Suchbegriffe, ${totalSearches} Suchanfragen gesamt.`;

  // Top 15 nach Anzahl
  const top = [...history].sort((a, b) => b.count - a.count).slice(0, 15);
  const max = top[0]?.count || 1;

  els.searchHistoryChart.innerHTML = "";
  for (const item of top) {
    const pct = Math.round((item.count / max) * 100);
    els.searchHistoryChart.appendChild(buildBarRow(escapeHTML(item.term), item.count, pct, "bar-accent"));
  }

  // Vollstaendige Liste, neueste zuerst
  const byDate = [...history].sort((a, b) => b.last - a.last);
  els.allSearches.innerHTML = "";
  for (const item of byDate) {
    const li = document.createElement("li");
    li.innerHTML = `<span>${escapeHTML(item.term)}</span> <small>${item.count}x &middot; ${formatDate(item.last)}</small>`;
    els.allSearches.appendChild(li);
  }
}

// ─── Gruppen-Chart ──────────────────────────────────────────────────────────

function renderGroupsChart(groups) {
  const items = groups
    .map((g) => ({
      name: g.name || "Unbenannt",
      total: (g.bookmarks || []).length,
      active: (g.bookmarks || []).filter((b) => !b.archived).length,
      archived: (g.bookmarks || []).filter((b) => Boolean(b.archived)).length,
    }))
    .filter((g) => g.total > 0)
    .sort((a, b) => b.total - a.total);

  if (items.length === 0) {
    els.groupsSummary.textContent = "Noch keine Lesezeichen vorhanden.";
    els.groupsChart.innerHTML = '<p class="search-info" style="padding:8px 0;">Keine Daten vorhanden.</p>';
    return;
  }

  const totalBookmarks = items.reduce((sum, g) => sum + g.total, 0);
  els.groupsSummary.textContent = `${items.length} Gruppen mit ${totalBookmarks} Lesezeichen gesamt.`;

  const max = items[0]?.total || 1;
  els.groupsChart.innerHTML = "";

  for (const item of items) {
    const pct = Math.round((item.total / max) * 100);
    els.groupsChart.appendChild(buildBarRow(escapeHTML(item.name), item.total, pct, "bar-group",
      item.archived > 0 ? ` <small style="color:var(--muted)">(${item.active} aktiv / ${item.archived} archiviert)</small>` : ""));
  }
}

// ─── Top Tags ───────────────────────────────────────────────────────────────

function renderTopTags(groups) {
  const allTags = groups
    .flatMap((g) => g.bookmarks || [])
    .flatMap((b) => b.tags || [])
    .map((t) => String(t).trim())
    .filter(Boolean);

  const counts = new Map();
  for (const tag of allTags) {
    const key = tag.toLowerCase();
    const existing = counts.get(key);
    if (existing) {
      existing.count += 1;
    } else {
      counts.set(key, { display: tag, count: 1 });
    }
  }

  const top = [...counts.values()].sort((a, b) => b.count - a.count).slice(0, 15);
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

// ─── Hilfsfunktionen ────────────────────────────────────────────────────────

function buildBarRow(label, count, pct, barClass, extraHTML = "") {
  const row = document.createElement("div");
  row.className = "bar-row";

  const labelEl = document.createElement("span");
  labelEl.className = "bar-label";
  labelEl.innerHTML = label + extraHTML;

  const track = document.createElement("div");
  track.className = "bar-track";

  const fill = document.createElement("div");
  fill.className = `bar-fill ${barClass}`;
  fill.style.width = `${pct}%`;

  const countEl = document.createElement("span");
  countEl.className = "bar-count";
  countEl.textContent = String(count);

  track.appendChild(fill);
  row.appendChild(labelEl);
  row.appendChild(track);
  row.appendChild(countEl);
  return row;
}

function formatDate(ts) {
  if (!ts) return "";
  const d = new Date(ts);
  return d.toLocaleDateString("de-DE", { day: "2-digit", month: "2-digit", year: "numeric" });
}

function escapeHTML(value) {
  return String(value ?? "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

async function request(path, options = {}) {
  const response = await fetch(path, {
    headers: { "Content-Type": "application/json", ...(options.headers || {}) },
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
