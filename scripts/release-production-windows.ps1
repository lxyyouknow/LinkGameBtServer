$ErrorActionPreference = 'Stop'

$ProjectDir = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$ConfigFile = Join-Path $ProjectDir '.release.env'
$Version = (Get-Date).ToUniversalTime().ToString('yyyyMMddHHmmss')
$BuildOnly = $args -contains '--build-only'
$AutoConfirm = $args -contains '--yes'
if ($BuildOnly) { & node (Join-Path $PSScriptRoot 'bt-project.mjs') check } else { & node (Join-Path $PSScriptRoot 'bt-project.mjs') check --release }
if ($LASTEXITCODE -ne 0) { throw 'BT 配置预检失败，尚未执行 SSH 或部署。' }
$UnknownArgs = @($args | Where-Object { $_ -notin @('--build-only', '--yes') })
if ($UnknownArgs.Count -gt 0) { throw "未知参数：$($UnknownArgs -join ', ')" }

foreach ($Command in @('go', 'tar.exe')) {
    if (-not (Get-Command $Command -ErrorAction SilentlyContinue)) { throw "没有找到 $Command。" }
}

Write-Host '[1/4] 服务端测试与静态检查...'
Push-Location $ProjectDir
try {
    $SavedTestDsn = $env:MYSQL_TEST_DSN
    try { $env:MYSQL_TEST_DSN = ''; & go test ./... } finally { $env:MYSQL_TEST_DSN = $SavedTestDsn }
    if ($LASTEXITCODE -ne 0) { throw 'go test 失败。' }
    & go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'go vet 失败。' }
} finally { Pop-Location }

Write-Host '[2/4] 构建 Linux amd64 发布包...'
$DistDir = Join-Path $ProjectDir 'dist'
$Archive = Join-Path $DistDir "linkgame-bt-server-linux-amd64-$Version.tar.gz"
$TempRoot = Join-Path ([System.IO.Path]::GetTempPath()) "linkgame-bt-server-$Version-$PID"
$PackageDir = Join-Path $TempRoot 'linkgame-bt-server'
New-Item -ItemType Directory -Path (Join-Path $PackageDir 'bin'), (Join-Path $PackageDir 'deploy\systemd'), (Join-Path $PackageDir 'deploy\nginx'), $DistDir -Force | Out-Null

$OldGoos = $env:GOOS
$OldGoarch = $env:GOARCH
$OldCgo = $env:CGO_ENABLED
try {
    $env:GOOS = 'linux'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
    Push-Location $ProjectDir
    try {
        & go build -trimpath -ldflags '-s -w' -o (Join-Path $PackageDir 'bin\linkgame-bt-api') ./cmd/api
        if ($LASTEXITCODE -ne 0) { throw 'API 构建失败。' }
        & go build -trimpath -ldflags '-s -w' -o (Join-Path $PackageDir 'bin\linkgame-bt-check-config') ./cmd/check-config
        if ($LASTEXITCODE -ne 0) { throw '配置预检工具构建失败。' }
        & go build -trimpath -ldflags '-s -w' -o (Join-Path $PackageDir 'bin\linkgame-bt-migrate') ./cmd/migrate
        if ($LASTEXITCODE -ne 0) { throw 'migration 工具构建失败。' }
    } finally { Pop-Location }
    Copy-Item -LiteralPath (Join-Path $ProjectDir 'deploy\check-target.sh') -Destination (Join-Path $PackageDir 'deploy')
    Copy-Item -LiteralPath (Join-Path $ProjectDir 'migrations') -Destination $PackageDir -Recurse
    Copy-Item -LiteralPath (Join-Path $ProjectDir 'deploy\systemd\linkgame-bt.service') -Destination (Join-Path $PackageDir 'deploy\systemd')
    Copy-Item -LiteralPath (Join-Path $ProjectDir 'deploy\systemd\app.env.example') -Destination (Join-Path $PackageDir 'deploy\systemd')
    Copy-Item -LiteralPath (Join-Path $ProjectDir 'deploy\nginx\linkgame-bt.conf') -Destination (Join-Path $PackageDir 'deploy\nginx')
    Copy-Item -LiteralPath (Join-Path $ProjectDir 'docs\服务器部署.md') -Destination (Join-Path $PackageDir '部署说明.md')
    [System.IO.File]::WriteAllText((Join-Path $PackageDir 'VERSION'), "$Version`n", [System.Text.UTF8Encoding]::new($false))
    & tar.exe -czf $Archive -C $TempRoot 'linkgame-bt-server'
    if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $Archive -PathType Leaf)) { throw '发布包压缩失败。' }
} finally {
    $env:GOOS = $OldGoos; $env:GOARCH = $OldGoarch; $env:CGO_ENABLED = $OldCgo
    if ((Test-Path -LiteralPath $TempRoot) -and ($TempRoot -like "$([System.IO.Path]::GetTempPath())linkgame-bt-server-*") -and -not ((Get-Item -LiteralPath $TempRoot).Attributes -band [IO.FileAttributes]::ReparsePoint)) {
        Remove-Item -LiteralPath $TempRoot -Recurse -Force
    }
}
$Hash = (Get-FileHash -LiteralPath $Archive -Algorithm SHA256).Hash.ToLower()
[System.IO.File]::WriteAllText("$Archive.sha256", "$Hash  $([IO.Path]::GetFileName($Archive))`n", [Text.Encoding]::ASCII)

if ($BuildOnly) { Write-Host "服务端发布包已生成：$Archive"; exit 0 }
foreach ($Command in @('ssh.exe', 'scp.exe')) {
    if (-not (Get-Command $Command -ErrorAction SilentlyContinue)) { throw "没有找到 $Command；请在 Windows 可选功能中安装 OpenSSH 客户端。" }
}
if (-not (Test-Path -LiteralPath $ConfigFile -PathType Leaf)) { throw '缺少 .release.env，请从 .release.env.example 复制。' }
foreach ($Line in Get-Content -LiteralPath $ConfigFile) {
    if ($Line -match '^\s*([A-Z0-9_]+)=(.*)$') { [Environment]::SetEnvironmentVariable($Matches[1], $Matches[2].Trim().Trim([char]39), 'Process') }
}
$Required = @('RELEASE_SSH_HOST', 'RELEASE_SSH_USER', 'RELEASE_SSH_KEY', 'RELEASE_REMOTE_ROOT', 'RELEASE_SYSTEMD_SERVICE', 'RELEASE_HEALTH_BASE_URL')
foreach ($Name in $Required) {
    if ([string]::IsNullOrWhiteSpace([Environment]::GetEnvironmentVariable($Name, 'Process'))) { throw "$Name 未配置。" }
}
$HostName = $env:RELEASE_SSH_HOST; $UserName = $env:RELEASE_SSH_USER; $KeyPath = $env:RELEASE_SSH_KEY
$RemoteRoot = $env:RELEASE_REMOTE_ROOT; $Service = $env:RELEASE_SYSTEMD_SERVICE; $HealthBase = $env:RELEASE_HEALTH_BASE_URL
if (-not (Test-Path -LiteralPath $KeyPath -PathType Leaf)) { throw "SSH 私钥不存在：$KeyPath" }
if ($HostName -notmatch '^[A-Za-z0-9.-]+$' -or $UserName -notmatch '^[A-Za-z0-9._-]+$') { throw 'SSH 主机或用户格式不合法。' }
if ($RemoteRoot -notmatch '^/[A-Za-z0-9._/-]+$' -or $RemoteRoot -eq '/') { throw 'RELEASE_REMOTE_ROOT 必须是明确的绝对目录且不能为 /。' }
if ($Service -notmatch '^[A-Za-z0-9._@-]+\.service$') { throw 'RELEASE_SYSTEMD_SERVICE 格式不合法。' }
if ($HealthBase -notmatch '^http://127\.0\.0\.1:\d+$') { throw 'RELEASE_HEALTH_BASE_URL 必须指向服务器本机 127.0.0.1 端口。' }
if (-not $AutoConfirm) {
    if ((Read-Host '即将更新正式服务与正式数据库 migration。输入 RELEASE 继续') -ne 'RELEASE') { throw '已取消。' }
}

Write-Host '[3/4] 上传发布包...'
$SshOptions = @('-i', $KeyPath, '-o', 'IdentitiesOnly=yes', '-o', 'BatchMode=yes', '-o', 'ConnectTimeout=10', '-o', 'StrictHostKeyChecking=accept-new')
$RemoteArchive = "$RemoteRoot/incoming/linkgame-bt-$Version.tar.gz"
$RemoteScript = "$RemoteRoot/incoming/deploy-$Version.sh"
$Target = "${UserName}@${HostName}"
& ssh.exe @SshOptions $Target "mkdir -p '$RemoteRoot/incoming' '$RemoteRoot/releases' '$RemoteRoot/shared'"
if ($LASTEXITCODE -ne 0) { throw 'SSH 连接或远端目录检查失败。' }
& scp.exe @SshOptions $Archive "${Target}:$RemoteArchive"
if ($LASTEXITCODE -ne 0) { throw '发布包上传失败。' }

$Template = @'
#!/bin/sh
set -eu
root='__ROOT__'
release_dir="$root/releases/__VERSION__"
previous_release=$(readlink -f "$root/current" 2>/dev/null || true)
mkdir -p "$release_dir"
tar -xzf '__ARCHIVE__' -C "$release_dir" --strip-components=1
[ -x "$release_dir/bin/linkgame-bt-api" ]
[ -x "$release_dir/bin/linkgame-bt-migrate" ]
set -a
. "$root/shared/app.env"
set +a
sh "$release_dir/deploy/check-target.sh"
"$release_dir/bin/linkgame-bt-check-config"
"$release_dir/bin/linkgame-bt-migrate" -path "$release_dir/migrations"
ln -sfn "$release_dir" "$root/current"
mkdir -p "$HOME/.config/systemd/user"
cp "$release_dir/deploy/systemd/__SERVICE__" "$HOME/.config/systemd/user/__SERVICE__"
systemctl --user daemon-reload
systemctl --user enable '__SERVICE__'
restart_ok=true
if ! systemctl --user restart '__SERVICE__'; then restart_ok=false; fi
attempt=1
while [ "$attempt" -le 20 ]; do
  if [ "$restart_ok" = true ] && curl --fail --silent --max-time 3 '__HEALTH__/health/live' >/dev/null && curl --fail --silent --max-time 3 '__HEALTH__/health/ready' >/dev/null; then break; fi
  if [ "$attempt" -eq 20 ]; then
    systemctl --user status '__SERVICE__' --no-pager >&2 || true
    journalctl --user -u '__SERVICE__' -n 80 --no-pager >&2 || true
    if [ -n "$previous_release" ] && [ -d "$previous_release" ]; then
      echo "新版本健康检查失败，回退到：$previous_release" >&2
      ln -sfn "$previous_release" "$root/current"
      systemctl --user restart '__SERVICE__' || true
    fi
    exit 1
  fi
  attempt=$((attempt + 1)); sleep 1
done
curl --fail --silent --show-error '__HEALTH__/health/live'
curl --fail --silent --show-error '__HEALTH__/health/ready'
rm -f '__ARCHIVE__' '__SCRIPT__'
'@
$RemoteBody = $Template.Replace('__ROOT__', $RemoteRoot).Replace('__VERSION__', $Version).Replace('__ARCHIVE__', $RemoteArchive).Replace('__SCRIPT__', $RemoteScript).Replace('__SERVICE__', $Service).Replace('__HEALTH__', $HealthBase)
$LocalRemoteScript = Join-Path $DistDir "deploy-$Version.sh"
[System.IO.File]::WriteAllText($LocalRemoteScript, ($RemoteBody -replace "`r`n", "`n"), [System.Text.UTF8Encoding]::new($false))
try {
    & scp.exe @SshOptions $LocalRemoteScript "${Target}:$RemoteScript"
    if ($LASTEXITCODE -ne 0) { throw '远端发布脚本上传失败。' }
    Write-Host '[4/4] migration、切换版本并验证健康状态...'
    & ssh.exe @SshOptions $Target "sh '$RemoteScript'"
    if ($LASTEXITCODE -ne 0) { throw '正式服务发布失败；如果新版未通过健康检查，脚本已尝试回退。' }
} finally {
    if (Test-Path -LiteralPath $LocalRemoteScript) { Remove-Item -LiteralPath $LocalRemoteScript -Force }
}
Write-Host "服务端发布完成：新 BT API（见 config/project.json）（版本 $Version）"
