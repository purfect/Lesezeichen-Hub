const els = {
  archiveSummary: document.getElementById("archive-summary"),
  archiveGroups: document.getElementById("archive-groups"),
  search: document.getElementById("archive-search"),
  group: document.getElementById("archive-group"),
  sort: document.getElementById("archive-sort"),
  pageSize: document.getElementById("archive-page-size"),
  status: document.getElementById("status"),
};

const state = { groups: [], pages: new Map(), expanded: new Set() };
const collator = new Intl.Collator("de", { sensitivity: "base", numeric: true });

init();

async function init() {
  try {
    setStatus("Lade Archiv...");
    const payload = await request("/api/state");
    state.groups = (payload.groups || [])
      .map((group) => ({
        id: String(group.id),
        name: group.name || "Unbenannt",
        bookmarks: (group.bookmarks || []).filter((bookmark) => Boolean(bookmark.archived)),
      }))
      .filter((group) => group.bookmarks.length > 0)
      .sort((first, second) => collator.compare(first.name, second.name));
    for (const group of state.groups) {
      const option = document.createElement("option");
      option.value = group.id;
      option.textContent = `${group.name} (${group.bookmarks.length})`;
      els.group.appendChild(option);
    }
    if (state.groups.length > 0) state.expanded.add(state.groups[0].id);
    for (const control of [els.search, els.group, els.sort, els.pageSize]) {
      control.disabled = false;
      control.addEventListener(control === els.search ? "input" : "change", () => {
        state.pages.clear();
        renderArchive();
      });
    }
    renderArchive();
    setStatus("Bereit.");
  } catch (error) {
    setStatus(error.message || "Fehler beim Laden des Archivs.", true);
  }
}

function bookmarkDomain(bookmark) {
  try {
    return new URL(bookmark.url).hostname;
  } catch {
    return bookmark.url || "";
  }
}

function renderArchive() {
  const terms = els.search.value.trim().toLocaleLowerCase("de").split(/\s+/).filter(Boolean);
  const filtering = terms.length > 0 || els.group.value !== "";
  const groupedArchive = state.groups
    .filter((group) => !els.group.value || group.id === els.group.value)
    .map((group) => ({ ...group, bookmarks: group.bookmarks.filter((bookmark) => {
      const text = [group.name, bookmark.title, bookmark.url, bookmark.notes, ...(bookmark.tags || [])]
        .join(" ").toLocaleLowerCase("de");
      return terms.every((term) => text.includes(term));
    }).sort((first, second) => {
      if (els.sort.value === "newest") {
        const difference = (Date.parse(second.updated_at || second.created_at) || 0)
          - (Date.parse(first.updated_at || first.created_at) || 0);
        if (difference) return difference;
      }
      if (els.sort.value === "domain") {
        const difference = collator.compare(bookmarkDomain(first), bookmarkDomain(second));
        if (difference) return difference;
      }
      return collator.compare(first.title || first.url, second.title || second.url);
    }) }))
    .filter((group) => group.bookmarks.length > 0);

  const total = state.groups.reduce((sum, group) => sum + group.bookmarks.length, 0);
  const matches = groupedArchive.reduce((sum, group) => sum + group.bookmarks.length, 0);
  els.archiveSummary.textContent = `${matches} von ${total} archivierten Lesezeichen · ${groupedArchive.length} Gruppen`;

  els.archiveGroups.replaceChildren();

  if (groupedArchive.length === 0) {
    const empty = document.createElement("div");
    empty.className = "empty-state";
    if (total === 0) {
      empty.innerHTML = '<p>Aktuell sind keine Lesezeichen archiviert.</p><a class="primary-link" href="/">Lesezeichen verwalten</a>';
    } else {
      const message = document.createElement("p");
      message.textContent = "Keine archivierten Lesezeichen gefunden.";
      const reset = document.createElement("button");
      reset.type = "button";
      reset.className = "ghost";
      reset.textContent = "Filter zurücksetzen";
      reset.addEventListener("click", () => {
        els.search.value = "";
        els.group.value = "";
        state.pages.clear();
        renderArchive();
        els.search.focus();
      });
      empty.append(message, reset);
    }
    els.archiveGroups.appendChild(empty);
    return;
  }

  for (const group of groupedArchive) {
    const section = document.createElement("details");
    section.className = "archive-group";
    section.open = filtering || state.expanded.has(group.id);
    const heading = document.createElement("summary");
    const title = document.createElement("span");
    title.textContent = group.name;
    const count = document.createElement("span");
    count.className = "archive-count";
    count.textContent = `${group.bookmarks.length} Einträge`;
    heading.append(title, count);
    const content = document.createElement("div");
    content.className = "archive-group-content";
    const showPage = () => {
      content.replaceChildren();
      const pageSize = Number(els.pageSize.value);
      const pageCount = Math.ceil(group.bookmarks.length / pageSize);
      const page = Math.min(state.pages.get(group.id) || 0, pageCount - 1);
      const start = page * pageSize;
      const end = Math.min(start + pageSize, group.bookmarks.length);
      const list = document.createElement("ul");
      list.className = "archive-link-list";

      for (const bookmark of group.bookmarks.slice(start, end)) {
        const li = document.createElement("li");
        const link = document.createElement("a");
        link.href = bookmark.url;
        link.target = "_blank";
        link.rel = "noreferrer";
        link.textContent = bookmark.title || bookmark.url;
        link.title = bookmark.url;
        const domain = document.createElement("span");
        domain.className = "archive-domain";
        domain.textContent = bookmarkDomain(bookmark);
        domain.title = bookmark.url;
        li.appendChild(link);
        li.appendChild(domain);
        list.appendChild(li);
      }

      content.appendChild(list);
      if (pageCount > 1) {
        const pager = document.createElement("nav");
        pager.className = "archive-pagination";
        pager.setAttribute("aria-label", `Seiten für ${group.name}`);
        const info = document.createElement("span");
        info.setAttribute("role", "status");
        info.textContent = `${start + 1}–${end} von ${group.bookmarks.length} · Seite ${page + 1}/${pageCount}`;
        pager.appendChild(info);
        for (const [label, symbol, offset] of [["Vorherige Seite", "←", -1], ["Nächste Seite", "→", 1]]) {
          const button = document.createElement("button");
          button.type = "button";
          button.className = "ghost";
          button.textContent = symbol;
          button.title = label;
          button.setAttribute("aria-label", label);
          button.disabled = page + offset < 0 || page + offset >= pageCount;
          button.addEventListener("click", () => {
            state.pages.set(group.id, page + offset);
            showPage();
            const target = content.querySelector(`button[aria-label="${label}"]:not(:disabled)`)
              || content.querySelector("button:not(:disabled)");
            target?.focus({ preventScroll: true });
            heading.scrollIntoView({ block: "nearest" });
          });
          pager.appendChild(button);
        }
        content.appendChild(pager);
      }
    };
    section.addEventListener("toggle", () => {
      if (!section.isConnected) return;
      if (section.open) {
        if (!filtering) state.expanded.add(group.id);
        if (!content.hasChildNodes()) showPage();
      } else {
        if (!filtering) state.expanded.delete(group.id);
        content.replaceChildren();
      }
    });
    if (section.open) showPage();
    section.append(heading, content);
    els.archiveGroups.appendChild(section);
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
