const els = {
  archiveSummary: document.getElementById("archive-summary"),
  archiveGroups: document.getElementById("archive-groups"),
  status: document.getElementById("status"),
};

init();

async function init() {
  try {
    setStatus("Lade Archiv...");
    const payload = await request("/api/state");
    renderArchive(payload.groups || []);
    setStatus("Bereit.");
  } catch (error) {
    setStatus(error.message || "Fehler beim Laden des Archivs.", true);
  }
}

function renderArchive(groups) {
  const groupedArchive = groups
    .map((group) => ({
      id: group.id,
      name: group.name || "Unbenannt",
      bookmarks: (group.bookmarks || []).filter((bookmark) => Boolean(bookmark.archived)),
    }))
    .filter((group) => group.bookmarks.length > 0);

  const archivedCount = groupedArchive.reduce((sum, group) => sum + group.bookmarks.length, 0);
  els.archiveSummary.textContent = `${archivedCount} archivierte Lesezeichen in ${groupedArchive.length} Gruppen.`;

  els.archiveGroups.innerHTML = "";

  if (groupedArchive.length === 0) {
    const empty = document.createElement("div");
    empty.className = "empty-state";
    empty.innerHTML = '<p>Aktuell sind keine Lesezeichen archiviert.</p><a class="primary-link" href="/">Lesezeichen verwalten</a>';
    els.archiveGroups.appendChild(empty);
    return;
  }

  for (const group of groupedArchive) {
    const card = document.createElement("article");
    card.className = "detail-card";

    const title = document.createElement("h2");
    title.textContent = `${group.name} (${group.bookmarks.length})`;

    const list = document.createElement("ul");
    list.className = "simple-list archive-link-list";

    for (const bookmark of group.bookmarks) {
      const li = document.createElement("li");
      const link = document.createElement("a");
      link.href = bookmark.url;
      link.target = "_blank";
      link.rel = "noreferrer";
      link.textContent = bookmark.title;
      li.appendChild(link);
      list.appendChild(li);
    }

    card.appendChild(title);
    card.appendChild(list);
    els.archiveGroups.appendChild(card);
  }
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
