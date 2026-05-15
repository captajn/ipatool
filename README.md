# 📲 IPA Downloader Telegram Bot

🇬🇧 **English** → [click here](#-english) · 🇻🇳 **Tiếng Việt** → đọc bên dưới ⬇️

---

<a id="vi"></a>

## 🇻🇳 Tiếng Việt

Telegram bot tải file IPA trực tiếp từ App Store về Telegram. Hỗ trợ nhiều Apple ID, 2FA, và tải file lớn (≤ 2GB free / ≤ 4GB premium).

### ⚙️ Dựa trên dự án

Bot được xây dựng dựa trên [`majd/ipatool`](https://github.com/majd/ipatool) (CLI gốc của Apple App Store) — cảm ơn tác giả vì công cụ tuyệt vời.

Repo này (https://github.com/captajn/ipatool) là **bản fork & chỉnh sửa** để chạy được như Telegram bot:
- Bỏ phụ thuộc keychain GUI (Keychain macOS / WinCred / SecretService) — chỉ dùng `FileBackend` để chạy headless trên server.
- Fix panic `interface conversion: nil, not bool` trong `cmd/auth.go` & `cmd/download.go` (mismatch context key).
- Retry tự động khi Apple trả non-plist response (rate limit / HTML page) ở bước Bag và Login.
- Per-user isolation: mỗi user Telegram có thư mục keychain + cookie jar riêng.
- Mask credentials trong log để bảo vệ privacy.

### ✨ Tính năng

- 📲 **Tải IPA** từ link App Store hoặc App ID
- 👥 **Multi-account** — nhiều Apple ID, dễ chuyển đổi
- 🔐 **2FA support** — nhập mã xác thực 6 chữ số
- 📊 **Progress bar** — hiển thị % + tốc độ + ETA khi upload lên Telegram
- 🌐 **Multi-region lookup** — tìm app trên 14 vùng khác nhau (us, vn, jp, gb, de, fr, kr, cn, sg, th, au, tw, ca, in)
- 🔗 **Web installer link** — tạo link cài trực tiếp lên iPhone/iPad qua `/getlink`
- ⚡ **Fast login** — đăng nhập 1 dòng `/login email password`
- 🛡️ **Privacy-first** — credentials được mask trong log

### 🔒 Cam kết riêng tư & bảo mật

Toàn bộ **20 lỗ hổng phổ biến trong code** đã được audit và mitigate. Xem chi tiết tại [SECURITY.md](./SECURITY.md).

| Dữ liệu | Ở đâu | Mã hoá? |
|---------|-------|---------|
| **Apple ID password** | MongoDB | ✅ **AES-256-GCM** với key 32 bytes random |
| Apple ID email | MongoDB | ❌ Plain (cần để debug) |
| Mã 2FA | RAM only (gọi 1 lần xong quên) | N/A |
| App Store cookies | `tmp/users/<chatID>/.ipatool/cookies` | ✅ Encrypted (ipatool FileBackend) |
| Apple keychain | `tmp/users/<chatID>/.ipatool/keychain` | ✅ Encrypted với passphrase |
| ipatool errors log | `tmp/users/<chatID>/ipatool.log` | ✅ Password đã mask thành `***` |
| File IPA | `tmp/downloads/<uuid>/` | ❌ Plain — xóa ngay sau upload |

**Bot CAM KẾT:**
- ✅ KHÔNG bao giờ lưu password plaintext
- ✅ KHÔNG bao giờ log password hoặc mã 2FA
- ✅ KHÔNG gửi credentials đến bên thứ 3 — outbound HTTPS chỉ tới `*.apple.com`, `*.mzstatic.com`, MongoDB của bạn, và Telegram
- ✅ Rate limit chống brute-force (5 login fail / 10 phút)
- ✅ Path traversal protection ở web endpoints
- ✅ SSRF protection (icon URL whitelist Apple CDN)
- ✅ XSS protection (escape user input trước khi render HTML)

**Khuyến nghị:**
- Dùng Apple ID phụ nếu muốn tách biệt với tài khoản chính
- Self-host bot riêng trên server của bạn (instructions ở dưới)
- Generate `ENCRYPTION_KEY` riêng — không bao giờ dùng key của repo

### 🚀 Lệnh bot

| Lệnh | Mô tả |
|------|-------|
| `/start` | Mở menu chính (welcome page với buttons) |
| `/login <email> <password>` | Đăng nhập nhanh (bot tự xóa tin nhắn của bạn để bảo mật) |
| `/get <link\|appID>` | Tải IPA phiên bản mới nhất, không hỏi gì thêm |
| `/accounts` | Quản lý nhiều Apple ID — chuyển/xóa |
| `/addaccount` | Thêm Apple ID khác |
| `/logout` | Đăng xuất tài khoản đang dùng |
| `/clearall confirm` | Xóa toàn bộ dữ liệu của bạn (DB + keychain) |
| `/getlink` | Tạo link cài đặt trực tiếp (reply vào file IPA) |
| `/help` | Trợ giúp |
| `/privacy` | Chính sách bảo mật |

**Lệnh admin (chỉ admin):**

| Lệnh | Mô tả |
|------|-------|
| `/add <chatID>` | Cấp Premium |
| `/rm <chatID>` | Xóa Premium |
| `/listpre` | Danh sách Premium |
| `/listuser` | Danh sách user |
| `/check <chatID>` | Xem thông tin user |

### 🛠 Cài đặt (self-host)

#### 1. Yêu cầu

- Go 1.21+
- MongoDB Atlas (free tier OK)
- Telegram Bot Token (lấy từ [@BotFather](https://t.me/BotFather))
- Telegram API ID + Hash (lấy từ [my.telegram.org](https://my.telegram.org))
- ipatool binary đã patched (build từ `majd-ipatool-src/` hoặc tải binary có sẵn)

#### 2. Clone & cấu hình

```bash
git clone https://github.com/captajn/ipatool.git
cd ipatool
cp .env.example .env
```

Sửa `.env`:

```env
BOT_TOKEN=your_telegram_bot_token
API_ID=your_telegram_api_id
API_HASH=your_telegram_api_hash
ADMIN_ID=your_telegram_user_id
MONGODB_URI=mongodb+srv://user:pass@cluster.mongodb.net/?appName=botipa
ENCRYPTION_KEY=<sinh_bằng_openssl_rand_-base64_32>
WEB_PORT=7098
HOST_URL=https://your-domain.com
DOWNLOAD_LIMIT_NORMAL=2048
DOWNLOAD_LIMIT_PREMIUM=4096
COOLDOWN_DURATION=15
SESSION_TIMEOUT=5
```

**Sinh `ENCRYPTION_KEY`** (32 bytes random, dùng để mã hoá password trong DB):

```bash
# Linux / macOS
openssl rand -base64 32

# Windows PowerShell
[Convert]::ToBase64String((1..32 | %{Get-Random -Maximum 256}))
```

⚠️ **Cảnh báo:** Đổi `ENCRYPTION_KEY` sau khi deploy = **mất tất cả password đã lưu** (user phải đăng nhập lại).

#### 3. Build patched ipatool

```bash
# Clone source ipatool gốc
git clone https://github.com/majd/ipatool.git majd-ipatool-src
cd majd-ipatool-src

# Patch để dùng FileBackend only + fix interactiveKey
# (Xem patches trong PATCHES.md hoặc copy từ /e/Code/majd-ipatool-src/)

# Build
GOOS=linux GOARCH=amd64 go build -o ../ipatool .          # Linux
GOOS=windows GOARCH=amd64 go build -o ../ipatool.exe .    # Windows

# Đặt vào PATH
sudo mv ../ipatool /usr/local/bin/                        # Linux
# hoặc copy ipatool.exe vào %PATH% trên Windows
```

#### 4. Chạy bot

```bash
go build -o ipa-downloader-bot .
./ipa-downloader-bot
```

Bot sẽ in:
```
📦 Connected to MongoDB
🌍 Web Server starting on port 7098
🚀 MTProto connected
🤖 Bot started as your_bot_username
🚀 Bot is running. Press Ctrl+C to stop.
```

### 🧪 Test

```bash
go vet ./...
go build ./...
go test ./...
```

### 📂 Cấu trúc dự án

```
.
├── main.go                    # Entry point
├── bot/                       # Telegram bot logic
│   ├── bot.go                 # Bot init + helpers (SafeSend, SafeEdit, SafeDelete)
│   ├── commands.go            # Command handlers + /start UI
│   ├── callbacks.go           # Callback (inline button) handlers
│   ├── login.go               # Login flow + /login command
│   ├── accounts.go            # Multi-account: /accounts, switch, remove
│   ├── download.go            # Download IPA + progress bar
│   └── admin.go               # Admin commands
├── pkg/
│   ├── ipatool/               # Wrapper cho ipatool CLI
│   │   └── ipatool.go         # Per-user lock, error categorize, mask credentials
│   ├── itunes/                # iTunes API multi-region lookup
│   └── mtproto/               # MTProto cho upload file > 50MB
├── db/mongo.go                # User schema (multi-account support)
├── config/config.go           # Load .env
├── server/                    # Web server cho /i/<file> redirect
├── cmd/
│   └── reset/main.go          # Tool xóa data 1 user (test)
└── tmp/                       # Runtime (gitignore)
    ├── users/<chatID>/        # Keychain + log per-user
    └── downloads/<uuid>/      # IPA tạm thời (auto-cleanup)
```

### 🐛 Báo lỗi

- Issue: https://github.com/captajn/ipatool/issues
- ipatool binary lỗi? Xem `tmp/users/<chatID>/ipatool.log`
- Bot lỗi? Check stdout (đã mask credentials)

### 📜 License

- Code bot: MIT
- ipatool gốc: MIT (https://github.com/majd/ipatool)

---

<a id="-english"></a>

## 🇬🇧 English

[⬆ Lên đầu / Back to top](#-ipa-downloader-telegram-bot)

Telegram bot to download IPA files directly from the App Store to Telegram. Supports multiple Apple IDs, 2FA, and large files (≤ 2GB free / ≤ 4GB premium).

### ⚙️ Built upon

This bot is built on top of [`majd/ipatool`](https://github.com/majd/ipatool) (the original Apple App Store CLI) — huge thanks to the author for this excellent tool.

This repo (https://github.com/captajn/ipatool) is a **fork & modification** to make it run as a Telegram bot:
- Removed GUI keychain dependency (macOS Keychain / WinCred / SecretService) — uses `FileBackend` only for headless server use.
- Fixed `interface conversion: nil, not bool` panic in `cmd/auth.go` & `cmd/download.go` (context key mismatch upstream).
- Auto-retry when Apple returns non-plist response (rate limit / HTML page) on Bag and Login steps.
- Per-user isolation: each Telegram user has separate keychain + cookie jar directory.
- Credential masking in logs for privacy.

### ✨ Features

- 📲 **Download IPA** from App Store link or App ID
- 👥 **Multi-account** — multiple Apple IDs with easy switching
- 🔐 **2FA support** — enter 6-digit verification code
- 📊 **Progress bar** — show % + speed + ETA during Telegram upload
- 🌐 **Multi-region lookup** — find apps across 14 regions (us, vn, jp, gb, de, fr, kr, cn, sg, th, au, tw, ca, in)
- 🔗 **Web installer link** — generate direct iPhone/iPad install link via `/getlink`
- ⚡ **Fast login** — one-liner `/login email password`
- 🛡️ **Privacy-first** — credentials are masked in logs

### 🔒 Privacy & security commitment

All **20 common code vulnerabilities** have been audited and mitigated. See [SECURITY.md](./SECURITY.md) for details.

| Data | Where | Encrypted? |
|------|-------|------------|
| **Apple ID password** | MongoDB | ✅ **AES-256-GCM** with random 32-byte key |
| Apple ID email | MongoDB | ❌ Plain (needed for debug) |
| 2FA code | RAM only (used once then forgotten) | N/A |
| App Store cookies | `tmp/users/<chatID>/.ipatool/cookies` | ✅ Encrypted (ipatool FileBackend) |
| Apple keychain | `tmp/users/<chatID>/.ipatool/keychain` | ✅ Encrypted with passphrase |
| ipatool error logs | `tmp/users/<chatID>/ipatool.log` | ✅ Passwords masked as `***` |
| IPA files | `tmp/downloads/<uuid>/` | ❌ Plain — deleted right after upload |

**This bot COMMITS to:**
- ✅ NEVER store plaintext passwords
- ✅ NEVER log passwords or 2FA codes
- ✅ NEVER send credentials to 3rd parties — outbound HTTPS only to `*.apple.com`, `*.mzstatic.com`, your MongoDB, and Telegram
- ✅ Rate-limit against brute-force (5 login fails / 10 minutes)
- ✅ Path traversal protection on web endpoints
- ✅ SSRF protection (icon URL whitelist for Apple CDN)
- ✅ XSS protection (escape user input before HTML rendering)

**Recommended:**
- Use a secondary Apple ID to separate from your main account
- Self-host on your own server (instructions below)
- Generate your own `ENCRYPTION_KEY` — never reuse one from a repo

### 🚀 Bot commands

| Command | Description |
|---------|-------------|
| `/start` | Open main menu (welcome page with buttons) |
| `/login <email> <password>` | Quick login (bot auto-deletes your message for security) |
| `/get <link\|appID>` | Download latest IPA, skip the prompts |
| `/accounts` | Manage multiple Apple IDs — switch/remove |
| `/addaccount` | Add another Apple ID |
| `/logout` | Sign out the current account |
| `/clearall confirm` | Wipe all your data (DB + keychain) |
| `/getlink` | Create direct install link (reply to IPA file) |
| `/help` | Help |
| `/privacy` | Privacy policy |

**Admin commands:**

| Command | Description |
|---------|-------------|
| `/add <chatID>` | Grant Premium |
| `/rm <chatID>` | Remove Premium |
| `/listpre` | List Premium users |
| `/listuser` | List all users |
| `/check <chatID>` | View user info |

### 🛠 Setup (self-host)

#### 1. Requirements

- Go 1.21+
- MongoDB Atlas (free tier OK)
- Telegram Bot Token (from [@BotFather](https://t.me/BotFather))
- Telegram API ID + Hash (from [my.telegram.org](https://my.telegram.org))
- Patched ipatool binary (build from `majd-ipatool-src/` or use prebuilt)

#### 2. Clone & configure

```bash
git clone https://github.com/captajn/ipatool.git
cd ipatool
cp .env.example .env
```

Edit `.env`:

```env
BOT_TOKEN=your_telegram_bot_token
API_ID=your_telegram_api_id
API_HASH=your_telegram_api_hash
ADMIN_ID=your_telegram_user_id
MONGODB_URI=mongodb+srv://user:pass@cluster.mongodb.net/?appName=botipa
ENCRYPTION_KEY=<generate_with_openssl_rand_-base64_32>
WEB_PORT=7098
HOST_URL=https://your-domain.com
DOWNLOAD_LIMIT_NORMAL=2048
DOWNLOAD_LIMIT_PREMIUM=4096
COOLDOWN_DURATION=15
SESSION_TIMEOUT=5
```

**Generate `ENCRYPTION_KEY`** (32 random bytes for AES-256 password encryption in DB):

```bash
# Linux / macOS
openssl rand -base64 32

# Windows PowerShell
[Convert]::ToBase64String((1..32 | %{Get-Random -Maximum 256}))
```

⚠️ **Warning:** Changing `ENCRYPTION_KEY` after deployment = **all stored passwords become unrecoverable** (users must re-login).

#### 3. Build patched ipatool

```bash
# Clone original ipatool source
git clone https://github.com/majd/ipatool.git majd-ipatool-src
cd majd-ipatool-src

# Apply patches: FileBackend only + fix interactiveKey
# (See PATCHES.md or copy from /e/Code/majd-ipatool-src/)

# Build
GOOS=linux GOARCH=amd64 go build -o ../ipatool .          # Linux
GOOS=windows GOARCH=amd64 go build -o ../ipatool.exe .    # Windows

# Place into PATH
sudo mv ../ipatool /usr/local/bin/                        # Linux
# or copy ipatool.exe to %PATH% on Windows
```

#### 4. Run

```bash
go build -o ipa-downloader-bot .
./ipa-downloader-bot
```

Output:
```
📦 Connected to MongoDB
🌍 Web Server starting on port 7098
🚀 MTProto connected
🤖 Bot started as your_bot_username
🚀 Bot is running. Press Ctrl+C to stop.
```

### 🧪 Tests

```bash
go vet ./...
go build ./...
go test ./...
```

### 📂 Project structure

```
.
├── main.go                    # Entry point
├── bot/                       # Telegram bot logic
│   ├── bot.go                 # Bot init + helpers (SafeSend, SafeEdit, SafeDelete)
│   ├── commands.go            # Command handlers + /start UI
│   ├── callbacks.go           # Callback (inline button) handlers
│   ├── login.go               # Login flow + /login command
│   ├── accounts.go            # Multi-account: /accounts, switch, remove
│   ├── download.go            # Download IPA + progress bar
│   └── admin.go               # Admin commands
├── pkg/
│   ├── ipatool/               # ipatool CLI wrapper
│   │   └── ipatool.go         # Per-user lock, error categorize, mask credentials
│   ├── itunes/                # iTunes API multi-region lookup
│   └── mtproto/               # MTProto for files > 50MB upload
├── db/mongo.go                # User schema (multi-account support)
├── config/config.go           # Load .env
├── server/                    # Web server for /i/<file> redirect
├── cmd/
│   └── reset/main.go          # Tool to wipe a user's data (test)
└── tmp/                       # Runtime (gitignored)
    ├── users/<chatID>/        # Per-user keychain + log
    └── downloads/<uuid>/      # Temp IPA (auto-cleanup)
```

### 🐛 Bug reports

- Issues: https://github.com/captajn/ipatool/issues
- ipatool binary error? See `tmp/users/<chatID>/ipatool.log`
- Bot error? Check stdout (credentials are masked)

### 📜 License

- Bot code: MIT
- Original ipatool: MIT (https://github.com/majd/ipatool)
