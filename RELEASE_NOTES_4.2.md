# Lesezeichen Hub 4.2

Version 4.2 verfeinert die Darstellung des Silberpreisverlaufs im ASCII-Monitor-Design und gliedert das Hub-Backend nach seinen Fachbereichen.

## Theme-Abhaengiger Silberpreisverlauf

- Der Silberpreisverlauf verwendet weiterhin das vorhandene Liniendiagramm in allen Designs.
- Im ASCII-Monitor-Design orientiert sich die Verlaufskarte jetzt am schwarzen Hub-Hintergrund.
- Die Reihe **Silber EUR/g** erscheint dort in deutlich erkennbarem Monitorgruen, waehrend das guenstigste 1oz-Angebot weiss bleibt.
- Raster, Beschriftungen und Legende sind auf den dunklen Monitorhintergrund abgestimmt.
- Die Kennzahlenleisten verwenden dezentes Neon-Moosgruen, ohne die Diagrammflaeche zu dominieren.
- Die Zeitraumauswahl bleibt auch auf schmalen Ansichten innerhalb des Panels bedienbar.

## Wartbareres Backend

- Der bisherige Inhalt von `main.go` ist in fachlich getrennte Dateien fuer Datenbank, HTTP-Helfer, Lesezeichen, Notizen, Module, Importe/Exporte, Preise und Updates aufgeteilt.
- `main.go` konzentriert sich jetzt auf Start, Konfiguration, eingebettete Web-Dateien und Routen.
- Das Refactoring aendert weder die HTTP-Endpunkte noch das Datenformat oder bestehende Datenbanken.

## Qualitaetssicherung

- Die vollstaendige Go-Testsuite wurde erfolgreich ausgefuehrt.
- `go vet ./...` und die Editor-Diagnostik wurden ohne Befund ausgefuehrt.
- Die Darstellung des Silberpreisverlaufs wurde fuer modernes Design und ASCII-Monitor-Design angepasst.

## Upgrade-Hinweis

Nach dem Update den Lesezeichen-Hub neu starten. Eine Datenbankmigration oder manuelle Anpassung bestehender Daten ist nicht erforderlich.
