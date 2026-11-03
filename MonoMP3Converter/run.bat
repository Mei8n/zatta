@echo off
chcp 65001 >nul
cd /d "%~dp0"
set "PATH=%~dp0;%PATH%"
where go >nul 2>nul
if errorlevel 1 (
    echo Go が見つかりません。https://go.dev/dl/ から Go をインストールしてください。
    pause
    exit /b 1
)
go run .
if errorlevel 1 pause
