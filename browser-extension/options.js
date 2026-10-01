const hubUrlInput = document.getElementById("hub-url");
const statusNode = document.getElementById("status");

getHubUrl().then((url) => { hubUrlInput.value = url; });

document.getElementById("options-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const url = normalizeHubUrl(hubUrlInput.value);
  if (!url) {
    setStatus("Ungültige Adresse. Beispiel: http://127.0.0.1:3333", "error");
    return;
  }
  await ext.storage.local.set({ hubUrl: url });
  hubUrlInput.value = url;
  setStatus("Gespeichert.", "success");
});

document.getElementById("test").addEventListener("click", async () => {
  const url = normalizeHubUrl(hubUrlInput.value);
  if (!url) {
    setStatus("Ungültige Adresse. Beispiel: http://127.0.0.1:3333", "error");
    return;
  }
  try {
    const response = await fetch(`${url}/api/config`, { headers: { "Accept": "application/json" } });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const config = await response.json();
    setStatus(`Verbunden – Hub-Version ${config.version || "unbekannt"}.`, "success");
  } catch (error) {
    setStatus(`Keine Verbindung zu ${url} (${error.message}).`, "error");
  }
});

function setStatus(message, kind) {
  statusNode.textContent = message;
  statusNode.classList.toggle("is-error", kind === "error");
  statusNode.classList.toggle("is-success", kind === "success");
}
