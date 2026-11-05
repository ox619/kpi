# Linux 部署（无 Docker / 无源码 / 无 nginx）

生产机上只有运行产物：Go 二进制 + Next.js standalone 前端 + 一份安装脚本。服务器**不需要** Go、npm、nginx、源码，只需要 Node.js 18+。

架构：浏览器 → `http://<服务器IP>:3000`（前端） + `http://<服务器IP>:19080/api`（后端，前端直连，后端已开启 CORS）。

## 1. 本地打包（Windows）

```powershell
powershell -ExecutionPolicy Bypass -File scripts/package-linux.ps1
# ARM 机器（鲲鹏 / 阿里云 ARM 实例等）：
powershell -ExecutionPolicy Bypass -File scripts/package-linux.ps1 -Arch arm64
# 后端换端口：
powershell -ExecutionPolicy Bypass -File scripts/package-linux.ps1 -ApiPort 19080
```

产物：`dist/kpi-<version>-linux-<arch>.tar.gz`（约 21 MB；后端是 debug 版，带符号表）。

构建期固化的变量（`NEXT_PUBLIC_*` 改端口必须重打包）：

- `NEXT_OUTPUT_MODE=standalone`
- `NEXT_PUBLIC_BASE_PATH=`（空，直接部署在根路径）
- `NEXT_PUBLIC_API_PORT=19080`（前端按访问地址推导 `http://<当前访问主机>:19080/api`，不用写死 IP/域名）

## 2. 服务器安装

```bash
# 只需要 Node.js 18+（推荐 20 LTS）
curl -fsSL https://deb.nodesource.com/setup_20.x | sudo -E bash -
sudo apt install -y nodejs curl

scp dist/kpi-0.1.0-linux-amd64.tar.gz root@server:/tmp/
ssh root@server
tar -xzf /tmp/kpi-0.1.0-linux-amd64.tar.gz -C /tmp
sudo sh /tmp/kpi/install.sh
```

安装脚本会：装到 `/web`、创建 `kpi` 用户、写 `kpi-api`（19080）/ `kpi-web`（3000）两个 systemd 服务并启动。

## 3. 放行端口

```bash
sudo ufw allow 3000/tcp
sudo ufw allow 19080/tcp

# CentOS / 阿里云等用 firewalld：
sudo firewall-cmd --add-port=3000/tcp --add-port=19080/tcp --permanent
sudo firewall-cmd --reload
```

云服务器还要在**安全组**里放行 3000 和 19080。

访问：`http://<服务器IP>:3000`，默认账号 `sunba@company.com` / `123456`（登录后立即改密码）。

## 调试后端（debug 版）

打包脚本对后端就是一句 `go build`，不加 `-trimpath -ldflags "-s -w"`，所以 `/web/dootask-kpi-server` 里保留了符号表和行号，可以直接用 Delve 调试：

```bash
# 服务器上装 dlv（或 go install github.com/go-delve/delve/cmd/dlv@latest）
sudo apt install -y delve

# 附加到正在运行的后端（systemd 起的进程就是 dootask-kpi-server）
sudo dlv attach "$(pgrep -f '^/web/dootask-kpi-server$')" --headless --listen=:2345 --api-version=2 --accept-multiclient
```

本机做端口转发后在 IDE 里连 `127.0.0.1:2345`（GoLand 的 Go Remote / VS Code 的 dlv remote）：

```bash
ssh -L 2345:127.0.0.1:2345 root@<服务器IP>
```

要单步更顺滑，把打包脚本里的 `go build` 改成 `go build -gcflags "all=-N -l"`（关掉内联和优化）；正式发版再换回 `go build -trimpath -ldflags "-s -w"` 换回精简体积。

## 密钥文件（/kpi/wecom.env）

后端所有敏感配置集中在这一个文件里，权限 `0600 root:root`，既不进仓库也不进安装包（安装包只带脱敏模板 `deploy/wecom.env.example`，打包脚本按白名单复制，真实 `wecom.env` 不会进包）：

| 键 | 内容 | 来源 |
| --- | --- | --- |
| `WECOM_*` | 企业微信接入配置 | 安装包内模板 `wecom.env.example` 拷入，需手工填写；留空则仅关闭企微功能 |
| `JWT_SECRET` | 登录态签名密钥（64 位十六进制随机值） | `install.sh` 首次安装自动生成并追加 |

`kpi-api.service` 用 `EnvironmentFile=-/kpi/wecom.env` 引用（前缀 `-` 表示文件缺失不报错）。注意事项：

- **升级直接重跑 `install.sh` 即可。** 它会按 key 检测 `JWT_SECRET`，缺失则往现有 `wecom.env` 追加，其余内容原样保留，不会覆盖已填的企微配置。
- 企微配置是**可选**项：文件不存在时 `install.sh` 会从模板建一份（连模板都没有就建空文件），安装不会中断；`JWT_SECRET` 的生成只依赖文件存在，与是否填了企微配置无关。`WECOM_*` 全空时后端静默降级为「企业微信未启用」，站内账号密码登录不受影响。
- 不要随意改动 `JWT_SECRET`，改一次等于让全站登录态立即失效。
- 后端读不到 `JWT_SECRET` 时回退到源码内置默认值并打印 `[auth] 严重警告`，服务仍可启动，但该默认值随源码公开，等于登录态可被伪造；正常安装流程不会走到这一步。
- 手动补建：`printf '\nJWT_SECRET=%s\n' "$(openssl rand -hex 32)" >> /kpi/wecom.env && systemctl restart kpi-api`

## 关于 HTTPS

当前是前端直连后端，没有反向代理：

- 若只是内网/HTTP 使用，不需要额外配置。
- 若后续要 HTTPS，必须**前端和后端都上 TLS**——只给 3000 配证书的话，页面是 https、API 仍是 `http://IP:19080`，浏览器会按混合内容拦截。届时最简单是用 Caddy/nginx 统一反代，并重新打包（`NEXT_PUBLIC_API_URL=/api`）。

## 升级

同一脚本重复执行即可（覆盖产物并重启服务，`db/` 不动）：

```bash
tar -xzf kpi-<新版本>-linux-amd64.tar.gz -C /tmp && sudo sh /tmp/kpi/install.sh
```

## 备份

```bash
sudo systemctl stop kpi-api
sudo cp /web/db/kpi.db /root/kpi-$(date +%F).db
sudo systemctl start kpi-api
```

`/web/db/backups` 是应用内「备份」页面生成的文件，`/web/public/exports` 是导出的 Excel，可按需清理。

## 目录结构

```
/web
├── dootask-kpi-server # Go 后端二进制（监听 19080）
├── server.js           # Next.js standalone 入口（监听 3000）
├── .next/              # 前端构建产物
├── node_modules/       # 精简后的运行时依赖
├── public/             # 静态资源 + exports/
└── db/kpi.db           # SQLite 数据（+ backups/）
```
