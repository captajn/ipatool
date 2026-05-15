# Patches applied to `majd/ipatool`

Bot dùng `ipatool` CLI gốc của [@majd](https://github.com/majd/ipatool) làm engine. Để chạy được dưới dạng Telegram bot trên server (headless, multi-user), source ipatool gốc cần một số patch nhỏ.

This bot uses [@majd's `ipatool`](https://github.com/majd/ipatool) CLI as its engine. To run as a Telegram bot on a headless server with multiple users, the upstream source needs a few small patches.

## Setup

```bash
git clone https://github.com/majd/ipatool.git majd-ipatool-src
cd majd-ipatool-src
git checkout v2.3.0   # hoặc latest tag
```

Apply 2 patches sau:

---

## Patch 1: `cmd/common.go` — FileBackend only

**Lý do / Reason:** Bot chạy headless (Linux server, hoặc Windows không có user logged in), không có GUI để mở Keychain/WinCred/SecretService. Bắt buộc dùng `FileBackend` (file `.ipatool/keychain` mã hoá bằng passphrase).

**Why:** Bot runs headless (Linux server, or Windows without a logged-in user), no GUI to unlock Keychain/WinCred/SecretService. Must use `FileBackend` only (encrypted `.ipatool/keychain` file, unlocked via passphrase).

Thay thế hàm `newKeychain` trong file `cmd/common.go`:

Replace `newKeychain` function in `cmd/common.go`:

```go
// newKeychain returns a new keychain instance.
// Only FileBackend is allowed so ipatool can run headless (no GUI, no DBus)
// in any environment. Credentials are stored as an encrypted file under
// $HOME/.ipatool/ and unlocked with --keychain-passphrase.
func newKeychain(machine machine.Machine, logger log.Logger, interactive bool) keychain.Keychain {
	ring := util.Must(keyring.Open(keyring.Config{
		AllowedBackends: []keyring.BackendType{
			keyring.FileBackend,
		},
		ServiceName: KeychainServiceName,
		FileDir:     filepath.Join(machine.HomeDirectory(), ConfigDirectoryName),
		FilePasswordFunc: func(s string) (string, error) {
			if keychainPassphrase == "" && !interactive {
				return "", errors.New("keychain passphrase is required when not running in interactive mode; use the \"--keychain-passphrase\" flag")
			}

			if keychainPassphrase != "" {
				return keychainPassphrase, nil
			}

			path := strings.Split(s, " unlock ")[1]
			logger.Log().Msgf("enter passphrase to unlock %s (this is separate from your Apple ID password): ", path)
			bytes, err := term.ReadPassword(int(os.Stdin.Fd()))
			if err != nil {
				return "", fmt.Errorf("failed to read password: %w", err)
			}

			password := string(bytes)
			password = strings.Trim(password, "\n")
			password = strings.Trim(password, "\r")

			return password, nil
		},
	}))

	return keychain.New(keychain.Args{Keyring: ring})
}
```

---

## Patch 2: `cmd/auth.go` + `cmd/download.go` — Fix context key panic

**Lý do / Reason:** Source gốc dùng `cmd.Context().Value("interactive")` (string key) trong khi `root.go` lưu với `interactiveKey` (typed key `contextKey("interactive")`). Type mismatch → `Value()` trả `nil` → type assertion `.(bool)` panic. Khi chạy với `--non-interactive` qua bot, login bị crash.

**Why:** Upstream uses `cmd.Context().Value("interactive")` (string key) while `root.go` stores it as `interactiveKey` (typed key `contextKey("interactive")`). Type mismatch → `Value()` returns `nil` → unsafe `.(bool)` assertion panics. When running with `--non-interactive` from the bot, login crashes.

### `cmd/auth.go` (line ~50)

```go
// Trước / Before:
interactive := cmd.Context().Value("interactive").(bool)

// Sau / After:
interactive, _ := cmd.Context().Value(interactiveKey).(bool)
```

### `cmd/download.go` (line ~84)

```go
// Trước / Before:
interactive, _ := cmd.Context().Value("interactive").(bool)

// Sau / After:
interactive, _ := cmd.Context().Value(interactiveKey).(bool)
```

---

## Patch 3: `pkg/appstore/appstore_bag.go` — Retry on transient Apple errors

**Lý do / Reason:** Apple đôi khi trả HTML error page hoặc rate-limit text thay vì plist XML. Retry 3 lần với backoff sẽ giúp bot không fail khi user vừa connect.

**Why:** Apple sometimes returns HTML error pages or rate-limit text instead of XML plist. 3-attempt retry with backoff prevents the bot from failing on transient errors.

Thay thế hàm `Bag` trong `pkg/appstore/appstore_bag.go`:

Replace `Bag` function:

```go
func (t *appstore) Bag(input BagInput) (BagOutput, error) {
	macAddr, err := t.machine.MacAddress()
	if err != nil {
		return BagOutput{}, fmt.Errorf("failed to get mac address: %w", err)
	}

	guid := strings.ReplaceAll(strings.ToUpper(macAddr), ":", "")
	req := t.bagRequest(guid)

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
		}

		res, err := t.bagClient.Send(req)
		if err != nil {
			lastErr = fmt.Errorf("attempt %d: %w", attempt+1, err)
			continue
		}

		if res.StatusCode != gohttp.StatusOK {
			lastErr = fmt.Errorf("attempt %d: received unexpected status code: %d", attempt+1, res.StatusCode)
			continue
		}

		if res.Data.URLBag.AuthEndpoint == "" {
			lastErr = fmt.Errorf("attempt %d: bag response missing auth endpoint", attempt+1)
			continue
		}

		return BagOutput{
			AuthEndpoint: res.Data.URLBag.AuthEndpoint,
		}, nil
	}

	return BagOutput{}, fmt.Errorf("failed to fetch bag after 3 attempts: %w", lastErr)
}
```

Add `"time"` import.

---

## Patch 4: `pkg/appstore/appstore_login.go` — Retry on plist parse errors

Thêm retry cho login request:

```go
for attempt := 1; retry && attempt <= 4; attempt++ {
	request := t.loginRequest(email, password, authCode, guid, endpoint, attempt)
	request.URL, _ = util.IfEmpty(redirect, request.URL), ""

	// Retry transient parse errors (Apple sometimes returns HTML instead of plist)
	var sendErr error
	for sendTry := 0; sendTry < 3; sendTry++ {
		res, sendErr = t.loginClient.Send(request)
		if sendErr == nil {
			break
		}
		if strings.Contains(sendErr.Error(), "unmarshal") || strings.Contains(sendErr.Error(), "plist") {
			time.Sleep(time.Duration(sendTry+1) * 2 * time.Second)
			continue
		}
		break
	}
	if sendErr != nil {
		return Account{}, fmt.Errorf("request failed: %w", sendErr)
	}

	if retry, redirect, err = t.parseLoginResponse(&res, attempt, authCode); err != nil {
		return Account{}, err
	}
}
```

Add `"time"` import.

---

## Patch 5: `pkg/http/client.go` — Debug raw body on plist errors

Khi plist parse fail, log raw body để debug. Hữu ích để biết Apple đang trả gì (rate limit page, captcha, v.v.).

Log raw body when plist parsing fails. Useful to know what Apple is returning (rate limit page, captcha, etc.).

```go
_, err = plist.Unmarshal(normalizedBody, &data)
if err != nil {
	// Log raw body for debugging plist parse failures
	snippet := string(body)
	if len(snippet) > 500 {
		snippet = snippet[:500] + "...(truncated)"
	}
	return Result[R]{}, fmt.Errorf("failed to unmarshal xml: %w\nraw_body_preview: %s", err, snippet)
}
```

---

## Patch 6: `vendor/.../client_builder_no_cgo.go` — Build tag fix

**Lý do / Reason:** Go 1.21+ build fail vì `client_builder_no_cgo.go` reference biến chỉ tồn tại khi CGO_ENABLED=1.

**Why:** Go 1.21+ build fails because `client_builder_no_cgo.go` references variables only present with CGO_ENABLED=1.

Thêm build tag `//go:build ignore` đầu file `vendor/github.com/1password/onepassword-sdk-go/client_builder_no_cgo.go`:

Add `//go:build ignore` build tag at top of `vendor/github.com/1password/onepassword-sdk-go/client_builder_no_cgo.go`:

```go
//go:build ignore

package onepassword
// ... rest of file
```

---

## Build

```bash
# Linux
GOOS=linux GOARCH=amd64 go build -o ipatool .

# Windows
GOOS=windows GOARCH=amd64 go build -o ipatool.exe .

# macOS
GOOS=darwin GOARCH=amd64 go build -o ipatool .
```

Đặt binary vào PATH (`/usr/local/bin/ipatool` trên Linux, hoặc thư mục có trong `%PATH%` trên Windows).

Place binary in PATH (`/usr/local/bin/ipatool` on Linux, or directory in `%PATH%` on Windows).

## Verify

```bash
ipatool --version
ipatool --help
```

Should print `dev` or version number — không có error là OK.
