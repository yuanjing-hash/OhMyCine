#!/usr/bin/env bash

set -Eeuo pipefail
IFS=$'\n\t'

readonly SERVER_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd -P)"
readonly WEBUI_DIR="${SERVER_DIR}/webui"

SKIP_BUILD=false

print_usage() {
  cat <<'EOF'
OhMyCine Server 一键前台启动

用法：
  ./start.sh [--skip-build]
  ./start.sh --help

参数：
  --skip-build  跳过 Web UI 与 Go 构建，复用已有二进制快速重启
  -h, --help    显示本帮助；不会创建 .runtime 或其它运行数据

默认运行目录：
  .runtime/bin/ohmycine-server  带内嵌 Web UI 的 Server 二进制
  .runtime/data/ohmycine.db     持久化 SQLite 数据库

可覆盖的环境变量：
  OMC_RUNTIME_DIR    脚本运行目录（默认 server/.runtime）
  OMC_BINARY_PATH    Server 二进制路径（默认在运行目录的 bin/ 下）
  OMC_DATABASE_PATH  SQLite 路径（默认在运行目录的 data/ 下）
  OMC_ENV            运行环境（默认 production）
  OMC_SERVER_HOST    监听地址（默认 127.0.0.1）
  OMC_SERVER_PORT    监听端口（默认 3000）
  OMC_PUBLIC_ORIGIN  浏览器精确来源（默认按监听地址和端口生成）
  OMC_COOKIE_SECURE  Cookie Secure 开关（默认由 public origin 推导）

示例：
  ./start.sh
  ./start.sh --skip-build
  OMC_SERVER_PORT=3300 ./start.sh
  OMC_DATABASE_PATH=/srv/ohmycine/ohmycine.db ./start.sh

脚本以前台 exec 方式运行 Server。数据库会长期保留，脚本不会自动删除、
重置或覆盖现有数据库。若开放到局域网或公网，请显式配置监听地址、
HTTPS 反向代理和真实 OMC_PUBLIC_ORIGIN。
EOF
}

fail() {
  printf '错误：%s\n' "$*" >&2
  exit 1
}

info() {
  printf '==> %s\n' "$*"
}

absolute_server_path() {
  local path="$1"

  if [[ "${path}" == /* ]]; then
    printf '%s\n' "${path}"
  else
    printf '%s/%s\n' "${SERVER_DIR}" "${path}"
  fi
}

is_windows_command() {
  local command_path="$1"
  local resolved_path

  resolved_path="$(readlink -f -- "${command_path}" 2>/dev/null || printf '%s' "${command_path}")"
  [[ "${command_path}" == /mnt/[A-Za-z]/* ]] || \
    [[ "${resolved_path}" == /mnt/[A-Za-z]/* ]] || \
    [[ "${command_path}" == *.exe ]] || \
    [[ "${resolved_path}" == *.exe ]]
}

find_go() {
  local go_path
  local candidate

  go_path="$(command -v go 2>/dev/null || true)"
  if [[ -n "${go_path}" ]] && ! is_windows_command "${go_path}"; then
    printf '%s\n' "${go_path}"
    return
  fi

  for candidate in \
    /home/linuxbrew/.linuxbrew/bin/go \
    /opt/homebrew/bin/go; do
    if [[ -x "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return
    fi
  done

  return 1
}

find_npm() {
  local npm_path
  local candidate

  npm_path="$(command -v npm 2>/dev/null || true)"
  if [[ -n "${npm_path}" ]] && ! is_windows_command "${npm_path}"; then
    printf '%s\n' "${npm_path}"
    return
  fi

  for candidate in \
    /home/linuxbrew/.linuxbrew/bin/npm \
    /opt/homebrew/bin/npm; do
    if [[ -x "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return
    fi
  done

  return 1
}

while (($# > 0)); do
  case "$1" in
    --skip-build)
      SKIP_BUILD=true
      ;;
    -h | --help)
      print_usage
      exit 0
      ;;
    --)
      shift
      (($# == 0)) || fail "不支持位置参数：$*"
      break
      ;;
    *)
      fail "未知参数：$1（使用 --help 查看帮助）"
      ;;
  esac
  shift
done

cd -- "${SERVER_DIR}"

readonly RUNTIME_DIR="$(absolute_server_path "${OMC_RUNTIME_DIR:-.runtime}")"
readonly BINARY_PATH="$(absolute_server_path "${OMC_BINARY_PATH:-${RUNTIME_DIR}/bin/ohmycine-server}")"

export OMC_ENV="${OMC_ENV:-production}"
export OMC_SERVER_PORT="${OMC_SERVER_PORT:-3000}"

listen_host="${OMC_SERVER_HOST:-127.0.0.1}"
if [[ "${listen_host}" == *:* && "${listen_host}" != \[*\] ]]; then
  listen_host="[${listen_host}]"
fi
export OMC_SERVER_HOST="${listen_host}"

case "${OMC_SERVER_HOST}" in
  0.0.0.0)
    origin_host="127.0.0.1"
    ;;
  \[::\])
    origin_host="[::1]"
    ;;
  *)
    origin_host="${OMC_SERVER_HOST}"
    ;;
esac
export OMC_PUBLIC_ORIGIN="${OMC_PUBLIC_ORIGIN:-http://${origin_host}:${OMC_SERVER_PORT}}"
export OMC_DATABASE_PATH="${OMC_DATABASE_PATH:-${RUNTIME_DIR}/data/ohmycine.db}"

if [[ "${SKIP_BUILD}" == true ]]; then
  [[ -x "${BINARY_PATH}" ]] || fail "--skip-build 需要已有可执行二进制：${BINARY_PATH}"
  info "跳过构建，复用现有二进制"
else
  GO_BIN="$(find_go)" || fail "未找到 Go。请安装 Go 1.23+，或将 Linuxbrew Go 加入 PATH。"
  NPM_BIN="$(find_npm)" || fail "未找到 npm。请安装 Node.js/npm 并加入 PATH。"
  export PATH="$(dirname -- "${NPM_BIN}"):${PATH}"
  NODE_BIN="$(command -v node 2>/dev/null || true)"
  if [[ -z "${NODE_BIN}" ]] || is_windows_command "${NODE_BIN}"; then
    fail "找到 npm，但未找到可用的 WSL/Linux Node.js。请安装 Node.js 并加入 PATH。"
  fi

  [[ -f "${WEBUI_DIR}/package.json" ]] || fail "缺少 Web UI package.json：${WEBUI_DIR}/package.json"
  [[ -f "${WEBUI_DIR}/package-lock.json" ]] || fail "缺少 Web UI package-lock.json，无法安全执行 npm ci。"

  readonly NODE_MODULES_DIR="${WEBUI_DIR}/node_modules"
  readonly LOCKFILE_STAMP="${NODE_MODULES_DIR}/.ohmycine-package-lock.json"

  if [[ ! -d "${NODE_MODULES_DIR}" ]] || \
    [[ ! -f "${LOCKFILE_STAMP}" ]] || \
    ! cmp -s -- "${WEBUI_DIR}/package-lock.json" "${LOCKFILE_STAMP}"; then
    info "安装 Web UI 依赖（首次运行或 package-lock.json 已变化）"
    (
      cd -- "${WEBUI_DIR}"
      "${NPM_BIN}" ci
    )
    cp -- "${WEBUI_DIR}/package-lock.json" "${LOCKFILE_STAMP}"
  else
    info "Web UI 依赖未变化，复用现有 node_modules"
  fi

  info "构建 Web UI"
  (
    cd -- "${WEBUI_DIR}"
    "${NPM_BIN}" run build
  )

  info "构建带内嵌 Web UI 的 Server 二进制"
  mkdir -p -- "$(dirname -- "${BINARY_PATH}")"
  (
    cd -- "${SERVER_DIR}"
    "${GO_BIN}" build -tags webui -o "${BINARY_PATH}" ./cmd/server
  )
fi

mkdir -p -- "$(dirname -- "${OMC_DATABASE_PATH}")"

info "启动 OhMyCine Server（前台运行，Ctrl+C 可安全停止）"
printf '    地址：%s\n' "${OMC_PUBLIC_ORIGIN}"
printf '    数据库：%s\n' "${OMC_DATABASE_PATH}"
printf '    二进制：%s\n' "${BINARY_PATH}"

exec "${BINARY_PATH}"
