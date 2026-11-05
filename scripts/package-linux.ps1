#Requires -Version 5.1
<#
  打 Linux 部署包（只含运行产物，不含源码、不含 Docker）
  产物：dist/kpi-<version>-linux-<arch>.tar.gz

  用法（Windows PowerShell，在仓库根目录）：
    powershell -ExecutionPolicy Bypass -File scripts/package-linux.ps1
    powershell -ExecutionPolicy Bypass -File scripts/package-linux.ps1 -Arch arm64
    powershell -ExecutionPolicy Bypass -File scripts/package-linux.ps1 -SkipInstall   # 已 npm install 过

  服务器端只需要 Node.js 18+，不需要 Go / npm / 源码。
#>
[CmdletBinding()]
param(
  [ValidateSet("amd64", "arm64")]
  [string]$Arch = "amd64",
  # 后端端口：前端直连（不走反向代理），构建期固化到前端代码里
  [int]$ApiPort = 19080,
  [switch]$SkipInstall
)

$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $PSScriptRoot
$distDir = Join-Path $root "dist"
$stage = Join-Path $distDir "kpi"
$version = (Get-Content (Join-Path $root "package.json") -Raw | ConvertFrom-Json).version

Push-Location $root
try {
  if (-not $SkipInstall) {
    Write-Host "==> 安装前端依赖" -ForegroundColor Cyan
    npm ci
    if ($LASTEXITCODE -ne 0) { throw "npm ci 失败" }
  }

  Write-Host "==> 构建前端（standalone，根路径访问，直连后端 :$ApiPort）" -ForegroundColor Cyan
  # NEXT_PUBLIC_* 在构建期固化：直接部署在域名根路径，前端按自身地址推导
  # http://<访问地址>:$ApiPort/api，因此不需要写死服务器 IP/域名
  $env:NEXT_OUTPUT_MODE = "standalone"
  $env:NEXT_PUBLIC_BASE_PATH = ""
  $env:NEXT_PUBLIC_API_PORT = "$ApiPort"
  Remove-Item Env:NEXT_PUBLIC_API_URL -ErrorAction SilentlyContinue
  # 仓库现有代码有 ESLint 报错，打包时跳过 lint（类型检查仍会执行）
  npm run build -- --no-lint
  if ($LASTEXITCODE -ne 0) { throw "前端构建失败" }

  Write-Host "==> 交叉编译后端 linux/$Arch（debug 版，直接 go build）" -ForegroundColor Cyan
  Remove-Item $stage -Recurse -Force -ErrorAction SilentlyContinue
  New-Item -ItemType Directory -Force -Path $stage | Out-Null
  # 纯 Go 的 glebarez/sqlite，CGO_ENABLED=0 即可交叉编译
  $env:CGO_ENABLED = "0"
  $env:GOOS = "linux"
  $env:GOARCH = $Arch
  Push-Location (Join-Path $root "server")
  # 调试版：直接 go build（不 -trimpath、不 -ldflags "-s -w"，保留符号和行号）
  # 发布版（去掉调试符号，体积小约 30%）：go build -trimpath -ldflags "-s -w"
  # 产物名沿用 go build 的默认名 dootask-kpi-server，改名会同步影响 systemd/install.sh
  go build
  if ($LASTEXITCODE -ne 0) { throw "后端编译失败" }
  $built = Join-Path (Get-Location) "dootask-kpi-server"
  if (-not (Test-Path $built)) { $built = Join-Path (Get-Location) "server" }
  if (-not (Test-Path $built)) { throw "未找到 go build 产物" }
  Move-Item -Force $built (Join-Path $stage "dootask-kpi-server")
  Pop-Location

  Write-Host "==> 组装部署目录" -ForegroundColor Cyan
  # Next standalone 入口 + 精简 node_modules
  Get-ChildItem (Join-Path $root ".next\standalone") -Force |
    Copy-Item -Destination $stage -Recurse -Force
  # standalone 不带静态资源，需要手工补
  New-Item -ItemType Directory -Force -Path (Join-Path $stage ".next\static") | Out-Null
  Get-ChildItem (Join-Path $root ".next\static") -Force |
    Copy-Item -Destination (Join-Path $stage ".next\static") -Recurse -Force
  New-Item -ItemType Directory -Force -Path (Join-Path $stage "public") | Out-Null
  Get-ChildItem (Join-Path $root "public") -Force |
    Copy-Item -Destination (Join-Path $stage "public") -Recurse -Force

  # sharp 只为 next/image 服务（本项目未使用），且是平台相关的原生包，
  # 从 Windows/macOS 打包时带过去在 Linux 上反而会出问题，直接剔除。
  Remove-Item (Join-Path $stage "node_modules\@img") -Recurse -Force -ErrorAction SilentlyContinue
  Remove-Item (Join-Path $stage "node_modules\sharp") -Recurse -Force -ErrorAction SilentlyContinue

  # 部署脚本与 systemd/nginx 模板
  # 白名单逐项复制：不能用 deploy\linux\* 通配，否则会把本地真实的 wecom.env（含企微 Secret）打进 tar 包。
  # 新增部署资源时必须同步维护下面的清单。
  $deployStage = Join-Path $stage "deploy"
  New-Item -ItemType Directory -Force -Path $deployStage | Out-Null
  $deployFiles = @(
    "install.sh",
    "stop.sh",
    "kpi-api.service",
    "kpi-web.service",
    "README.md",
    "wecom.env.example"
  )
  foreach ($f in $deployFiles) {
    $src = Join-Path $root "deploy\linux\$f"
    if (-not (Test-Path $src)) { throw "缺少部署资源：deploy\linux\$f" }
    Copy-Item $src -Destination $deployStage -Force
  }
  # 兜底：即便上面的清单被误加回真实配置，也绝不让 wecom.env 进包
  Remove-Item (Join-Path $deployStage "wecom.env") -Force -ErrorAction SilentlyContinue
  Copy-Item (Join-Path $root "deploy\linux\install.sh") -Destination $stage -Force
  Copy-Item (Join-Path $root "deploy\linux\stop.sh") -Destination $stage -Force

  # 版本信息，便于排查线上版本
  Set-Content -Path (Join-Path $stage "VERSION") -Value "$version linux/$Arch" -Encoding utf8

  $tarball = Join-Path $distDir "kpi-$version-linux-$Arch.tar.gz"
  Remove-Item $tarball -Force -ErrorAction SilentlyContinue
  Write-Host "==> 打包 $tarball" -ForegroundColor Cyan
  tar -czf $tarball -C $distDir kpi
  if ($LASTEXITCODE -ne 0) { throw "tar 打包失败" }

  $sizeMb = "{0:N1}" -f ((Get-Item $tarball).Length / 1MB)
  Write-Host ""
  Write-Host "完成：$tarball ($sizeMb MB)" -ForegroundColor Green
  Write-Host "上传后执行：" -ForegroundColor Green
  Write-Host "  tar -xzf kpi-$version-linux-$Arch.tar.gz && sudo sh kpi/install.sh"
}
finally {
  Pop-Location
}

exit 0
