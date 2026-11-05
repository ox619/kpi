#!/bin/sh
# KPI 绩效考核 —— Linux 安装脚本（不含源码，不需要 Docker / Go / npm）
#
# 用法：
#   tar -xzf kpi-<version>-linux-<arch>.tar.gz
#   sudo sh kpi/install.sh
#
# 可用环境变量：
#   APP_DIR=/web        安装目录（默认 /web，需与备份功能硬编码的 /web/db/backups 对齐）
#   RUN_USER=kpi        运行用户
#   API_PORT=19080      后端端口（前端直连，浏览器需要能访问）
#   WEB_PORT=3000       前端端口（浏览器访问入口）
#   SYSTEM_MODE=standalone   standalone 会初始化测试账号，integrated 用于对接 DooTask

set -e

APP_DIR=${APP_DIR:-/web}
RUN_USER=${RUN_USER:-kpi}
API_PORT=${API_PORT:-19080}
WEB_PORT=${WEB_PORT:-3000}
SYSTEM_MODE=${SYSTEM_MODE:-standalone}

SRC_DIR=$(cd "$(dirname "$0")" && pwd)

log() { printf '\033[36m==> %s\033[0m\n' "$*"; }

[ "$(id -u)" -eq 0 ] || { echo "请用 root 运行：sudo sh $0"; exit 1; }

if ! command -v node >/dev/null 2>&1; then
  echo "缺少 Node.js，请先安装 Node.js 18+（推荐 20 LTS）"
  exit 1
fi
node -e 'const [a]=process.versions.node.split(".").map(Number); if (a < 18) { console.error("Node.js 版本过低：" + process.version + "，需要 18+"); process.exit(1) }'
log "Node.js $(node --version)"

if ! id -u "$RUN_USER" >/dev/null 2>&1; then
  log "创建系统用户 $RUN_USER"
  useradd --system --home-dir "$APP_DIR" --shell /usr/sbin/nologin "$RUN_USER" 2>/dev/null ||
    useradd -r -d "$APP_DIR" -s /usr/sbin/nologin "$RUN_USER"
fi

log "安装到 $APP_DIR"
mkdir -p "$APP_DIR/db" "$APP_DIR/public/exports"

# 后端二进制（先写临时文件再替换，避免覆盖正在运行的进程）
install -m 0755 "$SRC_DIR/dootask-kpi-server" "$APP_DIR/dootask-kpi-server.new"
mv -f "$APP_DIR/dootask-kpi-server.new" "$APP_DIR/dootask-kpi-server"

# 前端产物
rm -rf "$APP_DIR/.next" "$APP_DIR/node_modules"
cp -a "$SRC_DIR/.next" "$APP_DIR/.next"
cp -a "$SRC_DIR/node_modules" "$APP_DIR/node_modules"
cp -f "$SRC_DIR/server.js" "$APP_DIR/server.js"
cp -f "$SRC_DIR/package.json" "$APP_DIR/package.json"
# public 里可能有运行期生成的 exports，用合并覆盖的方式
cp -a "$SRC_DIR/public/." "$APP_DIR/public/"

log "写入 systemd 服务"
# service 模板是必需资源，缺失说明安装包不完整：给出明确报错，而不是让 set -e 隐式中断。
for unit in kpi-api kpi-web; do
  tmpl="$SRC_DIR/deploy/$unit.service"
  if [ ! -f "$tmpl" ]; then
    echo "安装包不完整：缺少 $tmpl"
    exit 1
  fi
  # 分隔符用 |：双引号内的 $# 会被 shell 当作特殊参数展开，会让 s#^User=kpi$# 永不匹配
  sed -e "s|/web|$APP_DIR|g" \
    -e "s|^User=kpi\$|User=$RUN_USER|" \
    -e "s|^Group=kpi\$|Group=$RUN_USER|" \
    "$tmpl" > "/etc/systemd/system/$unit.service"
done
sed -i -e "s|^Environment=PORT=.*|Environment=PORT=$API_PORT|" \
  -e "s|^Environment=SYSTEM_MODE=.*|Environment=SYSTEM_MODE=$SYSTEM_MODE|" \
  /etc/systemd/system/kpi-api.service
sed -i -e "s|^Environment=PORT=.*|Environment=PORT=$WEB_PORT|" /etc/systemd/system/kpi-web.service

chown -R "$RUN_USER:$RUN_USER" "$APP_DIR"

# 企微配置文件（kpi-api.service 用 EnvironmentFile=-/kpi/wecom.env 引用，前缀 - 表示缺失不影响启动）。
# 它是可选资源：不存在时绝不能中断安装，因为 JWT 签名密钥同样写在这个文件里。
mkdir -p /kpi
if [ -f /kpi/wecom.env ]; then
  log "保留现有企微配置文件 /kpi/wecom.env"
elif [ -f "$SRC_DIR/deploy/wecom.env.example" ]; then
  install -m 0600 -o root -g root "$SRC_DIR/deploy/wecom.env.example" /kpi/wecom.env
  log "已安装企微配置模板 /kpi/wecom.env（填写 WECOM_* 后执行 systemctl restart kpi-api）"
else
  (umask 077 && : > /kpi/wecom.env)
  log "未找到企微配置模板，已创建空文件 /kpi/wecom.env；企业微信功能将保持未启用"
fi

# JWT 签名密钥与企微配置共用 /kpi/wecom.env，不额外增加密钥文件。
# 只依赖文件存在（上面已保证），与企微是否配置无关，必须独立执行。
# 已存在就保留——更换该值会让所有已签发 token 立刻失效，全员需要重新登录。
if grep -q '^JWT_SECRET=' /kpi/wecom.env 2>/dev/null; then
  log "保留现有 JWT 签名密钥（/kpi/wecom.env）"
else
  JWT_SECRET=$(openssl rand -hex 32 2>/dev/null || od -An -tx1 -N32 /dev/urandom | tr -d '[:space:]')
  printf '\n# 登录态签名密钥（install.sh 自动生成，改动会让全站登录态立即失效）\nJWT_SECRET=%s\n' "$JWT_SECRET" >> /kpi/wecom.env
  log "已生成 JWT 签名密钥并写入 /kpi/wecom.env"
fi
chmod 0600 /kpi/wecom.env

# 安装后自检：企微配置缺失只提示不阻断，后端会静默降级为「企业微信未启用」
if ! grep -q '^WECOM_CORP_ID=..*' /kpi/wecom.env 2>/dev/null; then
  log "提示：/kpi/wecom.env 未填写 WECOM_*，企业微信功能不会启用（站内登录不受影响）"
fi

systemctl daemon-reload
systemctl enable kpi-api kpi-web >/dev/null 2>&1 || true
systemctl restart kpi-api kpi-web

log "等待服务启动"
i=0
while [ $i -lt 30 ]; do
  if curl -fsS "http://127.0.0.1:$API_PORT/health" >/dev/null 2>&1; then
    log "后端就绪：http://127.0.0.1:$API_PORT/health"
    break
  fi
  i=$((i + 1))
  sleep 1
done
if [ $i -ge 30 ]; then
  echo "后端启动超时，查看日志：journalctl -u kpi-api -n 100 --no-pager"
  exit 1
fi

echo ""
log "安装完成"
echo "  前端入口：http://<服务器IP>:$WEB_PORT"
echo "  后端接口：http://<服务器IP>:$API_PORT/api（前端直连，必须对外开放）"
echo "  后端日志：journalctl -u kpi-api -f"
echo "  前端日志：journalctl -u kpi-web -f"
echo "  数据目录：$APP_DIR/db/kpi.db（备份目录 $APP_DIR/db/backups）"
echo "  默认账号：sunba@company.com / 123456（登录后请立即修改密码）"
echo ""
echo "  请确认防火墙/安全组已放行 $WEB_PORT 与 $API_PORT，例如："
echo "    ufw allow $WEB_PORT/tcp && ufw allow $API_PORT/tcp"
echo "    firewall-cmd --add-port=$WEB_PORT/tcp --add-port=$API_PORT/tcp --permanent && firewall-cmd --reload"
