#!/usr/bin/env python3
"""
Bot lifecycle manager.

Cach dung:
    python scripts/run-bot.py            # start + auto-restart on crash
    python scripts/run-bot.py --once     # start 1 lan, exit khi bot crash
    python scripts/run-bot.py --stop     # kill tat ca bot process dang chay
"""

import argparse
import os
import signal
import subprocess
import sys
import time
from pathlib import Path

# Force UTF-8 output trên Windows (mac dinh cp1252 khong show duoc emoji)
if sys.platform == "win32":
    try:
        sys.stdout.reconfigure(encoding="utf-8")
        sys.stderr.reconfigure(encoding="utf-8")
    except Exception:
        pass

REPO_ROOT = Path(__file__).resolve().parent.parent
BOT_EXE_NAMES = ["ipa-downloader-bot.exe", "ipa-downloader-bot"]
RESTART_DELAY = 5  # giây đợi trước khi restart sau crash


def find_bot_binary() -> Path:
    """Tìm file binary của bot ở repo root."""
    for name in BOT_EXE_NAMES:
        p = REPO_ROOT / name
        if p.exists():
            return p
    print("❌ Không tìm thấy ipa-downloader-bot[.exe]")
    print("   Build trước: go build -o ipa-downloader-bot.exe .")
    sys.exit(1)


def stop_existing():
    """Kill mọi process bot đang chạy (Windows + Linux)."""
    if sys.platform == "win32":
        for name in BOT_EXE_NAMES:
            subprocess.run(
                ["taskkill", "/F", "/IM", name],
                capture_output=True,
                text=True,
            )
        print("🛑 Đã kill mọi bot process trên Windows")
    else:
        subprocess.run(["pkill", "-f", "ipa-downloader-bot"], capture_output=True)
        print("🛑 Đã kill bot process trên Linux/macOS")


def run_once(bot_path: Path) -> int:
    """Chạy bot 1 lần. Trả exit code."""
    print(f"🚀 Starting {bot_path.name} ...")
    print("   Logs sẽ stream xuống console. Ctrl+C để dừng.\n")
    try:
        proc = subprocess.run(
            [str(bot_path)],
            cwd=str(REPO_ROOT),
        )
        return proc.returncode
    except KeyboardInterrupt:
        print("\n🛑 Stopped by user (Ctrl+C)")
        return 0


def run_forever(bot_path: Path):
    """Chạy bot, auto-restart nếu crash. Ctrl+C để dừng hẳn."""
    while True:
        code = run_once(bot_path)
        if code == 0:
            print("✅ Bot exited cleanly")
            break
        print(f"\n💥 Bot crashed (exit {code}). Restart sau {RESTART_DELAY}s...")
        try:
            time.sleep(RESTART_DELAY)
        except KeyboardInterrupt:
            print("\n🛑 Cancelled restart")
            break


def main():
    parser = argparse.ArgumentParser(description="IPA Downloader Bot manager")
    parser.add_argument("--once", action="store_true", help="run 1 lần, không auto-restart")
    parser.add_argument("--stop", action="store_true", help="kill tất cả bot process")
    args = parser.parse_args()

    if args.stop:
        stop_existing()
        return

    # Đảm bảo không có bot cũ đang chạy
    stop_existing()
    time.sleep(1)

    bot = find_bot_binary()

    if args.once:
        sys.exit(run_once(bot))
    else:
        run_forever(bot)


if __name__ == "__main__":
    # Forward Ctrl+C cho subprocess (Windows-friendly)
    if sys.platform == "win32":
        signal.signal(signal.SIGINT, signal.default_int_handler)
    main()
