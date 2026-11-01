@echo off
rem ============================================================
rem  fam - one click launcher (starts Go backend + Electron UI)
rem  Double click this file, or run:  start.cmd --smoke
rem ============================================================
chcp 65001 >nul 2>nul
setlocal
cd /d "%~dp0"

where node >nul 2>nul
if errorlevel 1 (
  echo.
  echo [ERROR] Node.js not found.
  echo         Please install Node.js 18+ from https://nodejs.org/ and retry.
  echo.
  pause
  exit /b 1
)

node "scripts\start.js" %*
set "CODE=%errorlevel%"
if not "%CODE%"=="0" (
  echo.
  echo [ERROR] fam exited with code %CODE%
  echo         Check the messages above for details.
  echo.
  pause
)
endlocal & exit /b %CODE%
