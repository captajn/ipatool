# 🩹 Patches applied to `majd/ipatool`

Bot dùng [`majd/ipatool@v2.3.0`](https://github.com/majd/ipatool/releases/tag/v2.3.0) làm engine. Để chạy được headless trên server, source gốc cần 5 thay đổi nhỏ — **tổng cộng ~56 dòng code thay đổi** (151 dòng patch bao gồm context lines).

This bot uses [`majd/ipatool@v2.3.0`](https://github.com/majd/ipatool/releases/tag/v2.3.0) as engine. To run headless on a server, the source needs 5 small changes — **~56 lines of actual code changes** (patch is 151 lines including diff context).

---

## ⚡ Auto-build

```bash
./scripts/build-ipatool.sh                    # current OS
./scripts/build-ipatool.sh linux amd64        # cross-compile
```

Script clones v2.3.0 → applies patch → builds binary. Hết.

---

## 📋 5 thay đổi (5 changes)

### 1. `cmd/common.go` — FileBackend only *(3 dòng)*

Bỏ `KeychainBackend` (macOS) + `SecretServiceBackend` (Linux DBus). Giữ `FileBackend` (encrypted file unlocked qua passphrase).

**Lý do:** Server không có GUI để mở keychain native.

```diff
- AllowedBackends: []keyring.BackendType{
-     keyring.KeychainBackend,
-     keyring.SecretServiceBackend,
-     keyring.FileBackend,
- },
+ AllowedBackends: []keyring.BackendType{
+     keyring.FileBackend,
+ },
```

### 2. `cmd/auth.go:50` — Fix panic *(1 dòng)*

Upstream dùng unsafe type assertion. Nếu context chưa set → panic.

```diff
- interactive := cmd.Context().Value("interactive").(bool)
+ interactive, _ := cmd.Context().Value("interactive").(bool)
```

### 3. `pkg/appstore/appstore_bag.go` — Retry + rate-limit fail-fast *(~25 dòng)*

Apple đôi khi trả HTML thay vì plist XML (transient). Retry 3 lần với backoff 2s/4s/6s. **Phát hiện rate-limit / account-disabled → dừng ngay**, không retry (tránh Apple block lâu hơn).

### 4. `pkg/appstore/appstore_login.go` — Same retry logic for login *(~25 dòng)*

Như #3 nhưng cho login endpoint.

### 5. `pkg/http/client.go` — Debug raw body *(~7 dòng)*

Khi plist parse fail, log 500 ký tự đầu của body. Giúp biết Apple đang trả gì (rate limit text, captcha, HTML, v.v.).

```diff
+ snippet := string(body)
+ if len(snippet) > 500 {
+     snippet = snippet[:500] + "...(truncated)"
+ }
+ return Result[R]{}, fmt.Errorf("failed to unmarshal xml: %w\nraw_body_preview: %s", err, snippet)
```

---

## 🔍 View full diff

[`scripts/ipatool.patch`](./scripts/ipatool.patch) — 151 dòng unified diff. Audit thoải mái.

```bash
# Apply thủ công nếu không dùng script
git clone --branch v2.3.0 https://github.com/majd/ipatool.git
cd ipatool
git apply /path/to/scripts/ipatool.patch
go build -o ipatool .
```

---

## 🆙 Khi upstream ra version mới

```bash
IPATOOL_TAG=v2.4.0 ./scripts/build-ipatool.sh
```

Nếu patch conflict → manually re-apply trong `.ipatool-build/`, regen:

```bash
cd .ipatool-build
git diff > ../scripts/ipatool.patch
```

---

## 📜 License

Patches dựa trên MIT-licensed upstream, giữ nguyên MIT.
