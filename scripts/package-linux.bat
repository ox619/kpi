@echo off
rem ============================================================
rem  KPI Linux package builder - entry point (double-click friendly)
rem  All logic lives in package-linux.ps1; this bat only forwards
rem  arguments and keeps the window open so results are visible.
rem
rem  Usage (double-click or cmd):
rem    scripts\package-linux.bat
rem    scripts\package-linux.bat -Arch arm64
rem    scripts\package-linux.bat -ApiPort 19080
rem    scripts\package-linux.bat -SkipInstall
rem ============================================================
setlocal
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0package-linux.ps1" %*
set "EXITCODE=%ERRORLEVEL%"
echo.
echo Exit code: %EXITCODE%
pause
exit /b %EXITCODE%
