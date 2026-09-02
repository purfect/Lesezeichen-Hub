# Lesezeichen Hub 3.1

Version 3.1 erweitert die lokale Modulverwaltung um einen direkt integrierten GitHub-Katalog. Verfügbare Module können aus dem Hub heraus heruntergeladen, eingerichtet und anschließend wie andere lokale Module geöffnet und verwaltet werden.

## GitHub-Modulkatalog

- Der Bereich **Module** lädt automatisch alle öffentlichen, nicht archivierten Repositorys der GitHub-Organisation `Lesezeichen-Hub`.
- Name, Beschreibung und ein Link zum jeweiligen Repository werden direkt in der Modulliste angezeigt.
- Bereits registrierte Module werden anhand ihres Repositorynamens erkannt.
- Eingerichtete Module sind mit einer grünen Kartenumrandung und dem Status **Eingerichtet** gekennzeichnet.
- Die Liste kann jederzeit über **Liste aktualisieren** erneut von GitHub geladen werden.

## Installation aus dem Hub

- Nicht installierte Module können über **Herunterladen & einrichten** direkt geladen werden.
- Der Hub lädt den Standardbranch des ausgewählten Repositorys als ZIP-Archiv herunter und entpackt ihn in einen verwalteten Modulordner.
- Als Einstieg werden `index.html` im Repository-Stamm sowie die üblichen Unterordner `public`, `static` und `web` unterstützt.
- Nach erfolgreicher Installation wird automatisch die Gruppe **Module** angelegt, sofern sie noch nicht existiert.
- Der Hub registriert das Modul und erzeugt ein favorisiertes Start-Lesezeichen in dieser Gruppe.
- Installierte Module liegen standardmäßig unter `modules`. Mit der Umgebungsvariable `MODULES_PATH` kann ein anderer Speicherort festgelegt werden.

## Sichere Modularchive

- Repositorys werden vor der Installation gegen den aktuell verfügbaren Organisationskatalog geprüft.
- ZIP-Einträge dürfen den vorgesehenen Installationsordner nicht verlassen.
- Symbolische Links in Archiven werden abgelehnt.
- Die entpackte Gesamtgröße ist auf 500 MB begrenzt.
- Eine unvollständige Installation wird automatisch entfernt und nicht in der Datenbank registriert.
- Bereits vorhandene Zielordner werden nicht überschrieben.

## Verwaltung installierter Module

- Heruntergeladene Module werden in der Datenbank als verwaltete Installation gekennzeichnet.
- **Vollständig löschen** entfernt bei verwalteten Modulen neben Moduldefinition und Start-Lesezeichen auch die heruntergeladenen Dateien.
- Manuell eingebundene lokale Ordner bleiben beim Entfernen ihrer Moduldefinition weiterhin unangetastet.
- Nach dem Löschen kann ein Katalogmodul erneut installiert werden.

## API-Erweiterungen

- `GET /api/module-catalog` liefert den verfügbaren GitHub-Modulkatalog einschließlich lokalem Installationsstatus.
- `POST /api/module-catalog/{repository}/install` lädt das ausgewählte Modul herunter und richtet es ein.
- Die bestehende Modul-API bleibt unverändert verfügbar.

## Qualitätssicherung

- Ein neuer Integrationstest bildet Katalogabruf, ZIP-Download, Erkennung von `public/index.html`, Datenbankregistrierung und Start-Lesezeichen vollständig ab.
- Der Test prüft außerdem die Kennzeichnung als installiert und das Entfernen verwalteter Moduldateien.
- Die Modulansicht wurde mit dem echten GitHub-Katalog sowie in Desktop- und Mobilansicht geprüft.
- Go-Tests und Go-Build laufen erfolgreich durch.

## Upgrade-Hinweis

Bestehende Datenbanken werden beim ersten Start automatisch um die Kennzeichnung für verwaltete Module erweitert. Bereits registrierte lokale Module gelten weiterhin als manuell verwaltet und ihre Dateien werden beim Löschen nicht entfernt.

Für den GitHub-Katalog und die Installation neuer Module benötigt der Hub Zugriff auf `api.github.com`. Bereits installierte und manuell eingebundene Module können weiterhin ohne Netzwerkzugriff verwendet werden.
