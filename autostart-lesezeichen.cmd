@echo off
setlocal

rem Startet den Hub beim Anmelden, ohne den Browser zu oeffnen.
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0start-lesezeichen.ps1" -NoBrowser

exit /b %errorlevel%
