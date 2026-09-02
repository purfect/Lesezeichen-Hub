# Lesezeichen Hub 2.5

Version 2.5 baut den lokalen Lesezeichen-Hub zu einer übersichtlicheren persönlichen Arbeitszentrale aus. Im Mittelpunkt stehen schnellere Filter, gespeicherte Ansichten, eine einheitliche Navigation, eine sichere Wiederherstellung und die vollständige Verwaltung lokaler Module.

## Highlights

### Schnellfilter und gespeicherte Ansichten

- Lesezeichen lassen sich direkt nach Gruppe, Tag, Favoriten, angepinnten Einträgen und fälligen Wiedervorlagen filtern.
- Suche und Filter können als benannte Ansicht gespeichert und später wieder aufgerufen werden.
- Gespeicherte Ansichten bleiben lokal im Browser erhalten.
- Nicht mehr benötigte Ansichten können direkt gelöscht werden.
- Filter, Ansichten, Import, Export und weitere Datenaktionen sind platzsparend unter **Verwalten** zusammengefasst.

### Einheitliche Navigation

- Startseite, Übersicht, Archiv, Notizen, Statistiken und Module verwenden eine gemeinsame sichtbare Hauptnavigation.
- Der aktuelle Bereich wird eindeutig hervorgehoben.
- Auf kleinen Bildschirmen erscheinen alle sechs Bereiche in einem kompakten Raster ohne horizontalen Seitenüberlauf.
- Sichtbare Texte verwenden jetzt durchgehend korrekte deutsche Umlaute.

### Verbesserte Leerzustände

- Leere Seiten erklären nicht mehr nur, dass Daten fehlen, sondern bieten direkt die passende nächste Aktion an.
- Beim ersten Start kann unmittelbar die erste Gruppe angelegt werden.
- Archiv, Übersicht, Statistiken, Notizen und Modulverwaltung verlinken aus leeren Zuständen direkt zum jeweiligen Erstellungs- oder Verwaltungsablauf.
- Der Notizeditor kann über einen direkten Link sofort für eine neue Notiz geöffnet werden.

## Sichere Wiederherstellung

- Vollsicherungen werden vor dem Einspielen serverseitig geprüft.
- Eine Vorschau zeigt neue Gruppen, Lesezeichen und Notizen sowie vorhandene Konflikte.
- Ungültige oder unsichere URLs werden bereits vor Beginn der Wiederherstellung abgelehnt.
- Bei Konflikten kann zwischen **Überschreiben** und **Überspringen** gewählt werden.
- Vor dem Einspielen kann automatisch eine neue Vollsicherung heruntergeladen werden.
- Die Vorschau verändert keine vorhandenen Daten.

## Lokale Module

### Neue Modulverwaltung

- Der neue Bereich **Module** listet alle registrierten lokalen Webanwendungen auf.
- Für jedes Modul wird angezeigt, ob der hinterlegte Ordner aktuell verfügbar ist.
- Module können direkt geöffnet, umbenannt und mit einem anderen Ordner verbunden werden.
- Der Windows-Ordnerdialog kann auch beim erneuten Verbinden verwendet werden.
- Eine Moduldefinition kann vollständig zusammen mit ihrem Start-Lesezeichen gelöscht werden.
- Beim Umbenennen wird der Titel des zugehörigen Start-Lesezeichens automatisch aktualisiert.

### Sicherere Dateiauslieferung

- Modulordner werden vor dem Speichern auf einen kanonischen Pfad aufgelöst.
- Auch jede angeforderte Moduldatei wird nach Auflösung von Symlinks und Windows-Junctions erneut geprüft.
- Dateien außerhalb des registrierten Modulordners werden nicht ausgeliefert.
- Direkte Aufrufe von `index.html` werden ohne unnötige Weiterleitung beantwortet.
- Änderungen an lokalen Moduldateien sind weiterhin beim nächsten Aufruf sichtbar und werden nicht dauerhaft zwischengespeichert.

## Browser-Integration

- Das Tampermonkey-Skript verwendet zum Speichern einer Webseite jetzt ein kompaktes Formular statt mehrerer aufeinanderfolgender Browser-Prompts.
- Gruppe, Titel, Notiz und Tags können in einem Schritt bearbeitet werden.
- Die zuletzt verwendete Gruppe ist beim nächsten Speichern vorausgewählt.
- Bereits im Hub verwendete Tags werden als Vorschläge angeboten.
- Favorit und angepinnt können direkt beim Speichern gesetzt werden.
- Die Hub-Adresse bleibt separat über das Tampermonkey-Menü konfigurierbar.

## Datenintegrität und Stabilität

- Lesezeichen-URLs werden normalisiert, damit Varianten derselben Adresse zuverlässiger als Dublette erkannt werden.
- Beim Löschen einer Gruppe werden zugehörige Lesezeichen durch aktivierte SQLite-Fremdschlüssel zuverlässig entfernt.
- Beim Start werden ältere verwaiste Lesezeichen ohne vorhandene Gruppe bereinigt.
- SQLite-Fremdschlüssel und Busy-Timeout gelten auch für neu geöffnete Datenbankverbindungen.
- Das Löschen eines Moduls bereinigt Verweise auf sein Start-Lesezeichen aus verknüpften Notizen.
- Die Modul-, Restore-, URL- und Datenbankabläufe werden durch zusätzliche automatisierte Tests abgesichert.

## Weitere Verbesserungen

- Vault-Notizen werden in der Statistik als eigener Notiztyp aufgeführt.
- Das Vault-Passwortfeld erscheint nur noch, wenn tatsächlich der Typ **Vault** ausgewählt ist.
- Meldungen zu fälligen und überfälligen Wiedervorlagen wurden sprachlich vereinheitlicht.
- Die Dokumentation enthält die neuen Modul- und Restore-Endpunkte sowie die aktualisierten Bedienabläufe.

## API-Erweiterungen

- `GET /api/modules` listet Module einschließlich Verfügbarkeitsstatus auf.
- `POST /api/modules` registriert ein neues lokales Modul.
- `PUT /api/modules/{id}` aktualisiert Name und Ordner eines Moduls.
- `DELETE /api/modules/{id}` löscht Moduldefinition und Start-Lesezeichen.
- `POST /api/restore?preview=1` prüft eine Vollsicherung ohne Schreibzugriff.
- `POST /api/restore?conflicts=overwrite` überschreibt vorhandene Konflikte.
- `POST /api/restore?conflicts=skip` lässt vorhandene Konflikte unverändert.

## Upgrade-Hinweis

Vor dem Update empfiehlt sich weiterhin eine Vollsicherung über **Verwalten > Vollsicherung**. Bestehende Datenbanken werden beim Start automatisch migriert und auf verwaiste Lesezeichen geprüft.

Gespeicherte Ansichten, Theme-Auswahl, eingeklappte Gruppen und die Sichtbarkeit der Edelmetallpreise liegen im lokalen Browser-Speicher. Bei einem Browserprofilwechsel müssen diese Einstellungen bei Bedarf neu angelegt werden.

## Voraussetzungen

- Windows für den nativen Ordnerdialog und die bereitgestellten Start- und Stopskripte.
- Go 1.23 oder neuer beim Bauen aus dem Quellcode.
- Zugriff des Hub-Prozesses auf die Ordner eingebundener lokaler Module.
