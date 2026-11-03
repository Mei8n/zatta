@echo off
chcp 65001 >nul
cd /d "%~dp0"
where go >nul 2>nul
if errorlevel 1 (
    echo Go が見つかりません。https://go.dev/dl/ から Go をインストールしてください。
    pause
    exit /b 1
)
set GOOS=windows
set GOARCH=amd64
set CGO_ENABLED=0
go build -trimpath -ldflags="-s -w -H=windowsgui" -o MonoMP3Converter.exe .
if errorlevel 1 (
    echo ビルドに失敗しました。
    pause
    exit /b 1
)
echo MonoMP3Converter.exe を作成しました。
pause
