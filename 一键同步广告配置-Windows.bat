@echo off
chcp 65001 >nul
setlocal
cd /d "%~dp0"
where go >nul 2>nul
if errorlevel 1 (
  echo 未找到 Go，请先安装与一键发布服务端相同的 Go 环境。
  pause
  exit /b 1
)
go run ./cmd/sync-ad-policy %*
set "RESULT=%ERRORLEVEL%"
echo.
pause
exit /b %RESULT%
