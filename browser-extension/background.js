// Chrome loads this file as service worker, Firefox loads api.js via manifest "scripts".
if (typeof importScripts === "function") importScripts("api.js");

const MENU_PAGE = "hub-save-page";
const MENU_LINK = "hub-save-link";
const MENU_SELECTION = "hub-save-selection";

async function createMenus() {
  await ext.contextMenus.removeAll();
  ext.contextMenus.create({ id: MENU_PAGE, title: "Seite im Lesezeichen-Hub speichern", contexts: ["page"] });
  ext.contextMenus.create({ id: MENU_LINK, title: "Link im Lesezeichen-Hub speichern", contexts: ["link"] });
  ext.contextMenus.create({ id: MENU_SELECTION, title: "Auswahl als Notiz im Lesezeichen-Hub speichern", contexts: ["selection"] });
}

ext.runtime.onInstalled.addListener(() => { createMenus().catch(console.error); });
ext.runtime.onStartup.addListener(() => { createMenus().catch(console.error); });

ext.contextMenus.onClicked.addListener((info, tab) => {
  handleMenuClick(info, tab).catch((error) => notify("Speichern fehlgeschlagen", error.message));
});

async function handleMenuClick(info, tab) {
  if (info.menuItemId === MENU_SELECTION) {
    const url = info.pageUrl || tab?.url || "";
    const title = (tab?.title || "Notiz aus dem Browser").slice(0, 200);
    await hubRequest("/api/notes", {
      method: "POST",
      body: { title, content: noteContent(info.selectionText, url), type: "note" },
    });
    notify("Notiz gespeichert", title);
    return;
  }

  const isLink = info.menuItemId === MENU_LINK;
  const url = isLink ? info.linkUrl : (info.pageUrl || tab?.url);
  if (!isSavableUrl(url)) throw new Error("Nur http- und https-Adressen können gespeichert werden.");
  // Chrome does not provide linkText, so fall back to the URL
  const title = ((isLink ? info.linkText : tab?.title) || url).trim().slice(0, 120);

  const groups = await loadGroups();
  const groupId = await preferredGroupId(groups);
  if (!groupId) throw new Error("Keine Gruppe vorhanden. Bitte zuerst im Hub eine Gruppe anlegen.");
  await hubRequest("/api/bookmarks", { method: "POST", body: bookmarkPayload({ groupId, title, url }) });
  const groupName = groups.find((group) => group.id === groupId)?.name || "";
  notify("Lesezeichen gespeichert", `${title} → ${groupName}`);
}

function notify(title, message) {
  ext.notifications.create({
    type: "basic",
    iconUrl: ext.runtime.getURL("icons/icon-128.png"),
    title,
    message: String(message || ""),
  });
}
