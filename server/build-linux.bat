@echo off
rem build-linux.bat - cross-compile backend for Linux amd64 (pure-Go sqlite, no CGO/zig)
rem Put this file in server\ dir and run build-linux.bat
rem Output: kpi-server-linux-amd64 in current dir
rem setlocal isolates env vars; restored on exit, safe for later go build
setlocal
set "GOOS=linux"
set "GOARCH=amd64"
set "CGO_ENABLED=0"
go build
if errorlevel 1 exit /b %errorlevel%
echo [OK] built: %cd%\kpi-server-linux-amd64
