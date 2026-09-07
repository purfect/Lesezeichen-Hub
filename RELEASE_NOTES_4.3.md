# Lesezeichen Hub 4.3

Version 4.3 ergaenzt den Lesezeichen-Hub um einen abgesicherten serverseitigen Pruefendpunkt fuer das NetzWache-Modul.

## NetzWache-Unterstuetzung

- Der neue Endpunkt `GET /api/netzwache/check?url=...` prueft die Erreichbarkeit einer oeffentlichen HTTP(S)-Adresse direkt vom Hub aus.
- NetzWache kann damit HTTP-Status und Antwortzeit auch dann erfassen, wenn die Zielseite keine CORS-Freigabe fuer Browser erteilt.
- NetzWache erhaelt eine eindeutige Antwort mit HTTP-Status, Antwortzeit oder Netzwerkfehler.
- Weiterleitungen werden nicht automatisch verfolgt und erscheinen als HTTP-Status im Monitor.

## Sicherheit

- Der Pruefendpunkt akzeptiert nur `http://`- und `https://`-Adressen.
- Private, lokale, Link-Local-, Multicast- und nicht spezifizierte IP-Adressen sind ausgeschlossen.
- Die DNS-Aufloesung wird vor dem Verbindungsaufbau erneut geprueft, damit Zielnamen nicht auf lokale Netzadressen umgeleitet werden koennen.

## Qualitaetssicherung

- Tests pruefen erlaubte oeffentliche HTTP(S)-Adressen sowie die Ablehnung von lokalen, privaten und unzulaessigen Schemas.
- Die vollstaendige Go-Testsuite und `go vet ./...` wurden erfolgreich ausgefuehrt.

## Upgrade-Hinweis

Nach dem Update den Lesezeichen-Hub neu starten und NetzWache auf Version 1.0.1 aktualisieren. Lokale, private und Link-Local-Ziele koennen bewusst nicht ueber den NetzWache-Pruefendpunkt abgefragt werden.
