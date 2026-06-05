#!/bin/bash
#
# BlockScanner 启动脚本
#

set -e

PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"
BIN_NAME="blockscanner"
BIN_PATH="$PROJECT_DIR/$BIN_NAME"
PID_FILE="$PROJECT_DIR/.blockscanner.pid"
LOG_FILE="$PROJECT_DIR/blockscanner.log"

# --- 检查是否已经运行 ---
if [ -f "$PID_FILE" ]; then
    pid=$(cat "$PID_FILE")
    if kill -0 "$pid" 2>/dev/null; then
        echo "[BlockScanner] 服务已在运行中 (pid=$pid)，请先执行 stop.sh"
        exit 1
    fi
    rm -f "$PID_FILE"
fi

# --- 检查二进制文件 ---
if [ ! -f "$BIN_PATH" ]; then
    echo "[BlockScanner] 二进制文件不存在: $BIN_PATH，请先编译"
    exit 1
fi

# --- 启动（工作目录为项目根，确保能找到 config.yaml）---
echo "[BlockScanner] 正在启动..."
cd "$PROJECT_DIR"
nohup "$BIN_PATH" >> "$LOG_FILE" 2>&1 &
pid=$!

echo "$pid" > "$PID_FILE"

sleep 2
if kill -0 "$pid" 2>/dev/null; then
    echo "[BlockScanner] 启动成功 (pid=$pid)"
    echo "[BlockScanner] 日志文件: $LOG_FILE"
else
    echo "[BlockScanner] 启动失败，请查看日志: $LOG_FILE"
    rm -f "$PID_FILE"
    exit 1
fi
