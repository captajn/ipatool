# 🔒 Security Audit & Hardening

This document tracks the security posture of the bot against the OWASP Top 10 + 20 common AI-generated code vulnerabilities. Tóm tắt kiểm thử bảo mật của bot theo OWASP Top 10 + 20 lỗ hổng phổ biến trong code do AI sinh.

## 📋 Audit checklist

| # | Vulnerability | Status | Mitigation |
|---|---------------|--------|------------|
| 1 | **Hardcoded Secrets** | ✅ Pass | Tất cả secret đến từ env vars (`.env`). `.gitignore` chặn `.env`. README dặn rõ. |
| 2 | **SQL/NoSQL Injection** | ✅ Pass | Mongo driver dùng `bson.M{}` parameterized values, không string concat. Keys hardcoded. |
| 3 | **XSS / HTML Injection** | ✅ Pass | Tất cả user-controlled string đi qua `html.EscapeString()` trước khi inject vào HTML message. Bao gồm `FirstName`, `AppleID`, `appName`. |
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
| 18 | **Missing Rate Limit** | ✅ Pass | `/login`: 5 / 10min. Download/actions: 30 / phút. Plus `CooldownDuration` per-user cho download (mặc định 15 phút). |
| 19 | **Race Condition** | ✅ Pass | `pkg/ipatool` dùng `sync.Map` of `*sync.Mutex` per-user — chỉ 1 lệnh ipatool / user / thời điểm (chống corrupt keychain khi spam). Crypto cipher safe-for-concurrent-use. |
| 20 | **Outdated Dependency** | ✅ Pass | Quét bằng `govulncheck` — **0 vulns trong code**, 1 vuln trong dep nhưng code không call (transitive). Recommend chạy `govulncheck` định kỳ. |

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
