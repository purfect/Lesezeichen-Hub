// ── State ──────────────────────────────────────────────────────────────────
let allNotes = [];
let allBookmarks = [];   // flat list from /api/state
let activeNoteId = null;
let isEditing = false;
let searchDebounce = null;
let pendingBookmarkIds = []; // set when arriving from main page via URL param
const decryptedVaultCache = new Map();

// ── DOM refs ──────────────────────────────────────────────────────────────
const els = {
  notesList:       document.getElementById("notes-list"),
  notesSearch:     document.getElementById("notes-search"),
  notesSearchClear:document.getElementById("notes-search-clear"),
  btnNewNote:      document.getElementById("btn-new-note"),
  detailHead:      document.getElementById("detail-head"),
  detailTitleLabel:document.getElementById("detail-title-label"),
  btnCopy:         document.getElementById("btn-copy"),
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
    updateTitleCounter();
    renderList(allNotes);
    setStatus("");

    // Auto-open editor if arriving from a bookmark's note button
    const params = new URLSearchParams(location.search);
    const bmId    = Number(params.get("bookmark_id"));
    const bmTitle = params.get("bookmark_title") || "";
    if (bmId > 0) {
      pendingBookmarkIds = [bmId];
      openEditor(null, bmTitle ? `Notiz zu: ${bmTitle}` : "");
    } else if (params.get("new") === "1") {
      openEditor(null);
      history.replaceState(null, "", "/static/notes.html");
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
  const bookmarkIdSet = new Set(allBookmarks.map(b => b.id));

  const regularNotes = notes.filter(n => n.type !== "vault");
  const vaultNotes = notes.filter(n => n.type === "vault");

  if (regularNotes.length > 0) {
    appendSectionTitle("Notizen");
    for (const n of regularNotes) {
      appendNoteItem(n, bookmarkIdSet);
    }
  }

  if (vaultNotes.length > 0) {
    appendSectionTitle("Vault");
    for (const n of vaultNotes) {
      appendNoteItem(n, bookmarkIdSet);
    }
  }
}

function appendSectionTitle(title) {
  const header = document.createElement("div");
  header.className = "notes-section-title";
  header.textContent = title;
  els.notesList.appendChild(header);
}

function appendNoteItem(n, bookmarkIdSet) {
    const item = document.createElement("div");
    const hasOrphan = (n.bookmark_ids || []).some(id => !bookmarkIdSet.has(id));
    item.className = "note-item" + (n.id === activeNoteId ? " active" : "") + (hasOrphan ? " has-orphan" : "");
    item.dataset.id = n.id;

    const preview = n.type === "vault"
      ? "🔒 Verschlüsselte Vault-Notiz"
      : (n.content || "").replace(/\n/g, " ").slice(0, 80);

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

  const bookmarkIdSet = new Set(allBookmarks.map(b => b.id));
  const linkedBookmarks = allBookmarks.filter(b =>
    (note.bookmark_ids || []).includes(b.id)
  );
  const orphanedIds = (note.bookmark_ids || []).filter(id => !bookmarkIdSet.has(id));

  let contentHtml;
  if (note.type === "vault") {
    const unlocked = decryptedVaultCache.get(note.id);
    contentHtml = unlocked
      ? `<pre class="note-view-content is-code">${esc(unlocked)}</pre>`
      : `<div class="note-view-content">🔒 Diese Vault-Notiz ist verschlüsselt. Zum Anzeigen bitte entsperren.</div>`;
  } else if (note.type === "code") {
    contentHtml = `<pre class="note-view-content is-code">${esc(note.content)}</pre>`;
  } else {
    contentHtml = `<div class="note-view-content">${esc(note.content)}</div>`;
  }

  const tagsHtml = (note.tags || []).length > 0
    ? `<div class="note-view-tags">${note.tags.map(t => `<span class="note-tag">${esc(t)}</span>`).join("")}</div>`
    : "";

  let bookmarksHtml = "";
  if (linkedBookmarks.length > 0 || orphanedIds.length > 0) {
    bookmarksHtml = `<div class="note-view-bookmarks">
      <h4>Verknuepfte Lesezeichen</h4>
      <div class="linked-bookmark-list">
        ${linkedBookmarks.map(b => `
          <a class="linked-bookmark" href="${esc(b.url)}" target="_blank" rel="noopener noreferrer">
            <span class="linked-bookmark-title">${esc(b.title)}</span>
            <span class="linked-bookmark-url">${esc(b.url)}</span>
          </a>
        `).join("")}
        ${orphanedIds.map(id => `
          <span class="linked-bookmark orphaned" title="Lesezeichen #${id} wurde gelöscht">
            <span class="orphaned-indicator">🔴</span>
            <span class="linked-bookmark-title">Lesezeichen #${id} (gelöscht)</span>
          </span>
        `).join("")}
      </div>
    </div>`;
  }

  const metaHtml = `
    <div class="note-meta-row">
      <span class="type-badge type-${esc(note.type)}">${typeLabel(note.type)}</span>
      <span class="note-meta-date">Erstellt: ${formatDateLong(note.created_at)}</span>
      <span class="note-meta-date">Geändert: ${formatDateLong(note.updated_at)}</span>
    </div>
  `;

  const vaultHtml = note.type === "vault"
    ? `<div class="vault-panel">
        <input id="vault-password" class="vault-input" type="password" placeholder="Vault-Passwort" autocomplete="off" />
        <button id="btn-vault-unlock" class="btn-edit" type="button">Entsperren</button>
      </div>`
    : "";

  els.detailBody.innerHTML = metaHtml + vaultHtml + contentHtml + tagsHtml + bookmarksHtml;

  if (note.type === "vault") {
    const unlockBtn = document.getElementById("btn-vault-unlock");
    const passInput = document.getElementById("vault-password");
    unlockBtn?.addEventListener("click", async () => {
      const pw = passInput?.value || "";
      if (!pw) {
        setStatus("Bitte Passwort eingeben.", true);
        return;
      }
      try {
        const plain = await decryptVaultContent(note.content, pw);
        decryptedVaultCache.set(note.id, plain);
        setStatus("Vault entsperrt.");
        renderNoteView(note);
      } catch {
        setStatus("Passwort falsch oder Vault-Inhalt ungültig.", true);
      }
    });
  }
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
          <option value="vault"       ${type === "vault"       ? "selected" : ""}>🔒 Vault</option>
        </select>
      </label>
      <label>
        Inhalt
        <textarea id="editor-content" class="${type === "code" ? "is-code" : ""}" placeholder="Inhalt der Notiz…">${esc(content)}</textarea>
      </label>
      <label id="vault-password-wrap" class="${type === "vault" ? "" : "hidden"}">
        Vault-Passwort
        <input id="editor-vault-password" type="password" placeholder="Passwort für Verschlüsselung" autocomplete="new-password" />
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
  const vaultWrap   = document.getElementById("vault-password-wrap");
  typeSelect.addEventListener("change", () => {
    contentArea.classList.toggle("is-code", typeSelect.value === "code");
    vaultWrap.classList.toggle("hidden", typeSelect.value !== "vault");
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
  let content   = document.getElementById("editor-content").value;
  const tagsRaw = document.getElementById("editor-tags").value;
  const tags    = tagsRaw.split(",").map(t => t.trim()).filter(Boolean);

  if (type === "vault") {
    const password = document.getElementById("editor-vault-password")?.value || "";
    if (!password) {
      setStatus("Fuer Vault-Notizen ist ein Passwort erforderlich.", true);
      return;
    }
    try {
      content = await encryptVaultContent(content, password);
    } catch {
      setStatus("Vault-Inhalt konnte nicht verschlüsselt werden.", true);
      return;
    }
  }

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
  if (!confirm("Notiz wirklich löschen?")) return;
  try {
    setStatus("Lösche…");
    await request(`/api/notes/${id}`, { method: "DELETE" });
    activeNoteId = null;
    await reloadNotes();
    resetDetail();
    setStatus("Notiz gelöscht.");
    setTimeout(() => setStatus(""), 2000);
  } catch (err) {
    setStatus(err.message || "Fehler beim Löschen.", true);
  }
}

function resetDetail() {
  els.detailHead.classList.add("hidden");
  els.detailBody.innerHTML = `
    <div class="notes-placeholder">
      <div class="notes-placeholder-icon">&#128221;</div>
      <p>Wähle eine Notiz aus oder lege eine neue an.</p>
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
  if (!note) return;
  if (note.type !== "vault") {
    openEditor(note);
    return;
  }

  const pw = prompt("Vault-Passwort zum Bearbeiten:");
  if (!pw) return;
  decryptVaultContent(note.content, pw)
    .then((plain) => {
      openEditor({ ...note, content: plain });
      setStatus("Vault entsperrt. Beim Speichern wird neu verschlüsselt.");
    })
    .catch(() => {
      setStatus("Passwort falsch oder Vault-Inhalt ungültig.", true);
    });
});

els.btnDelete.addEventListener("click", () => {
  if (activeNoteId) deleteNote(activeNoteId);
});

els.btnCopy.addEventListener("click", async () => {
  const note = allNotes.find(n => n.id === activeNoteId);
  if (!note) return;

  let textToCopy = note.content || "";
  if (note.type === "vault") {
    textToCopy = decryptedVaultCache.get(note.id) || "";
    if (!textToCopy) {
      setStatus("Vault zuerst entsperren, dann kopieren.", true);
      return;
    }
  }

  try {
    await navigator.clipboard.writeText(textToCopy);
    setStatus("Inhalt in Zwischenablage kopiert.");
    setTimeout(() => setStatus(""), 1600);
  } catch {
    setStatus("Kopieren fehlgeschlagen.", true);
  }
});

els.notesSearch.addEventListener("input", () => {
  updateSearchClearButton();
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

els.notesSearchClear.addEventListener("click", () => {
  els.notesSearch.value = "";
  renderList(allNotes);
  updateSearchClearButton();
  els.notesSearch.focus();
});

// ── Helpers ───────────────────────────────────────────────────────────────
async function reloadNotes() {
  const res = await request("/api/notes");
  allNotes = res.notes || [];
  updateTitleCounter();
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

function updateTitleCounter() {
  const el = document.getElementById("notes-title-count");
  if (el) el.textContent = `(${allNotes.length})`;
}

function updateSearchClearButton() {
  const hasValue = els.notesSearch.value.trim().length > 0;
  els.notesSearchClear.classList.toggle("hidden", !hasValue);
}

function typeLabel(type) {
  const map = { note: "Notiz", code: "Code", annotation: "Anmerkung", vault: "Vault" };
  return map[type] || type;
}

async function encryptVaultContent(plainText, password) {
  const iv = crypto.getRandomValues(new Uint8Array(12));
  const salt = crypto.getRandomValues(new Uint8Array(16));
  const iterations = 310000;
  const key = await deriveVaultKey(password, salt);
  const encoded = new TextEncoder().encode(String(plainText || ""));
  const cipherBuffer = await crypto.subtle.encrypt({ name: "AES-GCM", iv }, key, encoded);
  const payload = {
    v: 1,
    alg: "AES-256-GCM",
    kdf: "PBKDF2-HMAC-SHA-256",
    iter: iterations,
    s: bytesToBase64(salt),
    i: bytesToBase64(iv),
    c: bytesToBase64(new Uint8Array(cipherBuffer)),
  };
  return `vault:v1:${JSON.stringify(payload)}`;
}

async function decryptVaultContent(raw, password) {
  if (!String(raw || "").startsWith("vault:v1:")) {
    throw new Error("not encrypted");
  }
  const payload = JSON.parse(raw.slice("vault:v1:".length));
  const salt = base64ToBytes(payload.s);
  const iv = base64ToBytes(payload.i);
  const cipher = base64ToBytes(payload.c);
  const iter = Number(payload.iter) > 0 ? Number(payload.iter) : 250000;
  if (payload.alg && payload.alg !== "AES-256-GCM") {
    throw new Error("unsupported algorithm");
  }
  if (payload.kdf && payload.kdf !== "PBKDF2-HMAC-SHA-256") {
    throw new Error("unsupported kdf");
  }
  const key = await deriveVaultKey(password, salt, iter);
  const plainBuffer = await crypto.subtle.decrypt({ name: "AES-GCM", iv }, key, cipher);
  return new TextDecoder().decode(plainBuffer);
}

async function deriveVaultKey(password, salt, iterations = 310000) {
  const baseKey = await crypto.subtle.importKey(
    "raw",
    new TextEncoder().encode(password),
    { name: "PBKDF2" },
    false,
    ["deriveKey"]
  );
  return crypto.subtle.deriveKey(
    { name: "PBKDF2", salt, iterations, hash: "SHA-256" },
    baseKey,
    { name: "AES-GCM", length: 256 },
    false,
    ["encrypt", "decrypt"]
  );
}

function bytesToBase64(bytes) {
  let str = "";
  for (const b of bytes) str += String.fromCharCode(b);
  return btoa(str);
}

function base64ToBytes(b64) {
  const str = atob(String(b64 || ""));
  const out = new Uint8Array(str.length);
  for (let i = 0; i < str.length; i++) out[i] = str.charCodeAt(i);
  return out;
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
