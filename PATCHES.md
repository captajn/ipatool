# 🩹 Patches applied to `majd/ipatool`

Bot dùng [`majd/ipatool`](https://github.com/majd/ipatool) làm engine. Để chạy được dưới dạng Telegram bot trên server (headless, multi-user), source ipatool gốc cần một số patch nhỏ.

This bot uses [`majd/ipatool`](https://github.com/majd/ipatool) as its engine. To run as a headless multi-user Telegram bot, the upstream source needs a few small patches.

---

## ⚡ Quick build (auto-apply patches)

```bash
# Build cho OS hiện tại (output: ./ipatool[.exe])
./scripts/build-ipatool.sh

# Cross-compile
./scripts/build-ipatool.sh linux amd64
./scripts/build-ipatool.sh windows amd64
./scripts/build-ipatool.sh darwin amd64
```

Script sẽ:
1. Clone `majd/ipatool@v2.3.0` vào `.ipatool-build/`
2. Apply `scripts/ipatool.patch` (tất cả patches dưới đây)
3. Build binary

Sau đó copy binary vào `$PATH` để bot tìm thấy.

---

## 📋 Danh sách patches

File `scripts/ipatool.patch` chứa 6 thay đổi sau:

### 1. `cmd/common.go` — FileBackend only

**Why:** Bot chạy headless, không có GUI để mở Keychain/WinCred/SecretService. Bắt buộc dùng `FileBackend` (file `.ipatool/keychain` mã hoá bằng passphrase).

Bỏ tất cả `keyring.KeychainBackend`, `keyring.WinCredBackend`, `keyring.SecretServiceBackend`. Chỉ giữ `keyring.FileBackend`.

### 2. `cmd/auth.go:50` — Fix context key panic

**Bug:** Upstream dùng `cmd.Context().Value("interactive")` (string key) trong khi `root.go` lưu với `interactiveKey` (typed key `contextKey("interactive")`). Type mismatch → `Value()` trả `nil` → unsafe `.(bool)` panic ngay khi gọi `--non-interactive`.

```go
- interactive := cmd.Context().Value("interactive").(bool)
+ interactive, _ := cmd.Context().Value(interactiveKey).(bool)
```

### 3. `cmd/download.go:84` — Same fix

Cùng bug như #2 nhưng ở download command (silent fail thay vì panic).

```go
- interactive, _ := cmd.Context().Value("interactive").(bool)
+ interactive, _ := cmd.Context().Value(interactiveKey).(bool)
```

### 4. `pkg/appstore/appstore_bag.go` — Retry on transient errors

**Why:** Apple đôi khi trả HTML error page hoặc rate-limit text thay vì plist XML khi gọi bag endpoint. 3-attempt retry với backoff giúp bot không fail lúc user vừa kết nối.

Thêm vòng retry 3 lần, mỗi lần đợi `attempt * 2s`.

### 5. `pkg/appstore/appstore_login.go` — Retry login on plist parse errors

**Why:** Tương tự #4 nhưng cho login flow. Khi Apple rate limit, response không phải plist → retry sau 2-4-6s.

### 6. `pkg/http/client.go` — Debug raw body on plist failures

**Why:** Khi plist parse fail, log raw body (truncated 500 chars) để biết Apple đang trả gì — rate limit page, captcha, HTML error...

```go
+ snippet := string(body)
+ if len(snippet) > 500 {
+     snippet = snippet[:500] + "...(truncated)"
+ }
+ return Result[R]{}, fmt.Errorf("failed to unmarshal xml: %w\nraw_body_preview: %s", err, snippet)
```

---

## 🔍 View patch file

Toàn bộ unified diff: [`scripts/ipatool.patch`](./scripts/ipatool.patch) (159 dòng).

Apply thủ công nếu không dùng script:

```bash
git clone --branch v2.3.0 https://github.com/majd/ipatool.git
cd ipatool
git apply ../path/to/scripts/ipatool.patch
go build -o ipatool .
```

---

## 🆙 Cập nhật khi có version ipatool mới

Khi upstream `majd/ipatool` ra version mới:

1. Test patches còn apply được không:
   ```bash
   IPATOOL_TAG=v2.4.0 ./scripts/build-ipatool.sh
   ```

2. Nếu fail → manually re-apply, regenerate patch:
   ```bash
   cd .ipatool-build
   # ... fix conflicts manually ...
   git diff cmd/auth.go cmd/common.go cmd/download.go pkg/... > ../scripts/ipatool.patch
   ```

3. Update default tag trong `scripts/build-ipatool.sh`.

---

## 📜 License

Patches dựa trên upstream MIT-licensed code, giữ nguyên license. Xem `LICENSE`.
