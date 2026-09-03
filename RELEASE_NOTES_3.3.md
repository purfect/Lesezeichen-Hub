# Lesezeichen Hub 3.3

Version 3.3 macht die Modulverwaltung ruhiger und besser skalierbar und verbessert die Rückmeldung beim Abruf von GitHub-Releases.

## Übersichtlichere Modulverwaltung

- Der GitHub-Katalog verwendet nun eine kompakte Listenansicht statt einer gleichförmigen Kartenmatrix.
- Name, Beschreibung, Installationsstatus und Aktionen sind pro Modul klar zusammengefasst und auch bei langen Katalogen schnell erfassbar.
- Registrierte lokale Module erscheinen als schlanke Verwaltungszeilen.
- **Öffnen** bleibt immer sichtbar; selten benötigte Einstellungen wie Ordnerpfad, Umbenennen, Aktualisieren und vollständiges Löschen liegen unter dem aufklappbaren Bereich **Verwalten**.
- Die Seite passt sich auf kleinen Bildschirmen an und stapelt Status sowie Aktionen bedienbar untereinander.
- Module werden beim Öffnen aus dem Katalog und der registrierten Liste stets in einem neuen Browser-Tab geöffnet.

## GitHub Release-Check

- Der Update-Check verwendet jetzt, wie der Modulkatalog, optional `GITHUB_TOKEN` als Bearer-Token.
- Bei einem erschöpften GitHub-API-Kontingent erscheint eine konkrete Meldung mit der Empfehlung, später erneut zu prüfen oder `GITHUB_TOKEN` zu setzen.
- Ein Regressionstest deckt den GitHub-Status `403` bei ausgeschöpftem API-Limit ab.

## Qualitätssicherung

- JavaScript-Syntaxprüfung der Modulseite erfolgreich ausgeführt.
- Go-Tests des Projekts erfolgreich ausgeführt.

## Upgrade-Hinweis

Nach dem Update den Lesezeichen-Hub neu starten. Bestehende Datenbanken benötigen keine Migration.

Für häufige GitHub-Abfragen kann ein Fine-grained Token mit mindestens **Metadata: Read-only** für `purfect/Lesezeichen-Hub` als Umgebungsvariable `GITHUB_TOKEN` gesetzt werden. Der Token darf nicht in das Repository oder in eingecheckte Startskripte aufgenommen werden.