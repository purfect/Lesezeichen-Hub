@echo off
setlocal

powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0stop-lesezeichen.ps1"
if errorlevel 1 (
  echo.
  echo Stop hat einen Fehler gemeldet.
  pause
  exit /b 1
)

exit /b 0
