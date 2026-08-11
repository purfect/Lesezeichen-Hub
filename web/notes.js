// ── State ──────────────────────────────────────────────────────────────────
let allNotes = [];
let allBookmarks = [];   // flat list from /api/state
let activeNoteId = null;
let isEditing = false;
let searchDebounce = null;
let pendingBookmarkIds = []; // set when arriving from main page via URL param

// ── DOM refs ──────────────────────────────────────────────────────────────
const els = {
  notesList:       document.getElementById("notes-list"),
  notesSearch:     document.getElementById("notes-search"),
  btnNewNote:      document.getElementById("btn-new-note"),
  detailHead:      document.getElementById("detail-head"),
  detailTitleLabel:document.getElementById("detail-title-label"),
  btnEdit:         document.getElementById("btn-edit"),
  btnDelete:       document.getElementById("btn-delete"),
  detailBody:      document.getElementById("notes-detail-body"),
  status:          document.getElementById("status"),
};

// ── Bootstrap ─────────────────────────────────────────────────────────────
init();

async function init() {
  try {
    setStatus("Lade Daten…");
    const [notesRes, stateRes] = await Promise.all([
      request("/api/notes"),
      request("/api/state"),
    ]);
    allNotes     = notesRes.notes || [];
    allBookmarks = (stateRes.groups || []).flatMap(g => g.bookmarks || []);
    renderList(allNotes);
    setStatus("");

    // Auto-open editor if arriving from a bookmark's note button
    const params = new URLSearchParams(location.search);
    const bmId    = Number(params.get("bookmark_id"));
    const bmTitle = params.get("bookmark_title") || "";
    if (bmId > 0) {
      pendingBookmarkIds = [bmId];
      openEditor(null, bmTitle ? `Notiz zu: ${bmTitle}` : "");
    }
  } catch (err) {
    setStatus(err.message || "Fehler beim Laden.", true);
  }
}

// ── List rendering ────────────────────────────────────────────────────────
function renderList(notes) {
  els.notesList.innerHTML = "";
  if (notes.length === 0) {
    els.notesList.innerHTML = '<p class="notes-empty">Keine Notizen gefunden.</p>';
    return;
  }
  for (const n of notes) {
    const item = document.createElement("div");
    item.className = "note-item" + (n.id === activeNoteId ? " active" : "");
    item.dataset.id = n.id;

    const preview = (n.content || "").replace(/\n/g, " ").slice(0, 80);

    item.innerHTML = `
      <div class="note-item-title">${esc(n.title)}</div>
      <div class="note-item-meta">
        <span class="type-badge type-${esc(n.type)}">${typeLabel(n.type)}</span>
        <span class="note-item-preview">${esc(preview)}</span>
        <span class="note-item-date">${formatDate(n.updated_at)}</span>
      </div>
    `;
    item.addEventListener("click", () => selectNote(n.id));
    els.notesList.appendChild(item);
  }
}

// ── Select / view a note ──────────────────────────────────────────────────
function selectNote(id) {
  activeNoteId = id;
  isEditing = false;
  const note = allNotes.find(n => n.id === id);
  if (!note) return;

  // Highlight sidebar item
  document.querySelectorAll(".note-item").forEach(el => {
    el.classList.toggle("active", Number(el.dataset.id) === id);
  });

  renderNoteView(note);
}

function renderNoteView(note) {
  els.detailHead.classList.remove("hidden");
  els.detailTitleLabel.textContent = note.title;

  const linkedBookmarks = allBookmarks.filter(b =>
    (note.bookmark_ids || []).includes(b.id)
  );

  let contentHtml;
  if (note.type === "code") {
    contentHtml = `<pre class="note-view-content is-code">${esc(note.content)}</pre>`;
  } else {
    contentHtml = `<div class="note-view-content">${esc(note.content)}</div>`;
  }

  const tagsHtml = (note.tags || []).length > 0
    ? `<div class="note-view-tags">${note.tags.map(t => `<span class="note-tag">${esc(t)}</span>`).join("")}</div>`
    : "";

  const bookmarksHtml = linkedBookmarks.length > 0
    ? `<div class="note-view-bookmarks">
         <h4>Verknuepfte Lesezeichen</h4>
         <div class="linked-bookmark-list">
           ${linkedBookmarks.map(b => `
             <a class="linked-bookmark" href="${esc(b.url)}" target="_blank" rel="noopener noreferrer">
               <span class="linked-bookmark-title">${esc(b.title)}</span>
               <span class="linked-bookmark-url">${esc(b.url)}</span>
             </a>
           `).join("")}
         </div>
       </div>`
    : "";

  const metaHtml = `
    <div class="note-meta-row">
      <span class="type-badge type-${esc(note.type)}">${typeLabel(note.type)}</span>
      <span class="note-meta-date">Erstellt: ${formatDateLong(note.created_at)}</span>
      <span class="note-meta-date">Geaendert: ${formatDateLong(note.updated_at)}</span>
    </div>
  `;

  els.detailBody.innerHTML = metaHtml + contentHtml + tagsHtml + bookmarksHtml;
}

// ── Editor ────────────────────────────────────────────────────────────────
function openEditor(note, prefillTitle = "") {
  isEditing = true;
  els.detailHead.classList.add("hidden");

  const isNew  = !note;
  const title   = prefillTitle || note?.title   || "";
  const content = note?.content || "";
  const type    = note?.type    || "note";
  const tags    = (note?.tags || []).join(", ");

  els.detailBody.innerHTML = `
    <form id="note-editor-form" class="note-editor">
      <label>
        Titel
        <input id="editor-title" type="text" value="${esc(title)}" placeholder="Titel der Notiz" maxlength="200" required />
      </label>
      <label>
        Typ
        <select id="editor-type">
          <option value="note"        ${type === "note"        ? "selected" : ""}>&#128221; Notiz</option>
          <option value="code"        ${type === "code"        ? "selected" : ""}>&#128187; Code-Schnipsel</option>
          <option value="annotation"  ${type === "annotation"  ? "selected" : ""}>&#128204; Anmerkung</option>
        </select>
      </label>
      <label>
        Inhalt
        <textarea id="editor-content" class="${type === "code" ? "is-code" : ""}" placeholder="Inhalt der Notiz…">${esc(content)}</textarea>
      </label>
      <label>
        Tags <span style="font-weight:400;text-transform:none;letter-spacing:0">(kommagetrennt)</span>
        <input id="editor-tags" type="text" value="${esc(tags)}" placeholder="z.B. python, backend, todo" />
      </label>
      <div class="editor-actions">
        <button type="button" id="btn-cancel-edit" class="btn-cancel">Abbrechen</button>
        <button type="submit" class="btn-save">${isNew ? "Erstellen" : "Speichern"}</button>
      </div>
    </form>
  `;

  const typeSelect  = document.getElementById("editor-type");
  const contentArea = document.getElementById("editor-content");
  typeSelect.addEventListener("change", () => {
    contentArea.classList.toggle("is-code", typeSelect.value === "code");
  });

  document.getElementById("btn-cancel-edit").addEventListener("click", () => {
    isEditing = false;
    pendingBookmarkIds = [];
    if (activeNoteId) {
      selectNote(activeNoteId);
    } else {
      resetDetail();
    }
  });

  document.getElementById("note-editor-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    await saveNote(note?.id, note?.bookmark_ids);
  });
}

async function saveNote(existingId, existingBookmarkIds) {
  const title   = document.getElementById("editor-title").value.trim();
  const type    = document.getElementById("editor-type").value;
  const content = document.getElementById("editor-content").value;
  const tagsRaw = document.getElementById("editor-tags").value;
  const tags    = tagsRaw.split(",").map(t => t.trim()).filter(Boolean);

  // New note: use bookmark from URL param; existing note: preserve its links
  const bookmarkIds = existingId ? (existingBookmarkIds || []) : pendingBookmarkIds;

  const body = { title, content, type, tags, bookmark_ids: bookmarkIds };

  try {
    setStatus("Speichere…");
    if (existingId) {
      await request(`/api/notes/${existingId}`, { method: "PUT", body });
    } else {
      const res = await request("/api/notes", { method: "POST", body });
      activeNoteId = res.id;
    }
    pendingBookmarkIds = [];
    await reloadNotes();
    if (activeNoteId) selectNote(activeNoteId);
    setStatus("Gespeichert.");
    setTimeout(() => setStatus(""), 2000);
  } catch (err) {
    setStatus(err.message || "Fehler beim Speichern.", true);
  }
}

async function deleteNote(id) {
  if (!confirm("Notiz wirklich loeschen?")) return;
  try {
    setStatus("Loesche…");
    await request(`/api/notes/${id}`, { method: "DELETE" });
    activeNoteId = null;
    await reloadNotes();
    resetDetail();
    setStatus("Notiz geloescht.");
    setTimeout(() => setStatus(""), 2000);
  } catch (err) {
    setStatus(err.message || "Fehler beim Loeschen.", true);
  }
}

function resetDetail() {
  els.detailHead.classList.add("hidden");
  els.detailBody.innerHTML = `
    <div class="notes-placeholder">
      <div class="notes-placeholder-icon">&#128221;</div>
      <p>Waehle eine Notiz aus oder lege eine neue an.</p>
    </div>
  `;
}

// ── Event wiring ──────────────────────────────────────────────────────────
els.btnNewNote.addEventListener("click", () => {
  activeNoteId = null;
  pendingBookmarkIds = [];
  document.querySelectorAll(".note-item").forEach(el => el.classList.remove("active"));
  openEditor(null);
});

els.btnEdit.addEventListener("click", () => {
  const note = allNotes.find(n => n.id === activeNoteId);
  if (note) openEditor(note);
});

els.btnDelete.addEventListener("click", () => {
  if (activeNoteId) deleteNote(activeNoteId);
});

els.notesSearch.addEventListener("input", () => {
  clearTimeout(searchDebounce);
  searchDebounce = setTimeout(() => {
    const q = els.notesSearch.value.trim().toLowerCase();
    if (q === "") {
      renderList(allNotes);
      return;
    }
    const filtered = allNotes.filter(n =>
      n.title.toLowerCase().includes(q) ||
      (n.content || "").toLowerCase().includes(q) ||
      (n.tags || []).some(t => t.toLowerCase().includes(q))
    );
    renderList(filtered);
  }, 200);
});

// ── Helpers ───────────────────────────────────────────────────────────────
async function reloadNotes() {
  const res = await request("/api/notes");
  allNotes = res.notes || [];
  const q = els.notesSearch.value.trim().toLowerCase();
  if (q) {
    const filtered = allNotes.filter(n =>
      n.title.toLowerCase().includes(q) ||
      (n.content || "").toLowerCase().includes(q) ||
      (n.tags || []).some(t => t.toLowerCase().includes(q))
    );
    renderList(filtered);
  } else {
    renderList(allNotes);
  }
}

function typeLabel(type) {
  const map = { note: "Notiz", code: "Code", annotation: "Anmerkung" };
  return map[type] || type;
}

function esc(str) {
  return String(str || "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

function formatDate(iso) {
  if (!iso) return "";
  const d = new Date(iso);
  if (isNaN(d)) return "";
  return d.toLocaleDateString("de-DE", { day: "2-digit", month: "2-digit", year: "2-digit" });
}

function formatDateLong(iso) {
  if (!iso) return "–";
  const d = new Date(iso);
  if (isNaN(d)) return "–";
  return d.toLocaleString("de-DE", { day: "2-digit", month: "2-digit", year: "numeric", hour: "2-digit", minute: "2-digit" });
}

function setStatus(msg, isErr = false) {
  els.status.textContent = msg;
  els.status.style.color = isErr ? "var(--danger)" : "var(--muted)";
}

async function request(url, opts = {}) {
  const options = {
    method: opts.method || "GET",
    headers: {},
  };
  if (opts.body) {
    options.headers["Content-Type"] = "application/json";
    options.body = JSON.stringify(opts.body);
  }
  const res = await fetch(url, options);
  const json = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(json.error || `HTTP ${res.status}`);
  }
  return json;
}
