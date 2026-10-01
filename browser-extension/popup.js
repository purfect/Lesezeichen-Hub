const els = {
  bookmarkForm: document.getElementById("bookmark-form"),
  noteForm: document.getElementById("note-form"),
  tabs: [...document.querySelectorAll(".tab")],
  existing: document.getElementById("existing"),
  tagSuggestions: document.getElementById("tag-suggestions"),
  linkBookmarkWrap: document.getElementById("link-bookmark-wrap"),
  status: document.getElementById("status"),
};

let existingBookmark = null;

init();

async function init() {
  document.getElementById("open-hub").addEventListener("click", async () => {
    await ext.tabs.create({ url: await getHubUrl() });
    window.close();
  });
  document.getElementById("open-options").addEventListener("click", () => ext.runtime.openOptionsPage());
  els.tabs.forEach((tab) => tab.addEventListener("click", () => selectTab(tab.dataset.tab)));
  document.querySelectorAll(".clear-date").forEach((button) => {
    button.addEventListener("click", () => { button.previousElementSibling.value = ""; });
  });
  els.bookmarkForm.addEventListener("submit", onSaveBookmark);
  els.noteForm.addEventListener("submit", onSaveNote);

  const [tab] = await ext.tabs.query({ active: true, currentWindow: true });
  const url = tab?.url || "";
  const title = (tab?.title || "").trim();
  const selection = await readSelection(tab);

  els.bookmarkForm.elements.title.value = title.slice(0, 120);
  els.bookmarkForm.elements.url.value = url;
  els.noteForm.elements.title.value = title.slice(0, 200);
  els.noteForm.elements.content.value = isSavableUrl(url) ? noteContent(selection, url) : selection;
  if (selection) selectTab("note");

  try {
    setStatus("Lade Gruppen…");
    const groups = await loadGroups();
    fillGroups(groups, await preferredGroupId(groups));
    fillTagSuggestions(groups);
    showExisting(findBookmarkByUrl(groups, url));
    setStatus(isSavableUrl(url) ? "" : "Diese Seite hat keine http/https-Adresse.");
  } catch (error) {
    setStatus(error.message, "error");
    els.bookmarkForm.querySelector(".primary").disabled = true;
  }
}

async function readSelection(tab) {
  if (!tab?.id || !isSavableUrl(tab.url)) return "";
  try {
    const [result] = await ext.scripting.executeScript({
      target: { tabId: tab.id },
      func: () => String(window.getSelection?.() || ""),
    });
    return String(result?.result || "").trim();
  } catch {
    return "";
  }
}

function selectTab(name) {
  els.tabs.forEach((tab) => {
    const active = tab.dataset.tab === name;
    tab.classList.toggle("active", active);
    tab.setAttribute("aria-selected", String(active));
  });
  els.bookmarkForm.classList.toggle("hidden", name !== "bookmark");
  els.noteForm.classList.toggle("hidden", name !== "note");
}

function fillGroups(groups, selectedId) {
  const select = els.bookmarkForm.elements.group;
  select.replaceChildren();
  const candidates = realGroups(groups);
  if (candidates.length === 0) {
    select.add(new Option("Bitte zuerst im Hub eine Gruppe anlegen", ""));
    select.disabled = true;
    els.bookmarkForm.querySelector(".primary").disabled = true;
    return;
  }
  for (const group of candidates) select.add(new Option(group.name, String(group.id), false, group.id === selectedId));
}

function fillTagSuggestions(groups) {
  const tags = [...new Set(groups.flatMap((group) => group.bookmarks || []).flatMap((bookmark) => bookmark.tags || []))]
    .sort((a, b) => a.localeCompare(b, "de"));
  els.tagSuggestions.replaceChildren(...tags.map((tag) => new Option(tag)));
}

function showExisting(match) {
  existingBookmark = match?.bookmark || null;
  els.existing.classList.toggle("hidden", !match);
  els.linkBookmarkWrap.classList.toggle("hidden", !match);
  if (match) els.existing.textContent = `Bereits gespeichert in „${match.group.name}“ als „${match.bookmark.title}“.`;
}

async function onSaveBookmark(event) {
  event.preventDefault();
  const form = els.bookmarkForm.elements;
  const groupId = Number(form.group.value);
  const url = form.url.value.trim();
  if (!isSavableUrl(url)) {
    setStatus("Nur http- und https-Adressen können gespeichert werden.", "error");
    return;
  }
  await submit(event.submitter, async () => {
    await hubRequest("/api/bookmarks", {
      method: "POST",
      body: bookmarkPayload({
        groupId,
        title: form.title.value.trim(),
        url,
        notes: form.notes.value.trim(),
        tags: parseTags(form.tags.value),
        favorite: form.favorite.checked,
        pinned: form.pinned.checked,
        remindAt: form.remind_at.value,
      }),
    });
    await ext.storage.local.set({ lastGroupId: groupId });
    return "Lesezeichen gespeichert.";
  });
}

async function onSaveNote(event) {
  event.preventDefault();
  const form = els.noteForm.elements;
  const linkBookmark = existingBookmark && form.link_bookmark.checked;
  await submit(event.submitter, async () => {
    await hubRequest("/api/notes", {
      method: "POST",
      body: {
        title: form.title.value.trim(),
        content: form.content.value,
        type: "note",
        tags: parseTags(form.tags.value),
        due_at: form.due_at.value,
        bookmark_ids: linkBookmark ? [existingBookmark.id] : [],
      },
    });
    return "Notiz gespeichert.";
  });
}

async function submit(button, action) {
  button.disabled = true;
  setStatus("Speichere…");
  try {
    setStatus(await action(), "success");
    setTimeout(() => window.close(), 900);
  } catch (error) {
    setStatus(error.message, "error");
    button.disabled = false;
  }
}

function setStatus(message, kind = "") {
  els.status.textContent = message;
  els.status.classList.toggle("is-error", kind === "error");
  els.status.classList.toggle("is-success", kind === "success");
}
