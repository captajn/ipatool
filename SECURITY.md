# 🔒 Security Audit & Hardening

This document tracks the security posture of the bot against the OWASP Top 10 + 20 common AI-generated code vulnerabilities. Tóm tắt kiểm thử bảo mật của bot theo OWASP Top 10 + 20 lỗ hổng phổ biến trong code do AI sinh.

## 📋 Audit checklist

| # | Vulnerability | Status | Mitigation |
|---|---------------|--------|------------|
| 1 | **Hardcoded Secrets** | ✅ Pass | Tất cả secret đến từ env vars (`.env`). `.gitignore` chặn `.env`. README dặn rõ. |
| 2 | **SQL/NoSQL Injection** | ✅ Pass | Mongo driver dùng `bson.M{}` parameterized values, không string concat. Keys hardcoded. |
| 3 | **HTML Injection trong Telegram message** | ✅ Pass | Bot gửi tin nhắn `parse_mode=HTML`. User-controlled string (`FirstName`, `AppleID`, `appName`) đi qua `html.EscapeString()` trước khi inject. Note: KHÔNG có web frontend, KHÔNG có browser → đây không phải XSS truyền thống, chỉ format break / clickbait phòng ngừa. |
| 4 | **IDOR** | ✅ Pass | `chatID` luôn lấy từ Telegram message metadata (`msg.Chat.ID`), không từ user input. Telegram đảm bảo identity. |
| 5 | **Slopsquatting** | ✅ Pass | Tất cả deps trong `go.mod` từ orgs uy tín: `gin-gonic`, `gotd`, `mongo-driver`, `google`, `joho`, `nfnt`. Không có package lạ/typo. |
| 6 | **Brute Force** | ✅ Pass | `pkg/ratelimit`: per-user token bucket. `/login` giới hạn **5 attempts / 10 phút**. Login OK → reset bucket. |
| 7 | **Mass Assignment** | ✅ Pass | Tất cả `bson.M{}` keys hardcoded. User input chỉ vào values. Không endpoint nhận arbitrary fields. |
| 8 | **Insecure Deserialization** | ✅ Pass | Chỉ dùng JSON/BSON với struct schema cố định. Không có `pickle`, `yaml.UnmarshalUntyped`. |
| 9 | **SSRF** | ✅ Pass | Outbound HTTP duy nhất là `downloadAndResizeIcon` — đã giới hạn host vào allowlist `mzstatic.com` (Apple CDN), HTTPS only, timeout 10s. |
| 10 | **Path Traversal** | ✅ Pass | Web `/i/:filename` validate qua regex `^[a-zA-Z0-9._-]+$` + `filepath.Abs()` prefix check để chặn `..`. Static `/public/*` dùng Gin's `StaticFS` (built-in safe). |
| 11 | **CSRF** | ✅ Pass | Bot Telegram polling, không có session/cookie. Web server không có endpoint thay đổi state. |
| 12 | **Broken Access Control** | ✅ Pass | 5 admin commands đều check `msg.Chat.ID != b.Config.AdminID`. User chỉ access được data của chính họ qua chatID. |
| 13 | **Weak Password Hashing** | ✅ Pass | Password user **mã hoá AES-256-GCM** với key 32 bytes random từ env. Không lưu plaintext. Mỗi lần encrypt sinh nonce mới → cùng password ra ciphertext khác nhau. GCM auth tag chống tampering. |
| 14 | **JWT None Algorithm** | N/A | Không dùng JWT. |
| 15 | **CORS Misconfiguration** | ✅ Pass | Web server (Gin) default không expose CORS headers. `/i/<file>` chỉ redirect, `/public/<file>` static — không cần CORS. |
| 16 | **Unrestricted File Upload** | ✅ Pass | Bot **không nhận upload từ user**. Chỉ tải IPA từ Apple servers (`*.apple.com`). Filename validate khi serve qua web. |
| 17 | **Verbose Error Messages** | ✅ Pass | Error category-hoá qua `Categorize()` → user-friendly message. Stack traces chỉ vào stdout server, không gửi ra Telegram. Raw `err.Error()` bị truncate khi escape. |
| 18 | **Missing Rate Limit** | ✅ Pass | **Bot side:** `/login` 5/10min, action 30/phút, cooldown 15 phút per-user. **Web side:** 60 req/phút per-IP cho `/i/<file>` + `/public/<file>` (chống DDoS / scraper).  |
| 19 | **Race Condition** | ✅ Pass | `pkg/ipatool` dùng `sync.Map` of `*sync.Mutex` per-user — chỉ 1 lệnh ipatool / user / thời điểm (chống corrupt keychain khi spam). Crypto cipher safe-for-concurrent-use. |
| 20 | **Outdated Dependency** | ✅ Pass | Quét bằng `govulncheck` — **0 vulns trong code**, 1 vuln trong dep nhưng code không call (transitive). Recommend chạy `govulncheck` định kỳ. |

## 🏗️ Architecture

```
Telegram user
    │ message / button / command
    ▼
┌────────────────────────────────────────────┐
│ Telegram Bot API                           │ ← BotToken auth
│ (long polling)                             │
└────────────────────────────────────────────┘
    │
    ▼
┌────────────────────────────────────────────┐
│ Bot Go process (~3000 lines)               │
│  • Multi-account / encryption / rate limit │
│  • Per-user mutex                          │
│  • Per-user keychain dir                   │
└────────────────────────────────────────────┘
    │ exec subprocess               │ MTProto upload (file > 50MB)
    ▼                               ▼
ipatool CLI (patched)           api.telegram.org
    │
    ▼
*.apple.com  (auth, search, download)
```

Bot dùng `exec.Command("ipatool", args...)` với CLI flags chuẩn upstream (`auth login -e ... -p ... --auth-code ...`). Patch chỉ thay storage backend (FileBackend) + fix bugs + retry — **không thay đổi flags hay logic command**.

Tin nhắn Telegram dùng `parse_mode=HTML` (bot client render, không phải browser). Web server `/i/<file>` chỉ là 1 endpoint serve file IPA cho iPhone Safari cài — không có frontend, không có session, không có form.

---

## 🛡️ Privacy guarantees

### Cái gì được lưu

| Loại data | Vị trí | Encryption | Auto-delete |
|-----------|--------|------------|-------------|
| Apple ID password | MongoDB (`appleId.password`, `accounts[].password`) | ✅ AES-256-GCM | Khi user `/clearall` |
| Apple ID email | MongoDB (`appleId`) | ❌ Plain (cần để debug) | Khi user `/clearall` |
| Apple session cookies | `tmp/users/<chatID>/.ipatool/cookies` | ✅ Encrypted bởi ipatool keychain | Sau 24h không hoạt động hoặc `/clearall` |
| Apple keychain entry | `tmp/users/<chatID>/.ipatool/keychain` | ✅ Encrypted với passphrase cố định | Sau 24h hoặc `/clearall` |
| Mã 2FA | RAM only, gọi 1 lần rồi quên | N/A | Tức thì |
| File IPA | `tmp/downloads/<uuid>/` | ❌ Plain | Sau khi upload Telegram (defer cleanup) |

### Cái gì KHÔNG bao giờ lưu

- ❌ Plaintext password trong DB hoặc disk
- ❌ Mã 2FA dưới bất kỳ hình thức nào
- ❌ Nội dung của file IPA
- ❌ Log với password / 2FA code (đã mask `-p`, `--password`, `--auth-code` trong `pkg/ipatool/runCommand`)

### 🌐 Web server (`/i/<file>` + `/public/<file>`)

API ra ngoài duy nhất là HTTP server cho phép user cài IPA qua iPhone Safari. Bảo vệ:

| Layer | Cơ chế |
|-------|--------|
| **Path traversal** | Regex `^[a-zA-Z0-9._-]+$` + `filepath.Abs()` prefix check → chặn `../etc/passwd` |
| **Rate limit per-IP** | 60 req/phút/IP, trả `429 Too Many Requests` + `Retry-After: 60` |
| **Security headers** | `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, `Strict-Transport-Security`, strict `Content-Security-Policy` |
| **Slow-loris timeout** | `ReadTimeout: 15s`, `ReadHeaderTimeout: 5s`, `IdleTimeout: 30s` |
| **Auto cleanup** | File trong `public/` xóa sau 1h (cleanupStalePublicFiles) |
| **UUID filename** | Filename là `<uuid>_<safe-name>` (36 ký tự random) → không guess được |
| **Reverse proxy aware** | Lấy IP thật từ `X-Real-IP` / `X-Forwarded-For` khi sau nginx/cloudflare |

### Network egress

Bot chỉ gọi đến những domain sau, **không bên thứ 3 nào khác**:

- `*.apple.com` — auth, search, download metadata (qua `ipatool` CLI)
- `*.mzstatic.com` — App Store CDN (icon, package)
- `itunes.apple.com` — search/lookup app
- `mongodb.net` — DB của bạn (Atlas)
- `api.telegram.org` — Telegram Bot API
- DC's của Telegram qua MTProto (cho upload file > 50MB)

## 🧪 Verification

```bash
# Quét vulnerabilities
go install golang.org/x/vuln/cmd/govulncheck@latest
govulncheck ./...

# Run tests (crypto + ratelimit)
go test ./...

# Static analysis
go vet ./...

# Outdated deps (informational)
go list -m -u all
```

## 📝 Reporting security issues

If you find a security issue, please **DO NOT** open a public issue. Email or DM me directly.

Nếu bạn phát hiện lỗ hổng, **đừng open public issue**. Liên hệ qua DM/email.
