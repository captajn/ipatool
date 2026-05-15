#!/usr/bin/env bash
# Auto-clone majd/ipatool + apply patches + build cho platform hiện tại.
# Yêu cầu: git, go 1.21+
#
# Cách dùng:
#   ./scripts/build-ipatool.sh              # build cho OS hiện tại, output: ./ipatool[.exe]
#   ./scripts/build-ipatool.sh linux amd64  # cross-compile
#   ./scripts/build-ipatool.sh windows amd64
#
set -euo pipefail

UPSTREAM_URL="https://github.com/majd/ipatool.git"
UPSTREAM_TAG="${IPATOOL_TAG:-v2.3.0}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PATCH_FILE="$SCRIPT_DIR/ipatool.patch"
WORK_DIR="${IPATOOL_WORK_DIR:-./.ipatool-build}"

GOOS="${1:-$(go env GOOS)}"
GOARCH="${2:-$(go env GOARCH)}"

OUT_NAME="ipatool"
if [ "$GOOS" = "windows" ]; then
    OUT_NAME="ipatool.exe"
fi

echo "🔄 Building ipatool ($GOOS/$GOARCH) from $UPSTREAM_URL @ $UPSTREAM_TAG"

# 1. Clone upstream (skip nếu đã có)
if [ ! -d "$WORK_DIR" ]; then
    echo "📥 Cloning $UPSTREAM_URL → $WORK_DIR ..."
    git clone --depth 1 --branch "$UPSTREAM_TAG" "$UPSTREAM_URL" "$WORK_DIR"
else
    echo "✅ $WORK_DIR đã tồn tại, dùng lại."
fi

cd "$WORK_DIR"

# 2. Reset working tree về clean state để apply patch không lỗi
echo "🧹 Reset working tree..."
git reset --hard HEAD

# 3. Apply patches
echo "🩹 Applying patches từ $PATCH_FILE ..."
git apply --check "$PATCH_FILE" || {
    echo "❌ Patch không apply được. Có thể tag upstream đã thay đổi."
    echo "   Thử IPATOOL_TAG=<tag-khác> ./scripts/build-ipatool.sh"
    exit 1
}
git apply "$PATCH_FILE"

# 4. Build
echo "🔨 Building..."
GOOS="$GOOS" GOARCH="$GOARCH" go build -o "../$OUT_NAME" .

cd ..
echo "✅ Done: $OUT_NAME"
echo ""
echo "📦 Binary: $(realpath "$OUT_NAME")"
echo ""
echo "💡 Đặt vào PATH:"
case "$GOOS" in
    linux|darwin)
        echo "   sudo mv $OUT_NAME /usr/local/bin/"
        ;;
    windows)
        echo "   Copy $OUT_NAME vào thư mục có trong %PATH% (vd: C:\\Users\\<you>\\bin\\)"
        ;;
esac
