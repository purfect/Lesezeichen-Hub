(() => {
  const routes = [
    { href: "/", label: "Lesezeichen", icon: "⌂" },
    { href: "/static/overview.html", label: "Übersicht", icon: "◫" },
    { href: "/static/archive.html", label: "Archiv", icon: "▣" },
    { href: "/static/notes.html", label: "Notizen", icon: "✎" },
    { href: "/static/stats.html", label: "Statistiken", icon: "↗" },
    { href: "/silberpreis-verlauf", label: "Silberverlauf", icon: "⌁", requiresMetalPrices: true },
    { href: "/static/modules.html", label: "Module", icon: "▦" },
  ];
  const nav = document.createElement("nav");
  nav.className = "app-nav";
  nav.setAttribute("aria-label", "Hauptnavigation");
  document.querySelector(".topbar")?.insertAdjacentElement("afterend", nav);

  function renderNavigation(metalPricesEnabled) {
    nav.replaceChildren();
    for (const route of routes) {
      if (route.requiresMetalPrices && !metalPricesEnabled) continue;
      const link = document.createElement("a");
      link.href = route.href;
      link.innerHTML = `<span aria-hidden="true">${route.icon}</span><span>${route.label}</span>`;
      const currentPath = location.pathname === "/silver-preise" ? "/" : location.pathname;
      if (currentPath === route.href) link.setAttribute("aria-current", "page");
      nav.appendChild(link);
    }
  }

  renderNavigation(false);
  fetch("/api/config", { cache: "no-store" })
    .then((response) => response.ok ? response.json() : null)
    .then((config) => renderNavigation(Boolean(config?.metal_prices_enabled)))
    .catch(() => renderNavigation(false));
  window.addEventListener("metal-prices-visibility-change", (event) => {
    renderNavigation(Boolean(event.detail?.visible));
  });

  const topButton = document.createElement("button");
  topButton.className = "scroll-top-button";
  topButton.type = "button";
  topButton.setAttribute("aria-label", "Nach oben");
  topButton.title = "Nach oben";
  topButton.textContent = "↑";
  document.body.appendChild(topButton);

  topButton.addEventListener("click", () => {
    window.scrollTo({ top: 0, behavior: "smooth" });
  });

  let ticking = false;
  const updateTopButton = () => {
    topButton.classList.toggle("visible", window.scrollY > 360);
    ticking = false;
  };
  window.addEventListener("scroll", () => {
    if (ticking) return;
    ticking = true;
    window.requestAnimationFrame(updateTopButton);
  }, { passive: true });
  updateTopButton();
})();
