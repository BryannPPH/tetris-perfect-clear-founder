@echo off
cd /d %~dp0
if exist bin\tetrio-go-smart-advisor-windows-amd64.exe (
  bin\tetrio-go-smart-advisor-windows-amd64.exe
) else (
  go run .
)
pause
