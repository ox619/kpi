#!/bin/sh
# KPI 绩效考核 —— 停止 / 管理 systemd 服务脚本
# 服务由 install.sh 创建：kpi-api（后端 19080）、kpi-web（前端 3000）
# 注意：两个服务都配了 Restart=always，直接 kill 进程会被 systemd 自动拉起，
#       必须通过本脚本 / systemctl 停止。
#
# 用法（需要 root）：
#   sudo sh stop.sh                 # 停止 kpi-api、kpi-web 并取消开机自启
#   sudo sh stop.sh --restart       # 重启服务（升级/改配置后常用）
#   sudo sh stop.sh --status        # 查看当前运行状态

set -e

SERVICES="kpi-api kpi-web"

log() { printf '\033[36m==> %s\033[0m\n' "$*"; }

[ "$(id -u)" -eq 0 ] || { echo "请用 root 运行：sudo sh $0"; exit 1; }

stop_one() {
  if systemctl is-active --quiet "$1"; then
    systemctl stop "$1"
    log "已停止 $1"
  else
    log "$1 未在运行"
  fi
}

case "$1" in
  --restart)
    systemctl restart $SERVICES
    log "已重启：$SERVICES"
    ;;
  --status)
    systemctl status $SERVICES --no-pager || true
    exit 0
    ;;
  *)
    # 默认：停止服务并取消开机自启
    for svc in $SERVICES; do
      stop_one "$svc"
      systemctl disable "$svc" >/dev/null 2>&1 || true
      log "已取消开机自启 $svc"
    done
    ;;
esac

log "当前状态："
for svc in $SERVICES; do
  printf '  %-10s %s\n' "$svc" "$(systemctl is-active "$svc")"
done
