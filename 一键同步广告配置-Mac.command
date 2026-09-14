#!/bin/sh
project_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$project_dir" || exit 1
# Finder 双击启动时 PATH 较短，补上 Go 和 Homebrew 的常用安装位置。
PATH="$PATH:/usr/local/go/bin:/opt/homebrew/bin:/usr/local/bin"
export PATH
if command -v go >/dev/null 2>&1; then
  go run ./cmd/sync-ad-policy "$@"
  result=$?
else
  echo '未找到 Go，请先安装与一键发布服务端相同的 Go 环境。'
  result=1
fi
printf '\n按回车关闭窗口...'
read -r _
exit "$result"
