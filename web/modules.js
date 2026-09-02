const els = {
  catalogList: document.getElementById("catalog-list"),
  catalogSummary: document.getElementById("catalog-summary"),
  list: document.getElementById("modules-list"),
  summary: document.getElementById("modules-summary"),
  check: document.getElementById("check-modules"),
  status: document.getElementById("status"),
};

els.check.addEventListener("click", () => loadModules(true));
loadModules();

async function loadModules(showResult = false) {
  try {
    els.check.disabled = true;
    setStatus("Module werden geprüft...");
    const [catalogPayload, localPayload] = await Promise.all([
      request("/api/module-catalog"),
      request("/api/modules"),
    ]);
    renderCatalog(catalogPayload.modules || []);
    renderModules(localPayload.modules || []);
    setStatus(showResult ? "Modulliste aktualisiert." : "Bereit.");
  } catch (error) {
    setStatus(error.message || "Module konnten nicht geladen werden.", true);
  } finally {
    els.check.disabled = false;
  }
}

function renderCatalog(modules) {
  els.catalogList.innerHTML = "";
  const installed = modules.filter((item) => item.installed).length;
  els.catalogSummary.textContent = `${modules.length} Module, davon ${installed} eingerichtet.`;
  if (modules.length === 0) {
    const empty = document.createElement("div");
    empty.className = "empty-state";
    empty.textContent = "Zurzeit sind keine Module im GitHub-Katalog verfügbar.";
    els.catalogList.appendChild(empty);
    return;
  }

  for (const module of modules) {
    const card = document.createElement("article");
    card.className = `module-card catalog-card ${module.installed ? "is-installed" : ""}`;
    card.innerHTML = `
      <div class="module-card-head">
        <strong>${escapeHTML(module.name)}</strong>
        <span class="module-status ${module.installed ? "is-available" : ""}">${module.installed ? "Eingerichtet" : "Verfügbar"}</span>
      </div>
      <p class="module-description">${escapeHTML(module.description || "Modul aus dem Lesezeichen-Hub")}</p>
      <div class="module-actions">
        <a class="secondary-link" href="${escapeHTML(module.repository_url)}" target="_blank" rel="noreferrer">Repository</a>
        ${module.installed
          ? `<a class="primary-link" href="${escapeHTML(module.local_url)}">Öffnen</a>`
          : '<button class="install-module" type="button">Herunterladen &amp; einrichten</button>'}
      </div>`;
    const installButton = card.querySelector(".install-module");
    installButton?.addEventListener("click", async () => {
      try {
        installButton.disabled = true;
        installButton.textContent = "Wird eingerichtet...";
        setStatus(`${module.name} wird heruntergeladen und eingerichtet...`);
        await request(`/api/module-catalog/${encodeURIComponent(module.name)}/install`, { method: "POST" });
        setStatus(`${module.name} wurde eingerichtet.`);
        await loadModules();
      } catch (error) {
        installButton.disabled = false;
        installButton.textContent = "Herunterladen & einrichten";
        setStatus(error.message || "Modul konnte nicht eingerichtet werden.", true);
      }
    });
    els.catalogList.appendChild(card);
  }
}

function renderModules(modules) {
  els.list.innerHTML = "";
  const available = modules.filter((item) => item.available).length;
  els.summary.textContent = `${modules.length} Module, davon ${available} verfügbar.`;
  if (modules.length === 0) {
    const empty = document.createElement("div");
    empty.className = "empty-state";
    empty.innerHTML = '<p>Noch keine lokalen Module registriert.</p><a class="primary-link" href="/?add=module">Erstes Modul hinzufügen</a>';
    els.list.appendChild(empty);
    return;
  }

  for (const module of modules) {
    const card = document.createElement("article");
    card.className = "module-card is-installed";
    card.innerHTML = `
      <div class="module-card-head">
        <strong>${escapeHTML(module.name)}</strong>
        <span class="module-status ${module.available ? "is-available" : "is-missing"}">${module.available ? "Verfügbar" : "Nicht erreichbar"}</span>
      </div>
      ${module.error ? `<p class="module-error">${escapeHTML(module.error)}</p>` : ""}
      <form class="module-edit-form">
        <label>Name<input name="name" value="${escapeHTML(module.name)}" maxlength="100" required /></label>
        <label>Lokaler Ordner<span class="path-picker"><input name="path" value="${escapeHTML(module.path)}" required /><button class="ghost choose-path" type="button">Ordner wählen</button></span></label>
        <div class="module-actions">
          <a class="secondary-link ${module.available ? "" : "is-disabled"}" href="${escapeHTML(module.url)}" target="_blank" rel="noreferrer" ${module.available ? "" : 'aria-disabled="true"'}>Öffnen</a>
          <button type="submit">Änderungen speichern</button>
          <button class="danger delete-module" type="button">Vollständig löschen</button>
        </div>
      </form>`;

    const form = card.querySelector("form");
    card.querySelector(".choose-path").addEventListener("click", async () => {
      try {
        setStatus("Ordnerdialog wird geöffnet...");
        const result = await request("/api/module-folder");
        if (result.path) form.elements.path.value = result.path;
        setStatus(result.path ? "Ordner ausgewählt." : "Keine Auswahl getroffen.");
      } catch (error) {
        setStatus(error.message, true);
      }
    });
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      try {
        await request(`/api/modules/${module.id}`, {
          method: "PUT",
          body: JSON.stringify({ name: form.elements.name.value.trim(), path: form.elements.path.value.trim() }),
        });
        setStatus("Modul aktualisiert.");
        await loadModules();
      } catch (error) {
        setStatus(error.message, true);
      }
    });
    card.querySelector(".delete-module").addEventListener("click", async () => {
      if (!confirm(`Modul „${module.name}“ samt Start-Lesezeichen vollständig löschen?`)) return;
      try {
        await request(`/api/modules/${module.id}`, { method: "DELETE" });
        setStatus("Modul vollständig gelöscht.");
        await loadModules();
      } catch (error) {
        setStatus(error.message, true);
      }
    });
    els.list.appendChild(card);
  }
}

async function request(path, options = {}) {
  const response = await fetch(path, {
    headers: { "Content-Type": "application/json", ...(options.headers || {}) },
    ...options,
  });
  const payload = response.headers.get("content-type")?.includes("application/json") ? await response.json() : null;
  if (!response.ok) throw new Error(payload?.error || `Fehler (${response.status})`);
  return payload;
}

function setStatus(message, isError = false) {
  els.status.textContent = message;
  els.status.style.color = isError ? "#ffb3ba" : "#97a7bf";
}

function escapeHTML(value) {
  return String(value ?? "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}