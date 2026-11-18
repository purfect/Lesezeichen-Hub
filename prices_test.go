package main

import "testing"

func TestSilverProductsUseOwnCardNamePriceAndLink(t *testing.T) {
	source := "https://www.edelmetall-handel.de/anlegen/silber"
	html := `<a href="/anlegen/silber">Silbermünze 1oz 1,00 €</a>
<product-item>
  <a href="/image"><img alt="Silbermünze 1oz Bild"></a>
  <a href="/valid"><span class="product-item__name js-product-item__name">Silbermünze 1oz &quot;Känguru&quot;</span></a>
  <span class="money-price__amount js-product-item__default-price">52,59&nbsp;€</span>
</product-item>
<product-item>
  <a href="/no-price"><span class="product-item__name">Silbermünze 1oz ohne Preis</span></a>
</product-item>
<product-item>
  <a href="/half"><span class="product-item__name">Silbermünze 1/2oz</span></a>
  <span class="js-product-item__default-price">20,00 €</span>
</product-item>
<product-item>
  <a href="/bag"><span class="product-item__name">Silbermünze 20x 1oz Junk-Bag</span></a>
  <span class="js-product-item__default-price">900,00 €</span>
</product-item>
<product-item>
  <a href="/junk-silbermuenze-1oz-american-eagle-usa-10202187"><span class="product-item__name">JUNK-Silbermünze 1oz "American Eagle" (USA)</span></a>
  <span class="js-product-item__default-price">46,57 €</span>
</product-item>`
	products, err := parseSilverProductsFromHTML(html, source)
	if err != nil {
		t.Fatal(err)
	}
	if len(products) != 1 || products[0].Name != `Silbermünze 1oz "Känguru"` ||
		products[0].Price != 52.59 || products[0].URL != "https://www.edelmetall-handel.de/valid" {
		t.Fatalf("products = %#v", products)
	}
}
