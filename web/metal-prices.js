const metalPriceEls = {
  gold: document.getElementById("metal-price-gold"),
  silver: document.getElementById("metal-price-silver"),
  bestSilver: document.getElementById("metal-price-best-silver"),
  updated: document.getElementById("metal-price-updated"),
  hint: document.getElementById("metal-price-hint"),
};

if (metalPriceEls.gold && metalPriceEls.silver && metalPriceEls.bestSilver && metalPriceEls.updated && metalPriceEls.hint) {
  initMetalPricesFooter();
}

function initMetalPricesFooter() {
  if (!areMetalPricesVisible()) {
    document.querySelector(".metal-footer")?.classList.add("hidden");
    return;
  }
  loadMetalPrices();
  setInterval(loadMetalPrices, 2 * 60 * 60 * 1000);
}

function areMetalPricesVisible() {
  return localStorage.getItem("lsz_metal_prices_visible") !== "false";
}

window.addEventListener("metal-prices-visibility-change", (event) => {
  const visible = Boolean(event.detail?.visible);
  document.querySelector(".metal-footer")?.classList.toggle("hidden", !visible);
  if (visible) loadMetalPrices();
});

async function loadMetalPrices() {
  try {
    const configResponse = await fetch("/api/config");
    if (!configResponse.ok) throw new Error(`Fehler (${configResponse.status})`);
    const config = await configResponse.json();
    if (!config.external_prices) {
      document.querySelector(".metal-footer")?.classList.add("hidden");
      return;
    }

    setFooterHint("Lade Edelmetallkurse und bestes 1oz-Angebot...");
    const [metalResponse, silverResponse] = await Promise.all([
      fetch("/api/metal-prices"),
      fetch("/api/silver-prices"),
    ]);
    if (!metalResponse.ok || !silverResponse.ok) {
      throw new Error(`Fehler (${metalResponse.status}/${silverResponse.status})`);
    }

    const [metalPayload, silverPayload] = await Promise.all([
      metalResponse.json(),
      silverResponse.json(),
    ]);
    const gold = Number(metalPayload.gold_eur_per_g);
    const silver = Number(metalPayload.silver_eur_per_g);
    const bestProduct = silverPayload.best_product;
    const bestPrice = Number(bestProduct?.price);
    const metalFetchedAt = metalPayload.fetched_at ? new Date(metalPayload.fetched_at) : null;
    const silverFetchedAt = silverPayload.fetched_at ? new Date(silverPayload.fetched_at) : null;
    const isStale = Boolean(metalPayload.stale);

    if (!Number.isFinite(gold) || !Number.isFinite(silver) || !bestProduct || !Number.isFinite(bestPrice) || bestPrice <= 0) {
      throw new Error("Preiswerte ungültig");
    }

    metalPriceEls.gold.textContent = `${formatEURPerGram(gold)} €/g`;
    metalPriceEls.silver.textContent = `${formatEURPerGram(silver)} €/g`;
    metalPriceEls.bestSilver.textContent = `${formatEUR(bestPrice)} € – ${bestProduct.name}`;
    metalPriceEls.bestSilver.href = bestProduct.url || "/silver-preise";
    metalPriceEls.bestSilver.target = bestProduct.url ? "_blank" : "";
    metalPriceEls.bestSilver.rel = bestProduct.url ? "noreferrer" : "";

    const fetchedTimes = [metalFetchedAt, silverFetchedAt]
      .filter((value) => value && !Number.isNaN(value.getTime()))
      .map((value) => value.getTime());
    if (fetchedTimes.length) {
      metalPriceEls.updated.textContent = `Stand: ${new Date(Math.max(...fetchedTimes)).toLocaleString("de-DE")}`;
    } else {
      metalPriceEls.updated.textContent = "Stand: unbekannt";
    }

    if (isStale) {
      setFooterHint("Hinweis: Letzter gültiger Cachewert (Quelle temporär nicht erreichbar).", true);
    } else {
      setFooterHint("Quellen: scheideanstalt.de (Bankpreis) und edelmetall-handel.de (Top-Angebot). Aktualisierung alle 2 Stunden.");
    }
  } catch (error) {
    metalPriceEls.gold.textContent = "-";
    metalPriceEls.silver.textContent = "-";
    metalPriceEls.bestSilver.textContent = "-";
    metalPriceEls.bestSilver.href = "/silver-preise";
    metalPriceEls.bestSilver.removeAttribute("target");
    metalPriceEls.bestSilver.removeAttribute("rel");
    metalPriceEls.updated.textContent = "Stand: nicht verfuegbar";
    setFooterHint(error.message || "Preise konnten nicht geladen werden.", true);
  }
}

function formatEUR(value) {
  return new Intl.NumberFormat("de-DE", {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(value);
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