package ipatool

import (
	"bytes"
	"context"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

var (
	rePath    = regexp.MustCompile(`output="?([^"\s]+\.ipa)"?`)
	reVer     = regexp.MustCompile(`version=([^\s]+)`)
	reBracket = regexp.MustCompile(`\(([^\)]+)\)`)
	reError   = regexp.MustCompile(`"CustomerMessage":"([^"]+)"`)
	reErrAttr = regexp.MustCompile(`error="([^"]+)"`)
	reClean   = regexp.MustCompile(`^\d{1,2}:\d{2}[APM]{2}\s+[A-Z]+\s+`)
	reANSI    = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
)

// ErrorCategory phân loại lỗi từ ipatool/Apple để bot biết cách xử lý.
type ErrorCategory int

const (
	CatUnknown ErrorCategory = iota
	CatSuccess
	CatNeedPurchase // chưa "Nhận" / chưa mua / 5002 / STDQ / not purchased
	CatRegion       // app không có ở vùng của Apple ID
	Cat2FA          // cần mã 2FA
	CatAuth         // sai mật khẩu / session hết hạn
	CatKeychain     // lỗi truy cập keychain
	CatNetwork      // timeout / connection refused
	CatOther
)

type Client struct {
	BaseDir string

	mu       sync.Mutex
	userLock sync.Map // map[int64]*sync.Mutex — đảm bảo 1 lệnh ipatool / 1 user / 1 thời điểm
}

func NewClient(baseDir string) *Client {
	return &Client{BaseDir: baseDir}
}

// userMAC sinh MAC address giả deterministic từ userID.
// Format chuẩn: 12 hex digits có dấu ":". Bit "locally administered" = 1
// (byte đầu OR 0x02) để không trùng MAC thật của vendor.
//
// Mục đích: mỗi Telegram user có GUID riêng → Apple không thấy "1 device
// login nhiều account khác nhau" → tránh anti-fraud block toàn IP.
func userMAC(userID int64) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(fmt.Sprintf("ipa-bot-mac-%d", userID)))
	sum := h.Sum64()
	b := make([]byte, 6)
	for i := 0; i < 6; i++ {
		b[i] = byte(sum >> (i * 8))
	}
	b[0] = (b[0] | 0x02) & 0xfe // locally administered, unicast
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X", b[0], b[1], b[2], b[3], b[4], b[5])
}

// maskSensitiveArgs ẩn password / auth-code khi ghi log để bảo vệ privacy.
// Email được giữ nguyên (cần để debug). Password và 2FA code thay bằng "***".
func maskSensitiveArgs(args []string) string {
	masked := make([]string, len(args))
	copy(masked, args)
	for i := 0; i < len(masked); i++ {
		switch masked[i] {
		case "-p", "--password", "--auth-code", "--keychain-passphrase":
			if i+1 < len(masked) {
				masked[i+1] = "***"
			}
		}
	}
	return strings.Join(masked, " ")
}

// lockFor trả về mutex riêng của từng user, tạo nếu chưa có.
func (c *Client) lockFor(userID int64) *sync.Mutex {
	if v, ok := c.userLock.Load(userID); ok {
		return v.(*sync.Mutex)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.userLock.Load(userID); ok {
		return v.(*sync.Mutex)
	}
	m := &sync.Mutex{}
	c.userLock.Store(userID, m)
	return m
}

func (c *Client) getUserHome(userID int64) string {
	return filepath.Join(c.BaseDir, "users", fmt.Sprintf("%d", userID))
}

// userLogPath là nơi ghi tất cả stdout/stderr của ipatool cho 1 user (append).
func (c *Client) userLogPath(userID int64) string {
	return filepath.Join(c.getUserHome(userID), "ipatool.log")
}

func (c *Client) runCommand(ctx context.Context, userID int64, args ...string) (string, string, error) {
	// Lock per-user: đảm bảo chỉ 1 lệnh ipatool chạy tại 1 thời điểm cho 1 user
	// (tránh file keychain bị corrupt khi user spam click)
	ulock := c.lockFor(userID)
	ulock.Lock()
	defer ulock.Unlock()

	homeDir := c.getUserHome(userID)
	if err := os.MkdirAll(homeDir, 0755); err != nil {
		return "", "", err
	}

	// Log rotation: nếu file > 1MB → truncate (xóa nội dung cũ)
	logPath := c.userLogPath(userID)
	if info, err := os.Stat(logPath); err == nil && info.Size() > 1024*1024 {
		os.Truncate(logPath, 0)
	}

	// Mở file log per-user để ghi song song stdout/stderr (debug khi cần)
	// QUAN TRỌNG: Mask credentials trước khi log để bảo vệ privacy.
	logFile, _ := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if logFile != nil {
		defer logFile.Close()
		fmt.Fprintf(logFile, "\n===== [%s] ipatool %s =====\n", time.Now().Format(time.RFC3339), maskSensitiveArgs(args))
	}

	// Global flags: passphrase cho FileBackend + non-interactive
	fullArgs := append([]string{"--keychain-passphrase", "ipa-downloader-bot", "--non-interactive"}, args...)
	cmd := exec.CommandContext(ctx, "ipatool", fullArgs...)

	// HOME per-user → ipatool lưu file keychain (~/.ipatool/) riêng từng user.
	// Không cần DBus / mock keyring nữa vì ipatool đã được patch chỉ dùng FileBackend.
	// Trên Windows ipatool đọc HOMEDRIVE+HOMEPATH (không phải HOME), nên set cả hai.
	//
	// IPATOOL_MAC_OVERRIDE: GUID giả per-user. Mỗi Telegram user có MAC riêng
	// → Apple thấy mỗi user là 1 device riêng → tránh anti-fraud block.
	cmd.Env = append(os.Environ(),
		"HOME="+homeDir,
		"XDG_DATA_HOME="+filepath.Join(homeDir, ".local", "share"),
		"IPATOOL_SKIP_VERSION_CHECK=1",
		"IPATOOL_MAC_OVERRIDE="+userMAC(userID),
	)
	if runtime.GOOS == "windows" {
		drive := filepath.VolumeName(homeDir)             // VD: "E:"
		path := strings.TrimPrefix(homeDir, drive)        // VD: "/Code/ipatool/tmp/users/<id>"
		cmd.Env = append(cmd.Env,
			"HOMEDRIVE="+drive,
			"HOMEPATH="+filepath.FromSlash(path),
			"USERPROFILE="+homeDir,
		)
	}

	// Capture stdout/stderr vào buffer + ghi song song ra log file
	var stdout, stderr bytes.Buffer
	if logFile != nil {
		cmd.Stdout = io.MultiWriter(&stdout, logFile)
		cmd.Stderr = io.MultiWriter(&stderr, logFile)
	} else {
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
	}

	err := cmd.Run()
	if err != nil && logFile != nil {
		fmt.Fprintf(logFile, "----- exit: %v -----\n", err)
	}

	return stdout.String(), stderr.String(), err
}


func (c *Client) Login(userID int64, email, password, authCode string) error {
	_, _, err := c.LoginWithResult(userID, email, password, authCode)
	return err
}

func (c *Client) LoginWithResult(userID int64, email, password, authCode string) (string, string, error) {
	args := []string{"auth", "login", "-e", email, "-p", password}
	if authCode != "" {
		args = append(args, "--auth-code", authCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	return c.runCommand(ctx, userID, args...)
}

func (c *Client) AuthInfo(userID int64) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stdout, stderr, err := c.runCommand(ctx, userID, "auth", "info")
	if err != nil {
		return "", fmt.Errorf("%s: %v", stderr, err)
	}
	return stdout, nil
}

// Purchase thử "Nhận" app (free) hoặc xác nhận đã mua. Idempotent.
func (c *Client) Purchase(userID int64, bundleID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	stdout, stderr, err := c.runCommand(ctx, userID, "purchase", "-b", bundleID)
	combined := stdout + stderr
	if err != nil {
		lc := strings.ToLower(combined)
		if strings.Contains(lc, "already purchased") || strings.Contains(lc, "already owned") {
			return nil
		}
		return fmt.Errorf("%s: %v", combined, err)
	}
	return nil
}

// PurchaseByAppID dùng khi chỉ có appID (sẽ thử lookup bundleID qua AppInfo trước khi gọi Purchase).
// Trả về true nếu đã purchase được hoặc đã sở hữu.
func (c *Client) PurchaseByBundleID(userID int64, bundleID string) error {
	return c.Purchase(userID, bundleID)
}

func (c *Client) Download(userID int64, appID string, appVerId string, output string) (string, string, error) {
	// Ensure output path is absolute
	absOutput, _ := filepath.Abs(output)

	args := []string{"download", "-i", appID, "-o", absOutput, "--purchase"}
	if appVerId != "" {
		args = append(args, "--external-version-id", appVerId)
	}
	// Use a longer timeout for download
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	stdout, stderr, err := c.runCommand(ctx, userID, args...)
	if err != nil {
		return "", "", fmt.Errorf("%s: %v", stderr, err)
	}

	combined := stdout + stderr

	// 1. Extract actual output path from logs
	actualPath := ""
	matches := rePath.FindStringSubmatch(combined)
	if len(matches) > 1 {
		actualPath = matches[1]
		if !filepath.IsAbs(actualPath) {
			actualPath = filepath.Join(absOutput, filepath.Base(actualPath))
		}
	}

	// Fallback: Scan the output directory for any .ipa file
	if actualPath == "" || !strings.HasSuffix(actualPath, ".ipa") {
		files, _ := os.ReadDir(absOutput)
		for _, f := range files {
			if !f.IsDir() && strings.HasSuffix(f.Name(), ".ipa") {
				actualPath = filepath.Join(absOutput, f.Name())
				break
			}
		}
	}

	if actualPath == "" {
		return "", "", fmt.Errorf("không tìm thấy file IPA trong thư mục đầu ra")
	}

	// Ensure absolute path
	if !filepath.IsAbs(actualPath) {
		actualPath, _ = filepath.Abs(actualPath)
	}

	// 2. Extract version
	version := ""

	if actualPath != "" {
		base := filepath.Base(actualPath)
		base = strings.TrimSuffix(base, ".ipa")
		parts := strings.Split(base, "_")

		// Look for the part that looks like a version (contains dots and is not just letters)
		for i := len(parts) - 1; i >= 0; i-- {
			p := parts[i]
			if strings.Contains(p, ".") {
				// Avoid bundleID (usually parts[0])
				if i > 0 {
					version = p
					break
				}
			}
		}

		// Fallback to the 3rd part or last part if still empty
		if version == "" {
			if len(parts) >= 3 {
				version = parts[2]
			} else if len(parts) >= 2 {
				version = parts[1]
			}
		}
	}

	// Fallback to version=... in logs
	if version == "" {
		matches = reVer.FindStringSubmatch(combined)
		if len(matches) > 1 {
			version = strings.Trim(matches[1], "\"")
		}
	}

	// Second fallback: (...) in logs
	if version == "" {
		matches = reBracket.FindStringSubmatch(combined)
		if len(matches) > 1 {
			version = matches[1]
		}
	}

	return version, actualPath, nil
}

func (c *Client) Search(userID int64, term string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stdout, stderr, err := c.runCommand(ctx, userID, "search", term, "--limit", "1", "--format", "json")
	if err != nil {
		return "", fmt.Errorf("%s: %v", stderr, err)
	}
	return stdout, nil
}

// Categorize phân tích output (stdout+stderr) của ipatool và trả về category + thông báo VN.
func (c *Client) Categorize(output string) (ErrorCategory, string) {
	if output == "" {
		return CatUnknown, "Lỗi không xác định"
	}
	clean := reANSI.ReplaceAllString(output, "")
	low := strings.ToLower(clean)

	// 2FA detection
	if strings.Contains(low, "auth-code") ||
		strings.Contains(low, "2fa") ||
		strings.Contains(low, "verification code") ||
		strings.Contains(low, "6-digit") ||
		strings.Contains(low, "configurator_message") {
		return Cat2FA, "🔐 <b>Cần mã xác thực 2FA.</b> Vui lòng nhập mã 6 chữ số đã gửi tới thiết bị Apple."
	}

	// Auth errors (wrong password, etc.)
	if strings.Contains(low, "your apple id or password was incorrect") ||
		strings.Contains(low, "auth code is missing") ||
		strings.Contains(low, "invalid credentials") {
		return CatAuth, "❌ <b>Sai Apple ID hoặc mật khẩu.</b> Vui lòng kiểm tra lại."
	}

	// Apple rate limit
	if strings.Contains(low, "rate limit has been exceeded") || strings.Contains(low, "rate limit") {
		return CatNetwork, "🚦 <b>Apple đang giới hạn tốc độ truy cập.</b>\nVui lòng đợi vài phút rồi thử lại. ⏳"
	}

	// Account locked
	if strings.Contains(low, "account is disabled") || strings.Contains(low, "account has been locked") {
		return CatAuth, "🔒 <b>Tài khoản Apple ID đã bị khóa.</b>\nVui lòng đăng nhập <a href=\"https://iforgot.apple.com\">iforgot.apple.com</a> để mở khóa, hoặc dùng tài khoản khác."
	}

	// Region restriction
	if strings.Contains(low, "this item is not available in the u.s. store") ||
		strings.Contains(low, "not available in your country") ||
		strings.Contains(low, "this item is no longer available") ||
		strings.Contains(low, "not available in this") {
		return CatRegion, "🌏 <b>Ứng dụng không có sẵn ở vùng của Apple ID này.</b>\nVui lòng dùng Apple ID đúng vùng (ví dụ JP cho app Nhật)."
	}

	// CustomerMessage
	if m := reError.FindStringSubmatch(clean); len(m) > 1 {
		msg := m[1]
		mlow := strings.ToLower(msg)
		if strings.Contains(clean, "5002") || strings.Contains(mlow, "unknown error") {
			return CatNeedPurchase, c.getPurchaseRequiredMessage()
		}
		if strings.Contains(mlow, "not available") {
			return CatRegion, "🌏 <b>" + msg + "</b>"
		}
		return CatOther, msg
	}

	// error="..."
	if m := reErrAttr.FindStringSubmatch(clean); len(m) > 1 {
		errAttr := m[1]
		ealow := strings.ToLower(errAttr)
		if strings.Contains(ealow, "not purchased") ||
			strings.Contains(ealow, "not found") ||
			strings.Contains(ealow, "responseerrordomain error 2") {
			return CatNeedPurchase, c.getPurchaseRequiredMessage()
		}
		return CatOther, errAttr
	}

	// Temporarily unavailable (Apple-side issue, NOT a purchase problem)
	if strings.Contains(low, "temporarily unavailable") {
		return CatOther, "⏳ <b>Ứng dụng tạm thời không khả dụng trên Apple Store.</b>\n\n" +
			"Nguyên nhân có thể:\n" +
			"• Apple đang bảo trì hệ thống\n" +
			"• Ứng dụng đang được cập nhật hoặc đã bị gỡ tạm thời\n" +
			"• Vùng Apple ID không khớp với vùng phát hành app\n\n" +
			"Vui lòng thử lại sau vài phút. 🔄"
	}

	// Common purchase-required patterns
	if strings.Contains(low, "not purchased") ||
		strings.Contains(low, "not owned") ||
		strings.Contains(low, "purchase is required") ||
		strings.Contains(low, "purchase of this item is not currently available") ||
		strings.Contains(low, "failed to purchase item") ||
		strings.Contains(low, "stdq") ||
		(strings.Contains(low, "failed to download") && strings.Contains(low, "status 1")) {
		return CatNeedPurchase, c.getPurchaseRequiredMessage()
	}

	// Keychain errors
	if strings.Contains(low, "keychain") && (strings.Contains(low, "lock") || strings.Contains(low, "passphrase") || strings.Contains(low, "open")) {
		return CatKeychain, "❌ <b>Lỗi Keychain:</b> Hệ thống không thể truy cập kho lưu trữ bảo mật.\nVui lòng thử /start để reset phiên làm việc."
	}

	// Network
	if strings.Contains(low, "i/o timeout") || strings.Contains(low, "connection refused") || strings.Contains(low, "no such host") {
		return CatNetwork, "🌐 <b>Lỗi mạng:</b> Không kết nối được tới Apple. Vui lòng thử lại sau."
	}

	// Fallback: last meaningful line
	lines := strings.Split(strings.TrimSpace(clean), "\n")
	if len(lines) > 0 {
		lastLine := lines[len(lines)-1]
		cleanLine := reClean.ReplaceAllString(lastLine, "")
		if strings.Contains(cleanLine, "exit status 1") && len(lines) > 1 {
			cleanLine = reClean.ReplaceAllString(lines[len(lines)-2], "")
		}
		return CatOther, cleanLine
	}
	return CatOther, clean
}

// FormatError giữ tương thích ngược, gọi Categorize và trả message.
func (c *Client) FormatError(output string) string {
	_, msg := c.Categorize(output)
	return msg
}

func (c *Client) getPurchaseRequiredMessage() string {
	return "❌ <b>Ứng dụng chưa có trong tài khoản hoặc là ứng dụng trả phí.</b>\n\n" +
		"Hệ thống không thể tự động 'Nhận' ứng dụng này (đặc biệt là các ứng dụng mất phí). Vui lòng thực hiện các bước sau:\n" +
		"1. Dùng Apple ID này đăng nhập vào App Store trên một thiết bị Apple thật (iPhone/iPad).\n" +
		"2. Tìm ứng dụng này. Nếu là app miễn phí hãy nhấn <b>'Nhận'</b>, nếu là app trả phí hãy tiến hành <b>Mua</b>.\n" +
		"3. Sau khi ứng dụng đã nằm trong lịch sử mua hàng, hãy quay lại đây và thử tải lại. 🔄"
}

func (c *Client) ResetSession(userID int64) error {
	homeDir := c.getUserHome(userID)
	ipaToolDir := filepath.Join(homeDir, ".ipatool")
	return os.RemoveAll(ipaToolDir)
}

func (c *Client) CleanupUserDirectories(maxAge time.Duration) {
	usersDir := filepath.Join(c.BaseDir, "users")
	files, err := os.ReadDir(usersDir)
	if err != nil {
		return
	}

	now := time.Now()
	for _, f := range files {
		if f.IsDir() {
			info, err := f.Info()
			if err != nil {
				continue
			}
			if now.Sub(info.ModTime()) > maxAge {
				path := filepath.Join(usersDir, f.Name())
				os.RemoveAll(path)
				log.Printf("🧹 [Dọn dẹp] Đã xóa thư mục người dùng cũ: %s", f.Name())
			}
		}
	}
}


