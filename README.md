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

## Schnellstart

Voraussetzung: Go 1.22 oder neuer

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

## EXE bauen

### Lokal

```bash
go build -trimpath -ldflags "-s -w" -o Lesezeichen-Hub.exe .
```

Danach die EXE direkt starten.

Hinweise:

- Web-Dateien sind per embed in der EXE enthalten
- Port kann bei Bedarf per Umgebungsvariable gesetzt werden, z.B. ADDR=:2233

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
