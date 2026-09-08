# Lesezeichen Hub 4.4

Version 4.4 erweitert den Silberpreisverlauf um eine bessere Navigation durch
gespeicherte Preisstaende und detailliertere Diagramminformationen.

## Silberpreisverlauf

- Zeitraeume fuer Tag, Woche, Monat und Jahr lassen sich mit Pfeiltasten durch
  die gespeicherte Historie bewegen.
- Die Verlaufansicht kennt den fruehesten und letzten gespeicherten Messpunkt
  und deaktiviert die Rueckwaertsnavigation, wenn keine aelteren Werte mehr
  vorhanden sind.
- Die Schaltflaeche **Heute** stellt den aktuellen Zeitraum wieder her.
- Die Zeitachse zeigt bis zu sechs gleichmaessig verteilte Messzeitpunkte. Bei
  mehreren Messungen an einem Tag erscheinen Uhrzeiten; bei gleichen Minuten
  auch Sekunden.
- Beim Ueberfahren eines Diagrammpunkts erscheinen Zeitpunkt, Silberpreis je
  Gramm, guenstigster 1oz-Preis und der gespeicherte Produktname.

## Technische Aenderungen

- Der neue Endpunkt `/api/silver-price-history-bounds` stellt der Verlaufansicht
  den fruehesten und neuesten gespeicherten Preisstand bereit.
- Der Endpunkt ist durch einen HTTP-Test abgesichert.

## Upgrade-Hinweis

Nach dem Update den Lesezeichen-Hub neu starten. Eine Datenbankmigration oder
manuelle Anpassung bestehender Daten ist nicht erforderlich.