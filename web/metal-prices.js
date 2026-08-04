const metalPriceEls = {
  gold: document.getElementById("metal-price-gold"),
  silver: document.getElementById("metal-price-silver"),
  updated: document.getElementById("metal-price-updated"),
  hint: document.getElementById("metal-price-hint"),
};

if (metalPriceEls.gold && metalPriceEls.silver && metalPriceEls.updated && metalPriceEls.hint) {
  initMetalPricesFooter();
}

function initMetalPricesFooter() {
  loadMetalPrices();
  setInterval(loadMetalPrices, 2 * 60 * 60 * 1000);
}

async function loadMetalPrices() {
  try {
    setFooterHint("Lade Edelmetallkurse...");
    const response = await fetch("/api/metal-prices");
    if (!response.ok) {
      throw new Error(`Fehler (${response.status})`);
    }

    const payload = await response.json();
    const gold = Number(payload.gold_eur_per_g);
    const silver = Number(payload.silver_eur_per_g);
    const fetchedAt = payload.fetched_at ? new Date(payload.fetched_at) : null;
    const isStale = Boolean(payload.stale);

    if (!Number.isFinite(gold) || !Number.isFinite(silver)) {
      throw new Error("Kurswerte ungueltig");
    }

    metalPriceEls.gold.textContent = `${formatEURPerGram(gold)} €/g`;
    metalPriceEls.silver.textContent = `${formatEURPerGram(silver)} €/g`;

    if (fetchedAt && !Number.isNaN(fetchedAt.getTime())) {
      metalPriceEls.updated.textContent = `Stand: ${fetchedAt.toLocaleString("de-DE")}`;
    } else {
      metalPriceEls.updated.textContent = "Stand: unbekannt";
    }

    if (isStale) {
      setFooterHint("Hinweis: Letzter gueltiger Cachewert (Quelle temporaer nicht erreichbar).", true);
    } else {
      setFooterHint("Quelle: scheideanstalt.de rotierendes Kursbanner (Bankpreis).");
    }
  } catch (error) {
    metalPriceEls.gold.textContent = "-";
    metalPriceEls.silver.textContent = "-";
    metalPriceEls.updated.textContent = "Stand: nicht verfuegbar";
    setFooterHint(error.message || "Kurse konnten nicht geladen werden.", true);
  }
}

function formatEURPerGram(value) {
  return new Intl.NumberFormat("de-DE", {
    minimumFractionDigits: 2,
    maximumFractionDigits: 4,
  }).format(value);
}

function setFooterHint(text, isError = false) {
  metalPriceEls.hint.textContent = text;
  metalPriceEls.hint.classList.toggle("is-error", isError);
}