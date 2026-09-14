#!/bin/sh

set -eu

auto_confirm=false
build_only=false
for argument in "$@"; do
  case "$argument" in
    --yes) auto_confirm=true ;;
    --build-only) build_only=true ;;
    *) echo "未知参数：$argument" >&2; exit 1 ;;
  esac
done

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
project_dir=$(CDPATH= cd -- "$script_dir/.." && pwd)
config_file="$project_dir/.release.env"
version=$(date -u +%Y%m%d%H%M%S)
if [ "$build_only" != true ]; then
  node "$project_dir/scripts/bt-project.mjs" check --release
fi
echo "先执行测试与 Linux 构建..."
(cd "$project_dir" && env MYSQL_TEST_DSN= go test ./... && go vet ./...)
archive=$("$project_dir/scripts/build-linux-amd64.sh" "$version" | sed -n '1p')
if [ ! -f "$archive" ]; then echo "发布包不存在：$archive" >&2; exit 1; fi

if [ "$build_only" = true ]; then
  echo "服务端发布包已生成：$archive"
  exit 0
fi

if [ ! -f "$config_file" ]; then echo "缺少 .release.env，请从 .release.env.example 复制。" >&2; exit 1; fi
set -a
. "$config_file"
set +a

for name in RELEASE_SSH_HOST RELEASE_SSH_USER RELEASE_SSH_KEY RELEASE_REMOTE_ROOT RELEASE_SYSTEMD_SERVICE RELEASE_HEALTH_BASE_URL; do
  eval "value=\${$name:-}"
  if [ -z "$value" ]; then echo "$name 未配置" >&2; exit 1; fi
done
if [ ! -f "$RELEASE_SSH_KEY" ]; then echo "SSH 私钥不存在：$RELEASE_SSH_KEY" >&2; exit 1; fi
case "$RELEASE_SSH_HOST" in ''|*[!A-Za-z0-9.-]*) echo 'RELEASE_SSH_HOST 格式不合法' >&2; exit 1;; esac
case "$RELEASE_SSH_USER" in ''|*[!A-Za-z0-9._-]*) echo 'RELEASE_SSH_USER 格式不合法' >&2; exit 1;; esac
case "$RELEASE_REMOTE_ROOT" in /|''|*[!A-Za-z0-9._/-]*) echo 'RELEASE_REMOTE_ROOT 必须是明确的绝对目录且不能为 /' >&2; exit 1;; esac
case "$RELEASE_SYSTEMD_SERVICE" in *[!A-Za-z0-9._@-]*|''|.*) echo 'RELEASE_SYSTEMD_SERVICE 格式不合法' >&2; exit 1;; esac
case "$RELEASE_SYSTEMD_SERVICE" in *.service) :;; *) echo 'RELEASE_SYSTEMD_SERVICE 必须以 .service 结尾' >&2; exit 1;; esac
case "$RELEASE_HEALTH_BASE_URL" in http://127.0.0.1:*) health_port=${RELEASE_HEALTH_BASE_URL#http://127.0.0.1:};; *) health_port=;; esac
case "$health_port" in ''|*[!0-9]*) echo 'RELEASE_HEALTH_BASE_URL 必须指向服务器本机 127.0.0.1 端口' >&2; exit 1;; esac

if [ "$auto_confirm" != true ]; then
  printf '即将更新正式服务与正式数据库 migration。输入 RELEASE 继续：'
  read -r confirmation
  if [ "$confirmation" != "RELEASE" ]; then echo "已取消"; exit 1; fi
fi

remote_archive="$RELEASE_REMOTE_ROOT/incoming/linkgame-bt-$version.tar.gz"
ssh -i "$RELEASE_SSH_KEY" -o IdentitiesOnly=yes -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new \
  "$RELEASE_SSH_USER@$RELEASE_SSH_HOST" \
  "mkdir -p '$RELEASE_REMOTE_ROOT/incoming' '$RELEASE_REMOTE_ROOT/releases' '$RELEASE_REMOTE_ROOT/shared'"
scp -i "$RELEASE_SSH_KEY" -o IdentitiesOnly=yes -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new \
  "$archive" "$RELEASE_SSH_USER@$RELEASE_SSH_HOST:$remote_archive"

ssh -i "$RELEASE_SSH_KEY" -o IdentitiesOnly=yes -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new \
  "$RELEASE_SSH_USER@$RELEASE_SSH_HOST" "set -eu
release_dir='$RELEASE_REMOTE_ROOT/releases/$version'
previous_release=\$(readlink -f '$RELEASE_REMOTE_ROOT/current' 2>/dev/null || true)
mkdir -p \"\$release_dir\"
tar -xzf '$remote_archive' -C \"\$release_dir\" --strip-components=1
[ -x \"\$release_dir/bin/linkgame-bt-api\" ]
[ -x \"\$release_dir/bin/linkgame-bt-migrate\" ]
if [ ! -e '$RELEASE_REMOTE_ROOT/shared/ad-policy.json' ]; then cp \"\$release_dir/deploy/config/ad-policy.json\" '$RELEASE_REMOTE_ROOT/shared/ad-policy.json'; fi
set -a
. '$RELEASE_REMOTE_ROOT/shared/app.env'
set +a
sh \"\$release_dir/deploy/check-target.sh\"
\"\$release_dir/bin/linkgame-bt-check-config\"
\"\$release_dir/bin/linkgame-bt-migrate\" -path \"\$release_dir/migrations\"
ln -sfn \"\$release_dir\" '$RELEASE_REMOTE_ROOT/current'
mkdir -p \"\$HOME/.config/systemd/user\"
cp \"\$release_dir/deploy/systemd/$RELEASE_SYSTEMD_SERVICE\" \
  \"\$HOME/.config/systemd/user/$RELEASE_SYSTEMD_SERVICE\"
systemctl --user daemon-reload
systemctl --user enable '$RELEASE_SYSTEMD_SERVICE'
restart_ok=true
if ! systemctl --user restart '$RELEASE_SYSTEMD_SERVICE'; then restart_ok=false; fi
attempt=1
while [ \"\$attempt\" -le 20 ]; do
  if [ \"\$restart_ok\" = true ] && \
     curl --fail --silent --max-time 3 '$RELEASE_HEALTH_BASE_URL/health/live' >/dev/null && \
     curl --fail --silent --max-time 3 '$RELEASE_HEALTH_BASE_URL/health/ready' >/dev/null; then
    break
  fi
  if [ \"\$attempt\" -eq 20 ]; then
    systemctl --user status '$RELEASE_SYSTEMD_SERVICE' --no-pager >&2 || true
    journalctl --user -u '$RELEASE_SYSTEMD_SERVICE' -n 80 --no-pager >&2 || true
    if [ -n \"\$previous_release\" ] && [ -d \"\$previous_release\" ]; then
      echo \"新版本健康检查失败，回退到：\$previous_release\" >&2
      ln -sfn \"\$previous_release\" '$RELEASE_REMOTE_ROOT/current'
      systemctl --user restart '$RELEASE_SYSTEMD_SERVICE' || true
    fi
    exit 1
  fi
  attempt=\$((attempt + 1))
  sleep 1
done
curl --fail --silent --show-error '$RELEASE_HEALTH_BASE_URL/health/live'
curl --fail --silent --show-error '$RELEASE_HEALTH_BASE_URL/health/ready'
rm -f '$remote_archive'
"
echo "服务端发布完成：新 BT API（见 config/project.json）（版本 ${version}）"
