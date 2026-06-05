#!/bin/bash
#
# BlockScanner 停止脚本
#

set -e

PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"
BIN_NAME="blockscanner"
PID_FILE="$PROJECT_DIR/.blockscanner.pid"
SHUTDOWN_TIMEOUT=30

# --- 通过进程名查找 ---
find_pids() {
    pgrep -f "$BIN_NAME" 2>/dev/null || true
}

# --- 检查 PID 文件 ---
if [ ! -f "$PID_FILE" ]; then
    echo "[BlockScanner] 未找到 PID 文件，尝试通过进程名查找..."
    pids=$(find_pids)
    if [ -z "$pids" ]; then
        echo "[BlockScanner] 未找到运行中的进程"
        exit 0
    fi
    echo "[BlockScanner] 找到进程: $pids，尝试停止..."
    for pid in $pids; do
        kill "$pid" 2>/dev/null || true
    done
    sleep 2
    pids=$(find_pids)
    if [ -n "$pids" ]; then
        echo "[BlockScanner] 进程未响应，强制停止..."
        for pid in $pids; do
            kill -9 "$pid" 2>/dev/null || true
        done
    fi
    echo "[BlockScanner] 已停止"
    exit 0
fi

# --- 读取 PID ---
pid=$(cat "$PID_FILE")
if [ -z "$pid" ]; then
    echo "[BlockScanner] PID 文件为空"
    rm -f "$PID_FILE"
    exit 1
fi

if ! kill -0 "$pid" 2>/dev/null; then
    echo "[BlockScanner] 进程 $pid 不存在，清理 PID 文件"
    rm -f "$PID_FILE"
    exit 0
fi

# --- 优雅停止 ---
echo "[BlockScanner] 正在停止进程 (pid=$pid)..."
kill "$pid"

waited=0
while kill -0 "$pid" 2>/dev/null && [ "$waited" -lt "$SHUTDOWN_TIMEOUT" ]; do
    sleep 1
    waited=$((waited + 1))
    echo -n "."
done
echo ""

if kill -0 "$pid" 2>/dev/null; then
    echo "[BlockScanner] 超时，强制停止..."
    kill -9 "$pid"
    sleep 1
fi

rm -f "$PID_FILE"

if kill -0 "$pid" 2>/dev/null; then
    echo "[BlockScanner] 停止失败 (pid=$pid)"
    exit 1
else
    echo "[BlockScanner] 已停止"
fi
