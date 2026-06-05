#!/bin/bash
#
# BlockScanner 打包脚本
# 编译并打包为可部署的 zip 文件
#

set -e

PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"
SRC_DIR="$PROJECT_DIR/src"
BIN_NAME="blockscanner"
BUILD_DIR="$PROJECT_DIR/build"
PACKAGE_NAME="blockscanner-$(date +%Y%m%d-%H%M%S).zip"

echo "[Build] 清理旧的构建目录..."
rm -rf "$BUILD_DIR"
mkdir -p "$BUILD_DIR"

echo "[Build] 编译 $BIN_NAME ..."
cd "$SRC_DIR"
go build -o "$PROJECT_DIR/$BIN_NAME" .

echo "[Build] 复制部署文件..."
cp "$PROJECT_DIR/$BIN_NAME"   "$BUILD_DIR/"
cp "$PROJECT_DIR/config.yaml" "$BUILD_DIR/"
cp "$PROJECT_DIR/start.sh"    "$BUILD_DIR/"
cp "$PROJECT_DIR/stop.sh"     "$BUILD_DIR/"

chmod +x "$BUILD_DIR/start.sh"
chmod +x "$BUILD_DIR/stop.sh"
chmod +x "$BUILD_DIR/$BIN_NAME"

echo "[Build] 打包 $PACKAGE_NAME ..."
cd "$BUILD_DIR"
zip -r "$PROJECT_DIR/$PACKAGE_NAME" ./*

cd "$PROJECT_DIR"
rm -rf "$BUILD_DIR"

echo "[Build] 完成: $PACKAGE_NAME"
ls -lh "$PACKAGE_NAME"
