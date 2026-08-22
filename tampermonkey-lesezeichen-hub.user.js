// ==UserScript==
// @name         Lesezeichen Hub Saver
// @namespace    https://local.lesezeichen-hub
// @version      1.0.0
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
      const groupsPayload = await requestJson("GET", "/api/groups");
      const groups = Array.isArray(groupsPayload.groups) ? groupsPayload.groups : [];

      if (groups.length === 0) {
        notifyError("Keine Gruppen gefunden. Bitte zuerst im Hub eine Gruppe anlegen.");
        return;
      }

      const selectedGroup = pickGroup(groups);
      if (!selectedGroup) {
        return;
      }

      const title = askTitle();
      if (!title) {
        return;
      }

      const notes = prompt("Notiz (optional):", "") || "";
      const tagsInput = prompt("Tags (optional, komma-getrennt):", "") || "";

      const payload = {
        group_id: selectedGroup.id,
        title,
        url: window.location.href,
        notes: notes.trim(),
        tags: parseTags(tagsInput),
        favorite: false,
        pinned: false,
        archived: false,
        sort_order: 0,
        remind_at: "",
      };

      await requestJson("POST", "/api/bookmarks", payload);
      localStorage.setItem(LAST_GROUP_ID_STORAGE_KEY, String(selectedGroup.id));
      notifySuccess(`Gespeichert in Gruppe: ${selectedGroup.name}`);
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

  function pickGroup(groups) {
    const preferred = Number(localStorage.getItem(LAST_GROUP_ID_STORAGE_KEY) || "0");
    let defaultIndex = 0;

    if (preferred > 0) {
      const idx = groups.findIndex((group) => group.id === preferred);
      if (idx >= 0) {
        defaultIndex = idx;
      }
    }

    const lines = groups.map((group, index) => `${index + 1}) ${group.name}`);
    const message = [
      "Gruppe waehlen (Nummer eingeben):",
      "",
      ...lines,
      "",
      `Standard: ${defaultIndex + 1}`,
    ].join("\n");

    const raw = prompt(message, String(defaultIndex + 1));
    if (raw === null) {
      return null;
    }

    const selectedIndex = Number(raw.trim()) - 1;
    if (!Number.isInteger(selectedIndex) || selectedIndex < 0 || selectedIndex >= groups.length) {
      notifyError("Ungueltige Auswahl.");
      return null;
    }

    return groups[selectedIndex];
  }

  function askTitle() {
    const fallbackTitle = (document.title || window.location.hostname || "Neues Lesezeichen").trim();
    const input = prompt("Titel:", fallbackTitle);
    if (input === null) {
      return "";
    }
    const title = input.trim();
    if (!title) {
      notifyError("Titel darf nicht leer sein.");
      return "";
    }
    return title;
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
