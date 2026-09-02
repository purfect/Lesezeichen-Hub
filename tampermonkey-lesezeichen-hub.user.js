// ==UserScript==
// @name         Lesezeichen Hub Saver
// @namespace    https://local.lesezeichen-hub
// @version      1.1.0
// @description  Speichert die aktuelle Webseite direkt im lokalen Lesezeichen-Hub.
// @match        *://*/*
// @grant        GM_registerMenuCommand
// @grant        GM_xmlhttpRequest
// @grant        GM_notification
// @connect      localhost
// @connect      127.0.0.1
// ==/UserScript==

(function () {
  "use strict";

  const BASE_URL_STORAGE_KEY = "lesezeichenHubBaseUrl";
  const LAST_GROUP_ID_STORAGE_KEY = "lesezeichenHubLastGroupId";
  const FLOATING_BUTTON_ENABLED_KEY = "lesezeichenHubFloatingButtonEnabled";
  const DEFAULT_BASE_URL = "http://localhost:3233";
  const FLOATING_BUTTON_ID = "lesezeichen-hub-floating-save-button";

  const menuCreateLabel = "Im Lesezeichen-Hub speichern";
  const menuSettingsLabel = "Lesezeichen-Hub URL setzen";
  const menuToggleButtonLabel = "Mini-Icon ein/aus";

  GM_registerMenuCommand(menuCreateLabel, () => {
    void saveCurrentPage();
  });

  GM_registerMenuCommand(menuSettingsLabel, configureBaseUrl);
  GM_registerMenuCommand(menuToggleButtonLabel, toggleFloatingButton);

  initFloatingButton();

  document.addEventListener("keydown", (event) => {
    // Alt+Shift+B als schneller Shortcut.
    if (event.altKey && event.shiftKey && event.key.toLowerCase() === "b") {
      event.preventDefault();
      void saveCurrentPage();
    }
  });

  async function saveCurrentPage() {
    if (!isSupportedPage()) {
      notifyError("Nur http/https Seiten koennen gespeichert werden.");
      return;
    }

    try {
      const statePayload = await requestJson("GET", "/api/state");
      const groups = Array.isArray(statePayload.groups) ? statePayload.groups : [];

      if (groups.length === 0) {
        notifyError("Keine Gruppen gefunden. Bitte zuerst im Hub eine Gruppe anlegen.");
        return;
      }

      const tagSuggestions = [...new Set(groups
        .flatMap((group) => group.bookmarks || [])
        .flatMap((bookmark) => bookmark.tags || [])
        .map((tag) => String(tag).trim())
        .filter(Boolean))].sort((a, b) => a.localeCompare(b, "de"));
      const formData = await showSaveForm(groups, tagSuggestions);
      if (!formData) return;

      const payload = {
        group_id: formData.groupId,
        title: formData.title,
        url: window.location.href,
        notes: formData.notes,
        tags: parseTags(formData.tags),
        favorite: formData.favorite,
        pinned: formData.pinned,
        archived: false,
        sort_order: 0,
        remind_at: "",
      };

      await requestJson("POST", "/api/bookmarks", payload);
      localStorage.setItem(LAST_GROUP_ID_STORAGE_KEY, String(formData.groupId));
      const selectedGroup = groups.find((group) => group.id === formData.groupId);
      notifySuccess(`Gespeichert in Gruppe: ${selectedGroup?.name || "Unbekannt"}`);
    } catch (error) {
      notifyError(error.message || "Speichern fehlgeschlagen.");
    }
  }

  function configureBaseUrl() {
    const current = getBaseUrl();
    const input = prompt("Hub URL eingeben (z.B. http://localhost:3233):", current);
    if (input === null) {
      return;
    }

    const trimmed = input.trim().replace(/\/$/, "");
    if (!trimmed) {
      localStorage.removeItem(BASE_URL_STORAGE_KEY);
      notifySuccess(`Hub URL zurueckgesetzt auf Standard: ${DEFAULT_BASE_URL}`);
      return;
    }

    try {
      const url = new URL(trimmed);
      if (url.protocol !== "http:" && url.protocol !== "https:") {
        throw new Error("Ungueltiges Protokoll");
      }
      localStorage.setItem(BASE_URL_STORAGE_KEY, `${url.protocol}//${url.host}`);
      notifySuccess(`Hub URL gespeichert: ${url.protocol}//${url.host}`);
    } catch (_error) {
      notifyError("Ungueltige URL. Beispiel: http://localhost:3233");
    }
  }

  function toggleFloatingButton() {
    const enabled = isFloatingButtonEnabled();
    localStorage.setItem(FLOATING_BUTTON_ENABLED_KEY, enabled ? "0" : "1");

    const existingButton = document.getElementById(FLOATING_BUTTON_ID);
    if (existingButton) {
      existingButton.remove();
    }

    if (enabled) {
      notifySuccess("Mini-Icon deaktiviert.");
      return;
    }

    initFloatingButton();
    notifySuccess("Mini-Icon aktiviert.");
  }

  function showSaveForm(groups, tagSuggestions) {
    const preferred = Number(localStorage.getItem(LAST_GROUP_ID_STORAGE_KEY) || "0");
    const fallbackTitle = (document.title || window.location.hostname || "Neues Lesezeichen").trim();
    const host = document.createElement("div");
    host.id = "lesezeichen-hub-save-dialog";
    const shadow = host.attachShadow({ mode: "closed" });
    const groupOptions = groups.map((group, index) => {
      const selected = group.id === preferred || (!preferred && index === 0) ? " selected" : "";
      return `<option value="${group.id}"${selected}>${escapeHTML(group.name)}</option>`;
    }).join("");
    const tagOptions = tagSuggestions.map((tag) => `<option value="${escapeHTML(tag)}"></option>`).join("");
    shadow.innerHTML = `
      <style>
        :host { all: initial; }
        .backdrop { position: fixed; inset: 0; z-index: 2147483647; display: grid; place-items: center; padding: 16px; background: rgba(5, 12, 20, .68); font-family: "Segoe UI", sans-serif; color: #e8eef7; }
        form { width: min(440px, calc(100vw - 32px)); box-sizing: border-box; padding: 18px; border: 1px solid #365069; border-radius: 10px; background: #101a27; box-shadow: 0 20px 60px rgba(0, 0, 0, .5); }
        header { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 14px; }
        h2 { margin: 0; font-size: 20px; letter-spacing: 0; }
        label { display: grid; gap: 5px; margin: 10px 0; font-size: 13px; color: #b8c5d8; }
        input, textarea, select, button { box-sizing: border-box; font: inherit; }
        input, textarea, select { width: 100%; padding: 9px 10px; border: 1px solid #365069; border-radius: 7px; color: #e8eef7; background: #0a121d; }
        textarea { min-height: 70px; resize: vertical; }
        .checks { display: flex; gap: 18px; }
        .checks label { display: flex; align-items: center; gap: 7px; color: #e8eef7; }
        .checks input { width: auto; }
        .actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 16px; }
        button { min-height: 40px; padding: 8px 13px; border: 1px solid #365069; border-radius: 7px; cursor: pointer; color: #e8eef7; background: #18283a; }
        button[type="submit"] { border-color: #2cb7a1; color: #041512; background: #2cb7a1; font-weight: 700; }
        .close { width: 40px; padding: 0; font-size: 20px; }
      </style>
      <div class="backdrop">
        <form>
          <header><h2>Im Lesezeichen-Hub speichern</h2><button class="close" type="button" aria-label="Schließen">×</button></header>
          <label>Gruppe<select name="group">${groupOptions}</select></label>
          <label>Titel<input name="title" value="${escapeHTML(fallbackTitle)}" maxlength="120" required /></label>
          <label>Notiz<textarea name="notes" maxlength="500" placeholder="Optional"></textarea></label>
          <label>Tags<input name="tags" list="hub-tag-suggestions" placeholder="Kommagetrennt" /><datalist id="hub-tag-suggestions">${tagOptions}</datalist></label>
          <div class="checks"><label><input name="favorite" type="checkbox" /> Favorit</label><label><input name="pinned" type="checkbox" /> Angepinnt</label></div>
          <div class="actions"><button class="cancel" type="button">Abbrechen</button><button type="submit">Speichern</button></div>
        </form>
      </div>`;
    document.body.appendChild(host);

    return new Promise((resolve) => {
      const form = shadow.querySelector("form");
      const finish = (result) => {
        document.removeEventListener("keydown", onKeyDown, true);
        host.remove();
        resolve(result);
      };
      const onKeyDown = (event) => {
        if (event.key === "Escape") finish(null);
      };
      document.addEventListener("keydown", onKeyDown, true);
      shadow.querySelector(".close").addEventListener("click", () => finish(null));
      shadow.querySelector(".cancel").addEventListener("click", () => finish(null));
      shadow.querySelector(".backdrop").addEventListener("click", (event) => {
        if (event.target.classList.contains("backdrop")) finish(null);
      });
      form.addEventListener("submit", (event) => {
        event.preventDefault();
        const data = new FormData(form);
        finish({
          groupId: Number(data.get("group")),
          title: String(data.get("title") || "").trim(),
          notes: String(data.get("notes") || "").trim(),
          tags: String(data.get("tags") || ""),
          favorite: data.get("favorite") === "on",
          pinned: data.get("pinned") === "on",
        });
      });
      form.elements.title.focus();
      form.elements.title.select();
    });
  }

  function parseTags(raw) {
    if (!raw) {
      return [];
    }

    return raw
      .split(",")
      .map((item) => item.trim())
      .filter((item, index, arr) => item.length > 0 && arr.indexOf(item) === index);
  }

  function escapeHTML(value) {
    return String(value ?? "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function isSupportedPage() {
    return window.location.protocol === "http:" || window.location.protocol === "https:";
  }

  function isFloatingButtonEnabled() {
    const stored = localStorage.getItem(FLOATING_BUTTON_ENABLED_KEY);
    if (stored === null) {
      return true;
    }
    return stored !== "0";
  }

  function initFloatingButton() {
    if (!isSupportedPage()) {
      return;
    }
    if (window.top !== window.self) {
      return;
    }
    if (!isFloatingButtonEnabled()) {
      return;
    }
    if (document.getElementById(FLOATING_BUTTON_ID)) {
      return;
    }

    const button = document.createElement("button");
    button.id = FLOATING_BUTTON_ID;
    button.type = "button";
    button.textContent = "★";
    button.title = "Im Lesezeichen-Hub speichern";
    button.setAttribute("aria-label", "Im Lesezeichen-Hub speichern");
    button.style.position = "fixed";
    button.style.right = "14px";
    button.style.bottom = "14px";
    button.style.width = "28px";
    button.style.height = "28px";
    button.style.border = "none";
    button.style.borderRadius = "999px";
    button.style.background = "#1d4ed8";
    button.style.color = "#ffffff";
    button.style.fontSize = "14px";
    button.style.fontWeight = "700";
    button.style.fontFamily = "system-ui, sans-serif";
    button.style.cursor = "pointer";
    button.style.zIndex = "2147483647";
    button.style.boxShadow = "0 2px 8px rgba(0, 0, 0, 0.25)";
    button.style.opacity = "0.88";
    button.style.lineHeight = "1";

    button.addEventListener("mouseenter", () => {
      button.style.opacity = "1";
      button.style.transform = "scale(1.05)";
    });
    button.addEventListener("mouseleave", () => {
      button.style.opacity = "0.88";
      button.style.transform = "scale(1)";
    });
    button.addEventListener("click", (event) => {
      event.preventDefault();
      event.stopPropagation();
      void saveCurrentPage();
    });

    document.body.appendChild(button);
  }

  function getBaseUrl() {
    const stored = localStorage.getItem(BASE_URL_STORAGE_KEY);
    const value = (stored || DEFAULT_BASE_URL).trim();
    return value.replace(/\/$/, "");
  }

  function requestJson(method, path, body) {
    const url = `${getBaseUrl()}${path}`;
    const requestBody = body === undefined ? undefined : JSON.stringify(body);

    return new Promise((resolve, reject) => {
      GM_xmlhttpRequest({
        method,
        url,
        headers: {
          "Accept": "application/json",
          "Content-Type": "application/json; charset=UTF-8",
        },
        data: requestBody === undefined ? null : String(requestBody),
        timeout: 15000,
        onload: (response) => {
          const text = String(response.responseText || "").replace(/^\uFEFF/, "");
          const payload = tryParseJson(text);

          if (response.status < 200 || response.status >= 300) {
            const message = payload && payload.error
              ? payload.error
              : `HTTP ${response.status}: ${text.slice(0, 180) || "leere Antwort"}`;
            reject(new Error(`${message} (${url})`));
            return;
          }

          if (text && !payload) {
            reject(new Error(`Hub antwortete nicht mit JSON: ${text.slice(0, 180)} (${url})`));
            return;
          }

          resolve(payload || {});
        },
        onerror: () => {
          reject(new Error(`Keine Verbindung zum Hub unter ${url}`));
        },
        ontimeout: () => {
          reject(new Error(`Timeout beim Aufruf von ${url}`));
        },
      });
    });
  }

  function tryParseJson(text) {
    if (!text) {
      return null;
    }
    try {
      return JSON.parse(text);
    } catch (_error) {
      return null;
    }
  }

  function notifySuccess(text) {
    notify(text, "Lesezeichen Hub", false);
  }

  function notifyError(text) {
    notify(text, "Lesezeichen Hub Fehler", true);
  }

  function notify(text, title, isError) {
    try {
      GM_notification({
        text,
        title,
        timeout: isError ? 6000 : 4000,
      });
    } catch (_error) {
      alert(text);
    }
  }
})();
