# Lesezeichen Hub 2.1

Version 2.1 macht den Lesezeichen-Hub im lokalen Betrieb robuster und vereinfacht die taegliche Bedienung.

## Bedienung

### Vereinfachte Startseite

- Der Dialog **Hinzufuegen** buendelt das Anlegen von Lesezeichen, Gruppen und lokalen Modulen an einer Stelle.
- **Hinzufuegen** und **Verwalten** liegen gemeinsam in der Gruppen-Kopfzeile.
- Selten benoetigte Datenaktionen wie Import, Export, Vollsicherung und Wiederherstellung werden erst unter **Verwalten** angezeigt.
- Die Gruppenbearbeitung und der Gruppenexport verwenden jetzt vollwertige Dialoge statt Browser-Prompts.
- Der Verwaltungsbereich laesst sich verlaesslich ein- und ausblenden.

### Tastatur

- `/` setzt den Fokus auf die Suche.
- `N` oeffnet den Dialog fuer ein neues Lesezeichen.
- `G` oeffnet den Dialog fuer eine neue Gruppe.
- `Esc` schliesst offene Dialoge.

### Edelmetallanzeige

- Unter **Verwalten** kann die Edelmetallanzeige ein- und ausgeblendet werden.
- Die Auswahl wird lokal im Browser gespeichert und bleibt nach einem Neuladen erhalten.
- Beim Ausblenden werden keine weiteren Preisabfragen aus der aktuellen Seite ausgeloest.

### Anzeige und Design

- Unter **Verwalten** kann zwischen dem bisherigen modernen Design und dem neuen **ASCII-Monitor**-Design gewechselt werden.
- Das ASCII-Monitor-Design verwendet einen schwarzen, fast monochromen Look mit Monospace-Schrift, feinen Scanlines und entsaettigten Symbolen.
- Die Designwahl wird lokal im Browser gespeichert und gilt fuer Startseite, Archiv, Uebersicht, Notizen, Statistiken und Silberpreis-Finder.

## Sicherheit und Stabilitaet

- Der Server bindet standardmaessig nur noch an `127.0.0.1` und ist damit nicht mehr ohne ausdrueckliche Konfiguration im lokalen Netzwerk erreichbar.
- Die Start- und Stopskripte pruefen den vollstaendigen EXE-Pfad, bevor ein Prozess beendet wird. Eine veraltete PID-Datei kann dadurch keinen fremden Prozess mehr beenden.
- SQLite verwendet WAL-Modus, Busy-Timeout und eine begrenzte Verbindungszahl fuer stabilere lokale Zugriffe.
- Externe Edelmetallquellen lassen sich serverseitig mit `ENABLE_EXTERNAL_PRICES=false` deaktivieren.

## Tests und Wartung

- Neue automatisierte Tests pruefen URL-Validierung, SQLite-Schema und WAL-Modus sowie die Konfiguration externer Preisabfragen.
- Die Mindestversion fuer Builds ist Go 1.23.

## Upgrade-Hinweis

Vor einem Update empfiehlt sich weiterhin eine Vollsicherung ueber **Verwalten > Vollsicherung**. Bestehende Datenbanken werden beim Start automatisch migriert.

Wenn der Hub bisher absichtlich aus dem lokalen Netzwerk erreichbar war, muss die Bindungsadresse beim Start nun explizit gesetzt werden, zum Beispiel mit `ADDR=0.0.0.0:3233`.
