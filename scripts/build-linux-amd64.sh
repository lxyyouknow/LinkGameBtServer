#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
project_dir=$(CDPATH= cd -- "$script_dir/.." && pwd)
node "$project_dir/scripts/bt-project.mjs" check >&2
version=${1:-$(date -u +%Y%m%d%H%M%S)}
case "$version" in ""|*[!A-Za-z0-9._-]*) echo "版本号格式不合法" >&2; exit 1;; esac

dist_dir="$project_dir/dist"
archive_path="$dist_dir/linkgame-bt-server-linux-amd64-$version.tar.gz"
temporary_dir=$(mktemp -d)
package_dir="$temporary_dir/linkgame-bt-server"
cleanup(){
  case "$temporary_dir" in
    /tmp/*|/private/tmp/*|/var/folders/*)
      if [ -d "$temporary_dir" ] && [ ! -L "$temporary_dir" ]; then
        rm -rf -- "${temporary_dir:?}"
      fi
      ;;
    *) echo "拒绝清理意外的临时目录：$temporary_dir" >&2;;
  esac
}
trap cleanup EXIT HUP INT TERM

mkdir -p "$dist_dir" "$package_dir/bin" "$package_dir/deploy/systemd" "$package_dir/deploy/nginx"
(
  cd "$project_dir"
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "$package_dir/bin/linkgame-bt-api" ./cmd/api
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "$package_dir/bin/linkgame-bt-migrate" ./cmd/migrate
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "$package_dir/bin/linkgame-bt-check-config" ./cmd/check-config
)
cp "$project_dir/deploy/check-target.sh" "$package_dir/deploy/"
cp -R "$project_dir/deploy/config" "$package_dir/deploy/config"
cp -R "$project_dir/migrations" "$package_dir/migrations"
cp "$project_dir/deploy/systemd/linkgame-bt.service" "$package_dir/deploy/systemd/"
cp "$project_dir/deploy/systemd/app.env.example" "$package_dir/deploy/systemd/"
cp "$project_dir/deploy/nginx/linkgame-bt.conf" "$package_dir/deploy/nginx/"
cp "$project_dir/docs/服务器部署.md" "$package_dir/部署说明.md"
printf '%s\n' "$version" > "$package_dir/VERSION"
chmod 0755 "$package_dir/bin/linkgame-bt-api" "$package_dir/bin/linkgame-bt-migrate"
COPYFILE_DISABLE=1 tar -C "$temporary_dir" -czf "$archive_path" linkgame-bt-server
echo "$archive_path"
if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$archive_path"; else sha256sum "$archive_path"; fi
