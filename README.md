# Lesezeichen Hub

Moderner Bookmark-Hub als lokale Web-App mit Go und SQLite.

Die Anwendung laeuft komplett lokal, bietet Gruppen, Tags, Favoriten, Wiedervorlagen, Import/Export und eine kompakte Uebersichtsseite.

## Highlights

- Gruppen fuer saubere Struktur
- Lesezeichen mit Titel, URL, Notiz, Tags und Datum
- Favoriten und angepinnte Eintraege
- Suche ueber Titel, URL, Notizen, Tags und Gruppen
- Drag and Drop Sortierung fuer Gruppen und Lesezeichen
- Import und Export als JSON, CSV und HTML
- Lokale SQLite-Datenbank
- Lokale Webanwendungen als Lesezeichen in bestehende Gruppen integrieren

## Schnellstart

Voraussetzung: Go 1.23 oder neuer

1. Abhaengigkeiten aufloesen

```bash
go mod tidy
```

2. Entwicklung starten

```bash
go run .
```

3. Im Browser oeffnen

http://localhost:2222

## Lokale Module integrieren

Über **Hinzufuegen > Lokales Modul** kann ein Ordner mit einer `index.html` eingebunden werden, zum Beispiel der Projektordner von `werkplan`. Nach der Auswahl einer bestehenden Gruppe kann der Ordner über den Windows-Dialog **Ordner waehlen** ausgesucht werden. Der Hub speichert den lokalen Ordner und erzeugt in der ausgewählten Gruppe ein Start-Lesezeichen.

Für jedes Modul können zusätzlich Notizen und Tags vergeben werden. Da ein Modul als normales Lesezeichen gespeichert wird, erscheint es automatisch in Suche, Favoriten, Tags, Exporten und Notizen. Wird ein Modul archiviert oder sein Lesezeichen gelöscht, kann es mit demselben Namen erneut angelegt werden; verwaiste oder archivierte Moduldefinitionen werden dabei wiederverwendet beziehungsweise reaktiviert.

Die Dateien werden über den Hub unter `/modules/...` ausgeliefert, sodass relative CSS-, JavaScript- und Bildpfade der lokalen Anwendung funktionieren.

Der Server muss lokal unter Windows laufen und Zugriff auf den angegebenen Ordner haben. Änderungen an den Moduldateien werden beim nächsten Aufruf direkt verwendet.

## Windows Start und Stop per Klick

Nach dem Build kannst du den Hub ohne Terminal starten:

- start-lesezeichen.cmd startet den Server im Hintergrund und oeffnet den Browser
- stop-lesezeichen.cmd beendet den laufenden Server
- Die Logik liegt in start-lesezeichen.ps1 und stop-lesezeichen.ps1
- Laufzeitdateien landen in .runtime, damit der Projekt-Root sauber bleibt

Port anpassen:

- Standard-Port in start-lesezeichen.cmd: set "PORT=3233"
- Optional beim Start als Parameter: start-lesezeichen.cmd 2233
- Beim Portwechsel wird ein alter Prozess automatisch beendet und neu gestartet

## Webseite direkt aus Firefox speichern (Tampermonkey)

Wenn du Seiten aus dem Browser direkt in den Hub schreiben willst, kannst du das mit Tampermonkey nutzen.

Datei im Projekt:

- tampermonkey-lesezeichen-hub.user.js

Einrichtung:

1. In Firefox die Erweiterung Tampermonkey installieren.
2. In Tampermonkey ein neues Script anlegen.
3. Den Inhalt aus tampermonkey-lesezeichen-hub.user.js einfuegen und speichern.
4. Den Hub starten (Standard im Startskript: http://localhost:3233).

Nutzung:

- Tampermonkey-Menue: Im Lesezeichen-Hub speichern
- Tastenkombination: Alt+Shift+B
- Optional Hub-URL im Tampermonkey-Menue setzen, falls dein Port abweicht

Hinweise:

- Das Script fragt erst Gruppe, Titel, Notiz und Tags ab und sendet dann an /api/bookmarks.
- Wenn noch keine Gruppen existieren, zuerst im Hub eine Gruppe anlegen.

## EXE bauen

### Lokal

```bash
go build -trimpath -ldflags "-s -w" -o Lesezeichen-Hub.exe .
```

Danach die EXE direkt starten.

Hinweise:

- Web-Dateien sind per embed in der EXE enthalten
- Der Server bindet standardmaessig ausschliesslich an `127.0.0.1`.
- Port kann bei Bedarf per Umgebungsvariable gesetzt werden, z.B. `ADDR=127.0.0.1:2233`.

## Betrieb und Datenschutz

- SQLite verwendet WAL-Modus, einen Busy-Timeout und eine einzelne Verbindung, damit parallele Zugriffe zuverlaessig verarbeitet werden.
- Die Edelmetallpreise rufen externe Anbieter ab. Sie lassen sich beim Start abschalten:

```powershell
$env:ENABLE_EXTERNAL_PRICES = 'false'
.\Lesezeichen-Hub.exe
```

- Bei deaktivierter Option wird die Preisleiste ausgeblendet; alle Lesezeichen- und Notizdaten bleiben rein lokal.
- Start- und Stopskripte pruefen den EXE-Pfad des gespeicherten Prozesses, bevor sie einen laufenden Prozess beenden.

## Tastatur

- `/`: Suche fokussieren
- `N`: Dialog fuer ein neues Lesezeichen oeffnen
- `G`: Dialog fuer eine neue Gruppe oeffnen
- `Esc`: offenen Dialog schliessen

### GitHub Release Build

Ein Workflow unter .github/workflows/main.yml baut automatisch eine Windows-EXE bei einem veroeffentlichten Release.

Regel:

- Der Tag muss mit einer Zahl beginnen, z.B. 1.0.0 oder 1.2026.06

Ablauf:

1. Tag erstellen und pushen

```bash
git tag 1.0.0
git push origin 1.0.0
```

2. Release auf GitHub veroeffentlichen
3. Workflow erstellt und haengt das Asset an

Asset-Name:

- Lesezeichen-Hub_<Versionstag>.exe

## Daten und Dateien

- Datenbank: data.db
- Laufzeitdateien: .runtime
- Web-Frontend: web

## API Uebersicht

- GET /api/state
- GET /api/export
- POST /api/import
- GET /api/groups
- POST /api/groups
- PUT /api/groups/{id}
- DELETE /api/groups/{id}
- GET /api/groups/{id}/bookmarks
- POST /api/bookmarks
- PUT /api/bookmarks/{id}
- DELETE /api/bookmarks/{id}

Import-Hinweise:

- Speed Dial JSON mit dials wird in die Gruppe ungruppiert importiert
- Vorhandene Gruppen bleiben erhalten
- Der normale JSON-Import mit groups ist merge-basiert
- Bereits vorhandene Lesezeichen gleicher Gruppe plus URL werden nicht doppelt angelegt

## Troubleshooting

- Kein Zahnrad sichtbar: Seite mit Strg+F5 hart neu laden
- Falscher Port: Startskript auf 3233 pruefen oder Port als Parameter uebergeben
- Build nicht gefunden: Sicherstellen, dass Lesezeichen-Hub.exe im Projektordner liegt
