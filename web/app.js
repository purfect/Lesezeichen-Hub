const state = {
  groups: [],
  search: "",
  collapsedGroupIds: loadCollapsedGroupIds(),
  drag: {
    groupId: null,
    bookmarkId: null,
    bookmarkGroupId: null,
  },
};

const els = {
  groups: document.getElementById("groups"),
  groupForm: document.getElementById("group-form"),
  toggleGroupForm: document.getElementById("toggle-group-form"),
  bookmarkForm: document.getElementById("bookmark-form-global"),
  bookmarkEditDialog: document.getElementById("bookmark-edit-dialog"),
  bookmarkEditForm: document.getElementById("bookmark-edit-form"),
  bookmarkEditCancel: document.getElementById("bookmark-edit-cancel"),
  bookmarkEditGroup: document.getElementById("bookmark-edit-group"),
  bookmarkGroup: document.getElementById("bookmark-group"),
  toggleBookmarkForm: document.getElementById("toggle-bookmark-form"),
  searchInfo: document.getElementById("search-info"),
  favoritesSegment: document.getElementById("favorites-segment"),
  favoritesQuickbar: document.getElementById("favorites-quickbar"),
  groupTemplate: document.getElementById("group-template"),
  bookmarkTemplate: document.getElementById("bookmark-template"),
  exportJSON: document.getElementById("export-json"),
  exportCSV: document.getElementById("export-csv"),
  exportHTML: document.getElementById("export-html"),
  importData: document.getElementById("import-data"),
  importDataFile: document.getElementById("import-data-file"),
  reminderAlertsBlock: document.getElementById("reminder-alerts-block"),
  reminderAlertsList: document.getElementById("reminder-alerts-list"),
  status: document.getElementById("status"),
  reload: document.getElementById("reload"),
  search: document.getElementById("search"),
  searchClear: document.getElementById("search-clear"),
};

init();

function init() {
  els.groupForm.addEventListener("submit", onCreateGroup);
  els.toggleGroupForm.addEventListener("click", toggleGroupForm);
  els.bookmarkForm.addEventListener("submit", onCreateBookmark);
  els.bookmarkEditForm.addEventListener("submit", onSaveEditedBookmark);
  els.bookmarkEditCancel.addEventListener("click", closeBookmarkEditDialog);
  els.toggleBookmarkForm.addEventListener("click", toggleBookmarkForm);
  els.exportJSON.addEventListener("click", () => onExport("json"));
  els.exportCSV.addEventListener("click", () => onExport("csv"));
  els.exportHTML.addEventListener("click", () => onExport("html"));
  els.importData.addEventListener("click", onImportDataClick);
  els.importDataFile.addEventListener("change", onImportDataSelected);
  els.reload.addEventListener("click", () => loadState(true));
  els.search.addEventListener("input", (e) => {
    state.search = e.target.value.trim().toLowerCase();
    updateSearchClearButton();
    render();
  });
  els.searchClear.addEventListener("click", clearSearch);

  updateSearchClearButton();
  loadState();
}

function clearSearch() {
  els.search.value = "";
  state.search = "";
  updateSearchClearButton();
  render();
  els.search.focus();
}

function parseTagsInput(raw) {
  const source = raw?.toString() ?? "";
  const tags = source.split(",").map((t) => t.trim()).filter(Boolean);
  const seen = new Set();
  const out = [];
  for (const tag of tags) {
    const key = tag.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(tag);
  }
  return out;
}

function tagsToInput(tags) {
  return (tags || []).join(", ");
}

function getGroupById(groupID) {
  return state.groups.find((group) => group.id === groupID);
}

function getBookmarkById(bookmarkID) {
  for (const group of state.groups) {
    const found = (group.bookmarks || []).find((bookmark) => bookmark.id === bookmarkID);
    if (found) return found;
  }
  return null;
}

function nextSortOrderForGroup(groupID) {
  const group = getGroupById(groupID);
  if (!group || !group.bookmarks || group.bookmarks.length === 0) {
    return 0;
  }
  return Math.max(...group.bookmarks.map((bookmark) => Number(bookmark.sort_order ?? 0))) + 1;
}

function updateSearchClearButton() {
  const hasValue = els.search.value.trim().length > 0;
  els.searchClear.classList.toggle("hidden", !hasValue);
}

async function onExport(format, groupID = null) {
  try {
    setStatus(`Export (${format.toUpperCase()}) wird erstellt...`);
    const params = new URLSearchParams({ format });
    if (Number.isFinite(groupID) && groupID > 0) {
      params.set("group_id", String(groupID));
    }

    const response = await fetch(`/api/export?${params.toString()}`);
    if (!response.ok) {
      throw new Error(`Fehler (${response.status})`);
    }

    const blob = await response.blob();
    const contentDisposition = response.headers.get("content-disposition") || "";
    const match = contentDisposition.match(/filename="([^"]+)"/);
    const fileName = match?.[1] || `lesezeichen-export-${Date.now()}.json`;

    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = fileName;
    document.body.appendChild(link);
    link.click();
    link.remove();
    URL.revokeObjectURL(url);

    setStatus(`Export (${format.toUpperCase()}) abgeschlossen.`);
  } catch (error) {
    setStatus(error.message || "Export fehlgeschlagen.", true);
  }
}

function onImportDataClick() {
  els.importDataFile.value = "";
  els.importDataFile.click();
}

async function onImportDataSelected(event) {
  const file = event.target.files?.[0];
  if (!file) {
    return;
  }

  try {
    const rawText = await file.text();
    const payload = parseImportPayload(file, rawText);

    setStatus("Import laeuft...");
    await request("/api/import", {
      method: "POST",
      body: JSON.stringify(payload),
    });

    setStatus("Import abgeschlossen.");
    await loadState(true);
  } catch (error) {
    setStatus(error.message || "Import fehlgeschlagen.", true);
  }
}

function parseImportPayload(file, rawText) {
  const lowerName = String(file?.name || "").toLowerCase();
  if (lowerName.endsWith(".csv")) {
    return parseCSVImport(rawText);
  }
  if (lowerName.endsWith(".html") || lowerName.endsWith(".htm")) {
    return parseHTMLImport(rawText);
  }

  const parsed = JSON.parse(rawText);
  if (parsed && Array.isArray(parsed.groups)) {
    return parsed;
  }
  if (parsed && Array.isArray(parsed.dials)) {
    return parsed;
  }
  throw new Error("JSON-Format nicht unterstuetzt.");
}

function parseCSVImport(rawCSV) {
  const rows = parseCSVRows(rawCSV).filter((row) => row.some((cell) => String(cell || "").trim() !== ""));
  if (rows.length <= 1) {
    throw new Error("CSV ist leer oder ungueltig.");
  }

  const header = rows[0].map((cell) => String(cell || "").trim().toLowerCase());
  const idx = {
    groupID: header.indexOf("group_id"),
    groupName: header.indexOf("group_name"),
    groupDescription: header.indexOf("group_description"),
    title: header.indexOf("title"),
    url: header.indexOf("url"),
    notes: header.indexOf("notes"),
    tags: header.indexOf("tags"),
    favorite: header.indexOf("favorite"),
    pinned: header.indexOf("pinned"),
    sortOrder: header.indexOf("sort_order"),
    remindAt: header.indexOf("remind_at"),
  };

  if (idx.groupName < 0 || idx.title < 0 || idx.url < 0) {
    throw new Error("CSV-Header passt nicht zum Exportformat.");
  }

  const groupsMap = new Map();
  let seq = 0;
  for (const row of rows.slice(1)) {
    const groupName = getCSVCell(row, idx.groupName) || "ungruppiert";
    const groupID = getCSVCell(row, idx.groupID);
    const groupKey = groupID || groupName.toLowerCase();

    if (!groupsMap.has(groupKey)) {
      groupsMap.set(groupKey, {
        name: groupName,
        description: getCSVCell(row, idx.groupDescription),
        sort_order: seq++,
        bookmarks: [],
      });
    }

    const title = getCSVCell(row, idx.title);
    const url = getCSVCell(row, idx.url);
    if (!title || !url) {
      continue;
    }

    groupsMap.get(groupKey).bookmarks.push({
      title,
      url,
      notes: getCSVCell(row, idx.notes),
      tags: parseTagsInput(getCSVCell(row, idx.tags)),
      favorite: parseBoolish(getCSVCell(row, idx.favorite)),
      pinned: parseBoolish(getCSVCell(row, idx.pinned)),
      sort_order: parseNumberish(getCSVCell(row, idx.sortOrder), groupsMap.get(groupKey).bookmarks.length),
      remind_at: getCSVCell(row, idx.remindAt),
    });
  }

  const groups = Array.from(groupsMap.values()).filter((group) => group.bookmarks.length > 0);
  if (groups.length === 0) {
    throw new Error("CSV enthaelt keine importierbaren Lesezeichen.");
  }
  return { groups };
}

function parseHTMLImport(rawHTML) {
  const doc = new DOMParser().parseFromString(rawHTML, "text/html");
  const groupNodes = Array.from(doc.querySelectorAll(".group"));
  if (groupNodes.length === 0) {
    throw new Error("HTML-Format nicht erkannt.");
  }

  const groups = groupNodes.map((node, groupIndex) => {
    const name = (node.querySelector("h2")?.textContent || "ungruppiert").trim() || "ungruppiert";
    const description = (node.querySelector(".desc")?.textContent || "").trim();
    const bookmarks = Array.from(node.querySelectorAll("li a"))
      .map((link, bookmarkIndex) => {
        const title = (link.textContent || "").trim();
        const url = (link.getAttribute("href") || "").trim();
        if (!title || !url) return null;
        return {
          title,
          url,
          notes: "",
          tags: [],
          favorite: false,
          pinned: false,
          sort_order: bookmarkIndex,
          remind_at: "",
        };
      })
      .filter(Boolean);

    return {
      name,
      description,
      sort_order: groupIndex,
      bookmarks,
    };
  }).filter((group) => group.bookmarks.length > 0);

  if (groups.length === 0) {
    throw new Error("HTML enthaelt keine importierbaren Lesezeichen.");
  }

  return { groups };
}

function parseCSVRows(rawCSV) {
  const rows = [];
  let row = [];
  let cell = "";
  let inQuotes = false;

  for (let i = 0; i < rawCSV.length; i += 1) {
    const ch = rawCSV[i];

    if (ch === '"') {
      if (inQuotes && rawCSV[i + 1] === '"') {
        cell += '"';
        i += 1;
      } else {
        inQuotes = !inQuotes;
      }
      continue;
    }

    if (ch === "," && !inQuotes) {
      row.push(cell);
      cell = "";
      continue;
    }

    if ((ch === "\n" || ch === "\r") && !inQuotes) {
      if (ch === "\r" && rawCSV[i + 1] === "\n") {
        i += 1;
      }
      row.push(cell);
      rows.push(row);
      row = [];
      cell = "";
      continue;
    }

    cell += ch;
  }

  row.push(cell);
  rows.push(row);
  return rows;
}

function getCSVCell(row, index) {
  if (index < 0 || index >= row.length) return "";
  return String(row[index] || "").trim();
}

function parseBoolish(raw) {
  const normalized = String(raw || "").trim().toLowerCase();
  return normalized === "true" || normalized === "1" || normalized === "yes";
}

function parseNumberish(raw, fallback = 0) {
  const value = Number(raw);
  if (Number.isFinite(value)) return value;
  return fallback;
}

async function loadState(showHint = false) {
  try {
    if (showHint) setStatus("Lade Daten neu...");
    const payload = await request("/api/state");
    state.groups = payload.groups ?? [];
    populateGroupSelect();
    render();
    setStatus(showHint ? "Aktualisiert." : "Bereit.");
  } catch (error) {
    setStatus(error.message, true);
  }
}

function populateGroupSelect() {
  const previousValue = els.bookmarkGroup.value;
  els.bookmarkGroup.innerHTML = "";

  if (state.groups.length === 0) {
    const option = document.createElement("option");
    option.value = "";
    option.textContent = "Bitte zuerst eine Gruppe anlegen";
    els.bookmarkGroup.appendChild(option);
    els.bookmarkGroup.disabled = true;
    return;
  }

  els.bookmarkGroup.disabled = false;
  for (const group of state.groups) {
    const option = document.createElement("option");
    option.value = String(group.id);
    option.textContent = group.name;
    els.bookmarkGroup.appendChild(option);
  }

  if (previousValue && state.groups.some((group) => String(group.id) === previousValue)) {
    els.bookmarkGroup.value = previousValue;
  }
}

function toggleBookmarkForm() {
  const isHidden = els.bookmarkForm.classList.toggle("hidden");
  els.toggleBookmarkForm.setAttribute("aria-expanded", String(!isHidden));

  if (!isHidden) {
    const firstInput = els.bookmarkForm.querySelector("input[name='title']");
    firstInput?.focus();
  }
}

function toggleGroupForm() {
  const isHidden = els.groupForm.classList.toggle("hidden");
  els.toggleGroupForm.setAttribute("aria-expanded", String(!isHidden));

  if (!isHidden) {
    const firstInput = els.groupForm.querySelector("input[name='name']");
    firstInput?.focus();
  }
}

function render() {
  renderFavoritesQuickbar();
  renderReminderAlerts();
  els.groups.innerHTML = "";
  let matchCount = 0;
  const filteredGroups = state.groups
    .map((group) => {
      const allBookmarks = group.bookmarks || [];
      const groupMatches = matchesSearch(group.name, group.description);
      const bookmarks = groupMatches ? allBookmarks : filterBookmarks(allBookmarks);
      matchCount += bookmarks.length;

      return {
        ...group,
        bookmarks,
      };
    })
    .filter((group) => group.bookmarks.length > 0 || !state.search);

  updateSearchInfo(filteredGroups.length, matchCount);

  if (filteredGroups.length === 0) {
    els.groups.innerHTML = `<p>Keine Treffer. Versuche eine andere Suche.</p>`;
    return;
  }

  for (const group of filteredGroups) {
    const node = els.groupTemplate.content.firstElementChild.cloneNode(true);
    node.dataset.groupId = String(group.id);

    node.querySelector(".group-name").textContent = group.name;
    node.querySelector(".group-description").textContent = group.description || "";

    const collapseBtn = node.querySelector(".collapse-group");
    const isCollapsed = !state.search && state.collapsedGroupIds.has(group.id);
    applyCollapsedState(node, collapseBtn, isCollapsed);

    collapseBtn.addEventListener("click", () => {
      if (state.collapsedGroupIds.has(group.id)) {
        state.collapsedGroupIds.delete(group.id);
      } else {
        state.collapsedGroupIds.add(group.id);
      }
      saveCollapsedGroupIds(state.collapsedGroupIds);
      const nextCollapsed = state.collapsedGroupIds.has(group.id);
      applyCollapsedState(node, collapseBtn, nextCollapsed);
    });

    node.querySelector(".edit-group").addEventListener("click", () => onEditGroup(group));
    node.querySelector(".share-group").addEventListener("click", () => onShareGroup(group));
    node.querySelector(".delete-group").addEventListener("click", () => onDeleteGroup(group));

    node.addEventListener("dragstart", (event) => onGroupDragStart(event, group.id));
    node.addEventListener("dragend", onGroupDragEnd);
    node.addEventListener("dragover", onGroupDragOver);
    node.addEventListener("drop", (event) => onGroupDrop(event, group.id));

    const list = node.querySelector(".bookmark-list");
    list.dataset.groupId = String(group.id);
    list.addEventListener("dragover", onBookmarkListDragOver);
    list.addEventListener("drop", (event) => onBookmarkListDrop(event, group.id));

    for (const bookmark of group.bookmarks || []) {
      const item = els.bookmarkTemplate.content.firstElementChild.cloneNode(true);
      item.dataset.bookmarkId = String(bookmark.id);
      item.dataset.groupId = String(group.id);
      const link = item.querySelector(".bookmark-link");
      link.textContent = bookmark.title;
      link.href = bookmark.url;
      item.querySelector(".bookmark-url").textContent = bookmark.url;
      item.querySelector(".bookmark-notes").textContent = bookmark.notes || "";

      const reminderNode = item.querySelector(".bookmark-reminder");
      const remindDate = parseBookmarkDate(bookmark.remind_at);
      if (remindDate) {
        const due = isBookmarkAlert(bookmark);
        const daysUntil = getDaysUntil(remindDate);
        const formatted = remindDate.toLocaleDateString("de-DE");
        if (daysUntil < 0) {
          reminderNode.textContent = `Wiedervorlage ueberfaellig: ${formatted}`;
        } else if (daysUntil <= 3) {
          reminderNode.textContent = `Wiedervorlage bald: ${formatted}`;
        } else {
          reminderNode.textContent = `Wiedervorlage am: ${formatted}`;
        }
        reminderNode.classList.remove("hidden");
        reminderNode.classList.toggle("is-due", due);
        item.classList.toggle("is-reminder-alert", due);
      } else {
        reminderNode.classList.add("hidden");
        reminderNode.classList.remove("is-due");
        reminderNode.textContent = "";
        item.classList.remove("is-reminder-alert");
      }

      const tagContainer = item.querySelector(".bookmark-tags");
      for (const tag of bookmark.tags || []) {
        const chip = document.createElement("span");
        chip.className = "tag-chip";
        chip.textContent = `#${tag}`;
        tagContainer.appendChild(chip);
      }

      const pinBtn = item.querySelector(".toggle-pin");
      const favBtn = item.querySelector(".toggle-favorite");
      pinBtn.classList.toggle("active", Boolean(bookmark.pinned));
      favBtn.classList.toggle("active", Boolean(bookmark.favorite));
      pinBtn.textContent = bookmark.pinned ? "Unpin" : "Pin";
      favBtn.textContent = bookmark.favorite ? "Unfav" : "Fav";
      pinBtn.addEventListener("click", () => onToggleBookmarkPin(bookmark));
      favBtn.addEventListener("click", () => onToggleBookmarkFavorite(bookmark));

      item.querySelector(".edit-bookmark").addEventListener("click", () => onEditBookmark(bookmark));
      item.querySelector(".delete-bookmark").addEventListener("click", () => onDeleteBookmark(bookmark));

      item.addEventListener("dragstart", (event) => onBookmarkDragStart(event, bookmark.id, group.id));
      item.addEventListener("dragend", onBookmarkDragEnd);
      item.addEventListener("dragover", onBookmarkDragOver);
      item.addEventListener("drop", (event) => onBookmarkDrop(event, bookmark.id, group.id));

      list.appendChild(item);
    }

    els.groups.appendChild(node);
  }
}

function renderFavoritesQuickbar() {
  const favorites = state.groups
    .flatMap((group) => (group.bookmarks || []).map((bookmark) => ({
      ...bookmark,
      groupName: group.name,
    })))
    .filter((bookmark) => Boolean(bookmark.favorite))
    .sort((a, b) => {
      if (Number(b.pinned) !== Number(a.pinned)) {
        return Number(b.pinned) - Number(a.pinned);
      }
      if (Number(a.sort_order ?? 0) !== Number(b.sort_order ?? 0)) {
        return Number(a.sort_order ?? 0) - Number(b.sort_order ?? 0);
      }
      return String(a.title || "").localeCompare(String(b.title || ""));
    });

  els.favoritesQuickbar.innerHTML = "";
  if (favorites.length === 0) {
    els.favoritesSegment.classList.add("hidden");
    return;
  }

  els.favoritesSegment.classList.remove("hidden");
  for (const bookmark of favorites) {
    const link = document.createElement("a");
    link.className = "favorite-pill";
    link.href = bookmark.url;
    link.target = "_blank";
    link.rel = "noreferrer";
    link.title = `${bookmark.title} (${bookmark.groupName})`;
    link.textContent = bookmark.title;
    if (bookmark.pinned) {
      link.classList.add("is-pinned");
    }
    els.favoritesQuickbar.appendChild(link);
  }
}

function renderReminderAlerts() {
  const alerts = state.groups
    .flatMap((group) => (group.bookmarks || []).map((bookmark) => ({
      ...bookmark,
      groupName: group.name,
    })))
    .filter((bookmark) => isBookmarkAlert(bookmark))
    .sort((a, b) => {
      const aDate = parseBookmarkDate(a.remind_at)?.getTime() ?? Number.MAX_SAFE_INTEGER;
      const bDate = parseBookmarkDate(b.remind_at)?.getTime() ?? Number.MAX_SAFE_INTEGER;
      if (aDate !== bDate) {
        return aDate - bDate;
      }
      return String(a.title || "").localeCompare(String(b.title || ""));
    });

  els.reminderAlertsList.innerHTML = "";
  if (alerts.length === 0) {
    els.reminderAlertsBlock.classList.add("hidden");
    return;
  }

  els.reminderAlertsBlock.classList.remove("hidden");
  for (const bookmark of alerts) {
    const li = document.createElement("li");
    const link = document.createElement("a");
    const reminder = parseBookmarkDate(bookmark.remind_at);
    const days = getDaysUntil(reminder);

    link.href = bookmark.url;
    link.target = "_blank";
    link.rel = "noreferrer";
    link.textContent = `${bookmark.title} (${bookmark.groupName})`;

    const meta = document.createElement("span");
    if (days < 0) {
      meta.textContent = `ueberfaellig seit ${Math.abs(days)} Tag(en)`;
    } else if (days === 0) {
      meta.textContent = "faellig heute";
    } else {
      meta.textContent = `faellig in ${days} Tag(en)`;
    }

    li.appendChild(link);
    li.appendChild(meta);
    els.reminderAlertsList.appendChild(li);
  }
}

function applyCollapsedState(node, button, collapsed) {
  node.classList.toggle("is-collapsed", collapsed);
  button.textContent = collapsed ? "Aufklappen" : "Zuklappen";
  button.setAttribute("aria-expanded", String(!collapsed));
}

async function onToggleBookmarkPin(bookmark) {
  await saveBookmark(bookmark, { pinned: !bookmark.pinned });
}

async function onToggleBookmarkFavorite(bookmark) {
  await saveBookmark(bookmark, { favorite: !bookmark.favorite });
}

function onGroupDragStart(event, groupID) {
  state.drag.groupId = groupID;
  event.currentTarget.classList.add("dragging");
}

function onGroupDragEnd(event) {
  event.currentTarget.classList.remove("dragging");
  state.drag.groupId = null;
  for (const card of els.groups.querySelectorAll(".group-card")) {
    card.classList.remove("drop-target");
  }
}

function onGroupDragOver(event) {
  if (!state.drag.groupId) return;
  event.preventDefault();
  event.currentTarget.classList.add("drop-target");
}

async function onGroupDrop(event, targetGroupID) {
  event.preventDefault();
  const draggedGroupID = state.drag.groupId;
  event.currentTarget.classList.remove("drop-target");
  if (!draggedGroupID || draggedGroupID === targetGroupID) return;

  const draggedNode = els.groups.querySelector(`.group-card[data-group-id="${draggedGroupID}"]`);
  const targetNode = els.groups.querySelector(`.group-card[data-group-id="${targetGroupID}"]`);
  if (!draggedNode || !targetNode) return;

  els.groups.insertBefore(draggedNode, targetNode);
  await persistGroupOrder();
}

async function persistGroupOrder() {
  const orderedIDs = [...els.groups.querySelectorAll(".group-card")].map((node) => Number(node.dataset.groupId));
  try {
    await request("/api/groups/reorder", {
      method: "POST",
      body: JSON.stringify({ ordered_ids: orderedIDs }),
    });
    await loadState();
  } catch (error) {
    setStatus(error.message, true);
  }
}

function onBookmarkDragStart(event, bookmarkID, groupID) {
  state.drag.bookmarkId = bookmarkID;
  state.drag.bookmarkGroupId = groupID;
  event.currentTarget.classList.add("dragging");
}

function onBookmarkDragEnd(event) {
  event.currentTarget.classList.remove("dragging");
  state.drag.bookmarkId = null;
  state.drag.bookmarkGroupId = null;
  for (const item of els.groups.querySelectorAll(".bookmark-item")) {
    item.classList.remove("drop-target");
  }
}

function onBookmarkDragOver(event) {
  if (!state.drag.bookmarkId) return;
  event.preventDefault();
  event.currentTarget.classList.add("drop-target");
}

function onBookmarkListDragOver(event) {
  if (!state.drag.bookmarkId) return;
  event.preventDefault();
}

async function onBookmarkDrop(event, targetBookmarkID, targetGroupID) {
  event.preventDefault();
  event.currentTarget.classList.remove("drop-target");
  const draggedBookmarkID = state.drag.bookmarkId;
  const sourceGroupID = state.drag.bookmarkGroupId;
  if (!draggedBookmarkID || draggedBookmarkID === targetBookmarkID) return;
  if (sourceGroupID !== targetGroupID) return;

  const list = event.currentTarget.closest(".bookmark-list");
  const draggedNode = list.querySelector(`.bookmark-item[data-bookmark-id="${draggedBookmarkID}"]`);
  const targetNode = list.querySelector(`.bookmark-item[data-bookmark-id="${targetBookmarkID}"]`);
  if (!draggedNode || !targetNode) return;

  list.insertBefore(draggedNode, targetNode);
  await persistBookmarkOrder(targetGroupID, list);
}

async function onBookmarkListDrop(event, targetGroupID) {
  event.preventDefault();
  const draggedBookmarkID = state.drag.bookmarkId;
  const sourceGroupID = state.drag.bookmarkGroupId;
  if (!draggedBookmarkID || sourceGroupID !== targetGroupID) return;

  const list = event.currentTarget;
  const draggedNode = list.querySelector(`.bookmark-item[data-bookmark-id="${draggedBookmarkID}"]`);
  if (!draggedNode) return;
  list.appendChild(draggedNode);
  await persistBookmarkOrder(targetGroupID, list);
}

async function persistBookmarkOrder(groupID, listNode) {
  const orderedIDs = [...listNode.querySelectorAll(".bookmark-item")].map((node) => Number(node.dataset.bookmarkId));
  try {
    await request(`/api/groups/${groupID}/bookmarks/reorder`, {
      method: "POST",
      body: JSON.stringify({ ordered_ids: orderedIDs }),
    });
    await loadState();
  } catch (error) {
    setStatus(error.message, true);
  }
}

function loadCollapsedGroupIds() {
  try {
    const raw = localStorage.getItem("bookmark-collapsed-groups");
    if (!raw) return new Set();
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return new Set();

    return new Set(parsed.filter((id) => Number.isFinite(id)));
  } catch {
    return new Set();
  }
}

function saveCollapsedGroupIds(collapsedSet) {
  const ids = Array.from(collapsedSet.values());
  localStorage.setItem("bookmark-collapsed-groups", JSON.stringify(ids));
}

function filterBookmarks(bookmarks) {
  if (!state.search) return bookmarks;
  return bookmarks.filter((bookmark) => {
    return matchesSearch(bookmark.title, bookmark.url, bookmark.notes, (bookmark.tags || []).join(" "));
  });
}

function matchesSearch(...parts) {
  if (!state.search) return true;
  const haystack = parts.join(" ").toLowerCase();
  return haystack.includes(state.search);
}

function updateSearchInfo(groupCount, matchCount) {
  if (!state.search) {
    els.searchInfo.textContent = "Suche in Titeln, URLs, Notizen und Gruppen.";
    return;
  }

  const pluralBookmarks = matchCount === 1 ? "Lesezeichen" : "Lesezeichen";
  const pluralGroups = groupCount === 1 ? "Gruppe" : "Gruppen";
  els.searchInfo.textContent = `${matchCount} ${pluralBookmarks} in ${groupCount} ${pluralGroups} gefunden.`;
}

async function onCreateGroup(event) {
  event.preventDefault();
  const formData = new FormData(event.target);
  const payload = {
    name: formData.get("name")?.toString().trim(),
    description: formData.get("description")?.toString().trim(),
    sort_order: Number(formData.get("sort_order") || 0),
  };

  try {
    await request("/api/groups", {
      method: "POST",
      body: JSON.stringify(payload),
    });
    event.target.reset();
    setStatus("Gruppe erstellt.");
    await loadState();

    if (!els.groupForm.classList.contains("hidden")) {
      toggleGroupForm();
    }

    if (els.bookmarkForm.classList.contains("hidden")) {
      toggleBookmarkForm();
    }
  } catch (error) {
    setStatus(error.message, true);
  }
}

async function onEditGroup(group) {
  const name = prompt("Neuer Gruppenname:", group.name);
  if (name === null) return;
  const description = prompt("Beschreibung:", group.description || "") ?? "";
  const sortOrderRaw = prompt("Reihenfolge (Zahl):", String(group.sort_order ?? 0));
  if (sortOrderRaw === null) return;

  const sortOrder = Number(sortOrderRaw);
  if (Number.isNaN(sortOrder)) {
    setStatus("Reihenfolge muss eine Zahl sein.", true);
    return;
  }

  try {
    await request(`/api/groups/${group.id}`, {
      method: "PUT",
      body: JSON.stringify({
        name: name.trim(),
        description: description.trim(),
        sort_order: sortOrder,
      }),
    });
    setStatus("Gruppe aktualisiert.");
    await loadState();
  } catch (error) {
    setStatus(error.message, true);
  }
}

async function onDeleteGroup(group) {
  if (!confirm(`Gruppe "${group.name}" und alle Lesezeichen wirklich loeschen?`)) return;
  try {
    await request(`/api/groups/${group.id}`, { method: "DELETE" });
    setStatus("Gruppe geloescht.");
    await loadState();
  } catch (error) {
    setStatus(error.message, true);
  }
}

async function onShareGroup(group) {
  const choice = prompt(
    "Exportformat fuer diese Gruppe (json, csv, html):",
    "json",
  );
  if (choice === null) return;

  const format = (choice || "").trim().toLowerCase();
  if (!["json", "csv", "html"].includes(format)) {
    setStatus("Ungueltiges Format. Erlaubt: json, csv, html.", true);
    return;
  }

  await onExport(format, group.id);
}

async function onCreateBookmark(event) {
  event.preventDefault();

  if (state.groups.length === 0) {
    setStatus("Bitte zuerst eine Gruppe anlegen.", true);
    return;
  }

  const formData = new FormData(event.target);
  const groupID = Number(formData.get("group_id"));
  const payload = {
    group_id: groupID,
    title: formData.get("title")?.toString().trim(),
    url: formData.get("url")?.toString().trim(),
    notes: formData.get("notes")?.toString().trim(),
    remind_at: formData.get("remind_at")?.toString().trim() || "",
    tags: parseTagsInput(formData.get("tags")),
    favorite: formData.get("favorite") === "on",
    pinned: formData.get("pinned") === "on",
    sort_order: nextSortOrderForGroup(groupID),
  };

  if (!Number.isFinite(groupID) || groupID <= 0) {
    setStatus("Bitte eine gueltige Gruppe waehlen.", true);
    return;
  }

  try {
    await request("/api/bookmarks", {
      method: "POST",
      body: JSON.stringify(payload),
    });
    event.target.reset();
    setStatus("Lesezeichen erstellt.");
    await loadState();
  } catch (error) {
    setStatus(error.message, true);
  }
}

async function onEditBookmark(bookmark) {
  const titleField = els.bookmarkEditForm.elements.namedItem("title");
  const urlField = els.bookmarkEditForm.elements.namedItem("url");
  const notesField = els.bookmarkEditForm.elements.namedItem("notes");
  const remindAtField = els.bookmarkEditForm.elements.namedItem("remind_at");
  const tagsField = els.bookmarkEditForm.elements.namedItem("tags");
  const favoriteField = els.bookmarkEditForm.elements.namedItem("favorite");
  const pinnedField = els.bookmarkEditForm.elements.namedItem("pinned");
  const idField = els.bookmarkEditForm.elements.namedItem("id");
  const groupIDField = els.bookmarkEditForm.elements.namedItem("group_id");

  populateBookmarkEditGroupSelect(bookmark.group_id);

  idField.value = String(bookmark.id);
  groupIDField.value = String(bookmark.group_id);
  titleField.value = bookmark.title || "";
  urlField.value = bookmark.url || "";
  notesField.value = bookmark.notes || "";
  remindAtField.value = toDateInputValue(bookmark.remind_at);
  tagsField.value = tagsToInput(bookmark.tags || []);
  favoriteField.checked = Boolean(bookmark.favorite);
  pinnedField.checked = Boolean(bookmark.pinned);

  if (typeof els.bookmarkEditDialog.showModal === "function") {
    els.bookmarkEditDialog.showModal();
    titleField.focus();
  } else {
    setStatus("Dialog wird von diesem Browser nicht unterstuetzt.", true);
  }
}

function populateBookmarkEditGroupSelect(selectedGroupID) {
  els.bookmarkEditGroup.innerHTML = "";

  for (const group of state.groups) {
    const option = document.createElement("option");
    option.value = String(group.id);
    option.textContent = group.name;
    els.bookmarkEditGroup.appendChild(option);
  }

  if (selectedGroupID && state.groups.some((group) => group.id === selectedGroupID)) {
    els.bookmarkEditGroup.value = String(selectedGroupID);
  }
}

function closeBookmarkEditDialog() {
  if (els.bookmarkEditDialog.open) {
    els.bookmarkEditDialog.close();
  }
}

async function onSaveEditedBookmark(event) {
  event.preventDefault();
  const formData = new FormData(els.bookmarkEditForm);
  const id = Number(formData.get("id"));
  const groupID = Number(formData.get("group_id"));

  if (!Number.isFinite(id) || id <= 0) {
    setStatus("Ungueltige Lesezeichen-ID.", true);
    return;
  }

  if (!Number.isFinite(groupID) || groupID <= 0) {
    setStatus("Bitte eine gueltige Gruppe waehlen.", true);
    return;
  }

  try {
    await saveBookmark(getBookmarkById(id), {
      group_id: groupID,
      title: formData.get("title")?.toString().trim(),
      url: formData.get("url")?.toString().trim(),
      notes: formData.get("notes")?.toString().trim(),
      remind_at: formData.get("remind_at")?.toString().trim() || "",
      tags: parseTagsInput(formData.get("tags")),
      favorite: formData.get("favorite") === "on",
      pinned: formData.get("pinned") === "on",
    }, false);

    closeBookmarkEditDialog();
    setStatus("Lesezeichen aktualisiert.");
  } catch (error) {
    setStatus(error.message, true);
  }
}

async function saveBookmark(bookmark, patch, withHint = true) {
  if (!bookmark && !patch?.group_id) {
    throw new Error("Bookmark konnte nicht geladen werden.");
  }

  const payload = {
    group_id: patch.group_id ?? bookmark.group_id,
    title: patch.title ?? bookmark.title,
    url: patch.url ?? bookmark.url,
    notes: patch.notes ?? bookmark.notes,
    remind_at: patch.remind_at ?? bookmark.remind_at ?? "",
    tags: patch.tags ?? bookmark.tags ?? [],
    favorite: patch.favorite ?? Boolean(bookmark.favorite),
    pinned: patch.pinned ?? Boolean(bookmark.pinned),
    sort_order: patch.sort_order ?? Number(bookmark.sort_order ?? 0),
  };

  await request(`/api/bookmarks/${patch.id ?? bookmark.id}`, {
    method: "PUT",
    body: JSON.stringify(payload),
  });

  if (withHint) {
    setStatus("Lesezeichen aktualisiert.");
  }
  await loadState();
}

async function onDeleteBookmark(bookmark) {
  if (!confirm(`Lesezeichen "${bookmark.title}" wirklich loeschen?`)) return;

  try {
    await request(`/api/bookmarks/${bookmark.id}`, { method: "DELETE" });
    setStatus("Lesezeichen geloescht.");
    await loadState();
  } catch (error) {
    setStatus(error.message, true);
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
  els.status.style.color = isError ? "#922" : "#334";
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

function isBookmarkAlert(bookmark) {
  const reminder = parseBookmarkDate(bookmark.remind_at);
  if (!reminder) return false;
  const daysUntil = getDaysUntil(reminder);
  return daysUntil <= 3;
}

function toDateInputValue(rawValue) {
  const date = parseBookmarkDate(rawValue);
  if (!date) return "";
  return date.toISOString().slice(0, 10);
}
