(() => {
  const routes = [
    { href: "/", label: "Lesezeichen", icon: "⌂" },
    { href: "/static/overview.html", label: "Übersicht", icon: "◫" },
    { href: "/static/archive.html", label: "Archiv", icon: "▣" },
    { href: "/static/notes.html", label: "Notizen", icon: "✎" },
    { href: "/static/stats.html", label: "Statistiken", icon: "↗" },
    { href: "/static/modules.html", label: "Module", icon: "▦" },
  ];
  const nav = document.createElement("nav");
  nav.className = "app-nav";
  nav.setAttribute("aria-label", "Hauptnavigation");
  for (const route of routes) {
    const link = document.createElement("a");
    link.href = route.href;
    link.innerHTML = `<span aria-hidden="true">${route.icon}</span><span>${route.label}</span>`;
    const currentPath = location.pathname === "/silver-preise" ? "/" : location.pathname;
    if (currentPath === route.href) link.setAttribute("aria-current", "page");
    nav.appendChild(link);
  }
  document.querySelector(".topbar")?.insertAdjacentElement("afterend", nav);
})();