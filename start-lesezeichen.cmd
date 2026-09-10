@echo off
setlocal

set "PORT=%~1"
if "%PORT%"=="" (
  powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0start-lesezeichen.ps1"
) else (
  powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0start-lesezeichen.ps1" -Port %PORT%
)
if errorlevel 1 (
  echo.
  echo Start fehlgeschlagen. Details stehen oben.
  pause
  exit /b 1
)

exit /b 0
