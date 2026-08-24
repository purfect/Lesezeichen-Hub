const themeStorageKey = "lsz_theme";

function normalizeTheme(theme) {
  return theme === "terminal" ? "terminal" : "modern";
}

function getLesezeichenTheme() {
  return normalizeTheme(localStorage.getItem(themeStorageKey));
}

function setLesezeichenTheme(theme) {
  const normalized = normalizeTheme(theme);
  localStorage.setItem(themeStorageKey, normalized);
  document.documentElement.dataset.theme = normalized;
  window.dispatchEvent(new CustomEvent("lesezeichen-theme-change", { detail: { theme: normalized } }));
}

document.documentElement.dataset.theme = getLesezeichenTheme();
window.getLesezeichenTheme = getLesezeichenTheme;
window.setLesezeichenTheme = setLesezeichenTheme;
