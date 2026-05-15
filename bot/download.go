package bot

import (
	"context"
	"fmt"
	"html"
	"image/jpeg"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"ipa-downloader-bot/pkg/ipatool"
	"ipa-downloader-bot/pkg/itunes"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/google/uuid"
	"github.com/nfnt/resize"
	"go.mongodb.org/mongo-driver/bson"
)

func (b *Bot) promptDownload(chatID int64) {
	ctx := context.Background()
	b.DB.UpdateUser(ctx, chatID, bson.M{"$set": bson.M{"loginStep": "awaiting_app_id"}})
	msg := tgbotapi.NewMessage(chatID, "Vui lòng gửi link App Store hoặc AppID: 🔗")
	msg.ParseMode = "HTML"
	sent, _ := b.SafeSend(msg)
	b.State.Store(fmt.Sprintf("prompt_%d", chatID), sent.MessageID)
}

func (b *Bot) handleDownloadRequest(msg *tgbotapi.Message) {
	ctx := context.Background()
	// Store origin message ID to reply to it later
	b.State.Store(fmt.Sprintf("origin_%d", msg.Chat.ID), msg.MessageID)

	// Delete ONLY bot's prompt, keep user's link message
	if promptID, ok := b.State.Load(fmt.Sprintf("prompt_%d", msg.Chat.ID)); ok {
		b.SafeDelete(msg.Chat.ID, promptID.(int))
		b.State.Delete(fmt.Sprintf("prompt_%d", msg.Chat.ID))
	}

	appID := b.extractAppID(msg.Text)
	if appID == "" {
		reply := tgbotapi.NewMessage(msg.Chat.ID, "❌ <b>Link hoặc AppID không hợp lệ.</b> 🚫")
		reply.ParseMode = "HTML"
		reply.ReplyToMessageID = msg.MessageID
		b.SafeSend(reply)
		return
	}

	// Clear state after getting ID
	b.DB.UpdateUser(ctx, msg.Chat.ID, bson.M{"$set": bson.M{"loginStep": ""}})

	appInfo, _ := itunes.GetAppInfo(appID)
	appName := "ID: " + appID
	if appInfo != nil {
		appName = appInfo.TrackName
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Phiên bản mới nhất 🌟", fmt.Sprintf("download_latest_%s", appID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Phiên bản cũ 📜", fmt.Sprintf("download_old_%s", appID)),
		),
	)

	reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("📦 <b>Ứng dụng:</b> <code>%s</code> 🌟\nChọn phiên bản để tải:", html.EscapeString(appName)))
	reply.ParseMode = "HTML"
	reply.ReplyMarkup = keyboard
	reply.ReplyToMessageID = msg.MessageID
	b.SafeSend(reply)
}

func (b *Bot) promptOldVersion(chatID int64, appID string) {
	ctx := context.Background()
	b.DB.UpdateUser(ctx, chatID, bson.M{"$set": bson.M{"loginStep": "awaiting_app_ver_id", "tempAppleId": appID}})
	
	msg := tgbotapi.NewMessage(chatID, "Vui lòng nhập appVerId của phiên bản cũ: 📜")
	msg.ParseMode = "HTML"
	if originID, ok := b.State.Load(fmt.Sprintf("origin_%d", chatID)); ok {
		msg.ReplyToMessageID = originID.(int)
	}
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL("Tìm appVerId 🔍", "https://ipa.thuthuatjb.com/historyver/"),
		),
	)
	sent, _ := b.SafeSend(msg)
	b.State.Store(fmt.Sprintf("prompt_%d", chatID), sent.MessageID)
}

func (b *Bot) executeDownload(chatID int64, appID string, appVerID string) {
	ctx := context.Background()
	user, _ := b.DB.GetUser(ctx, chatID)
	if user == nil {
		return
	}

	// SECURITY: Rate limit chống spam download
	if !b.ActionLimit.Allow(chatID) {
		retry := b.ActionLimit.RetryAfter(chatID)
		reply := tgbotapi.NewMessage(chatID, fmt.Sprintf("🚫 <b>Quá nhiều thao tác.</b>\nVui lòng đợi <b>%d giây</b>.", int(retry.Seconds())+1))
		reply.ParseMode = "HTML"
		b.SafeSend(reply)
		return
	}

	var err error

	// Cooldown check
	if !user.IsPremium && user.LastDownload > 0 {
		cooldown := time.Duration(b.Config.CooldownDuration) * time.Minute
		if time.Since(time.UnixMilli(user.LastDownload)) < cooldown {
			remaining := time.Until(time.UnixMilli(user.LastDownload).Add(cooldown))
			msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("Vui lòng chờ %d phút trước khi tải tiếp ⏳. Bạn cũng có thể nâng cấp lên Premium để không phải chờ.", int(remaining.Minutes())+1))
			msg.ParseMode = "HTML"
			b.SafeSend(msg)
			return
		}
	}

	waitMsg := tgbotapi.NewMessage(chatID, "🔍 <b>Đang kiểm tra ứng dụng từ Apple Store...</b>")
	waitMsg.ParseMode = "HTML"
	if originID, ok := b.State.Load(fmt.Sprintf("origin_%d", chatID)); ok {
		waitMsg.ReplyToMessageID = originID.(int)
	}
	sentWait, _ := b.SafeSend(waitMsg)

	appInfo, _ := itunes.GetAppInfo(appID)
	appName := "ID: " + appID
	if appInfo != nil {
		appName = appInfo.TrackName
	}

	tempDir, _ := filepath.Abs(filepath.Join(b.IPATool.BaseDir, "downloads", uuid.New().String()))
	os.MkdirAll(tempDir, 0755)
	defer os.RemoveAll(tempDir)

	// Sanitize app name for filename
	reg := regexp.MustCompile("[^a-zA-Z0-9]+")
	safeAppName := reg.ReplaceAllString(appName, "_")
	// Note: We use tempDir directly as output for IPATool to let it generate filename with version
	outputPath := tempDir

	// Download (ipatool download already includes --purchase flag)
	edit := tgbotapi.NewEditMessageText(chatID, sentWait.MessageID, fmt.Sprintf("📥 <b>Đang tải IPA về máy chủ...</b>\n📦 <b>Ứng dụng:</b> <code>%s</code>", html.EscapeString(appName)))
	edit.ParseMode = "HTML"
	b.SafeEdit(edit)
	var downloadedVersion, actualPath string
	downloadedVersion, actualPath, err = b.IPATool.Download(chatID, appID, appVerID, outputPath)

	// Auto-relogin if session is lost
	if err != nil && strings.Contains(err.Error(), "failed to get account") && user.AppleID != "" && user.Password != "" {
		editRelogin := tgbotapi.NewEditMessageText(chatID, sentWait.MessageID, "🔑 <b>Phiên đăng nhập hết hạn, đang tự động đăng nhập lại...</b>")
		editRelogin.ParseMode = "HTML"
		b.SafeEdit(editRelogin)
		loginErr := b.IPATool.Login(chatID, user.AppleID, b.decPwd(user.Password), "")
		if loginErr == nil {
			downloadedVersion, actualPath, err = b.IPATool.Download(chatID, appID, appVerID, outputPath)
		} else {
			// Relogin failed
			b.DB.UnsetFields(ctx, chatID, bson.M{"appleId": "", "password": ""})
			edit := tgbotapi.NewEditMessageText(chatID, sentWait.MessageID, "❌ <b>Tự động đăng nhập thất bại.</b>\nTài khoản của bạn có thể yêu cầu 2FA hoặc mật khẩu đã đổi. Vui lòng đăng nhập lại bằng lệnh /start. 🔑")
			edit.ParseMode = "HTML"
			b.SafeEdit(edit)
			return
		}
	}

	// Auto-purchase retry: nếu lỗi là "chưa Nhận / chưa mua" → thử Purchase rồi retry 1 lần
	if err != nil && appInfo != nil && appInfo.BundleId != "" {
		cat, _ := b.IPATool.Categorize(err.Error())
		if cat == ipatool.CatNeedPurchase {
			editPurchase := tgbotapi.NewEditMessageText(chatID, sentWait.MessageID, "🛒 <b>Đang tự động 'Nhận' ứng dụng...</b>")
			editPurchase.ParseMode = "HTML"
			b.SafeEdit(editPurchase)
			if pErr := b.IPATool.Purchase(chatID, appInfo.BundleId); pErr == nil {
				editRetry := tgbotapi.NewEditMessageText(chatID, sentWait.MessageID, fmt.Sprintf("📥 <b>Đã 'Nhận' xong, đang tải lại IPA...</b>\n📦 <b>Ứng dụng:</b> <code>%s</code>", html.EscapeString(appName)))
				editRetry.ParseMode = "HTML"
				b.SafeEdit(editRetry)
				downloadedVersion, actualPath, err = b.IPATool.Download(chatID, appID, appVerID, outputPath)
			}
		}
	}

	if err != nil {
		_, msg := b.IPATool.Categorize(err.Error())
		edit := tgbotapi.NewEditMessageText(chatID, sentWait.MessageID, fmt.Sprintf("❌ <b>Lỗi tải IPA:</b>\n%s", msg))
		edit.ParseMode = "HTML"
		b.SafeEdit(edit)
		return
	}

	if actualPath != "" {
		outputPath = actualPath
	}

	// Size check
	fileInfo, err := os.Stat(outputPath)
	if err == nil {
		limit := b.Config.DownloadLimitNormal
		if user.IsPremium {
			limit = b.Config.DownloadLimitPremium
		}
		if fileInfo.Size() > limit {
			edit := tgbotapi.NewEditMessageText(chatID, sentWait.MessageID, fmt.Sprintf("❌ <b>Tệp %.2fMB vượt quá giới hạn.</b> 🚫", float64(fileInfo.Size())/(1024*1024)))
			edit.ParseMode = "HTML"
			b.SafeEdit(edit)
			return
		}
	}

	// Thumbnail processing (parallel with upload prep)
	var thumbPath string
	if appInfo != nil && appInfo.ArtworkUrl100 != "" {
		thumbPath = b.downloadAndResizeIcon(appInfo.ArtworkUrl100)
	}

	// Format caption
	version := "Không xác định"
	bundleID := "Không xác định"
	if appInfo != nil {
		version = appInfo.Version
		bundleID = appInfo.BundleId
	}
	if downloadedVersion != "" {
		version = downloadedVersion
	}

	// Rename file to include version and suffix
	finalFileName := fmt.Sprintf("%s_%s_encrypted_by_@%s.ipa", safeAppName, version, b.API.Self.UserName)
	finalPath := filepath.Join(filepath.Dir(outputPath), finalFileName)
	if os.Rename(outputPath, finalPath) == nil {
		outputPath = finalPath
	}

	fileSizeMB := float64(0)
	if fileInfo, err := os.Stat(outputPath); err == nil {
		fileSizeMB = float64(fileInfo.Size()) / (1024 * 1024)
	}

	// Người tải: dùng @username Telegram, fallback FirstName, fallback "ID:..."
	downloader := ""
	if user.Username != "" {
		downloader = "@" + user.Username
	} else if user.FirstName != "" {
		downloader = html.EscapeString(user.FirstName)
	} else {
		downloader = fmt.Sprintf("ID:%d", chatID)
	}

	caption := fmt.Sprintf("📦 <b>%s</b>\n\n"+
		"👤 <b>Người tải:</b> %s\n"+
		"🆔 <b>Bundle ID:</b> <code>%s</code>\n"+
		"📐 <b>Dung lượng:</b> %.2f MB\n"+
		"🔢 <b>Phiên bản:</b> %s\n"+
		"📅 <b>Ngày tải:</b> %s\n\n"+
		"🌟 <b>Cung cấp bởi:</b> @%s",
		html.EscapeString(appName), downloader, bundleID, fileSizeMB, version,
		time.Now().Format("15:04:05 02/01/2006"), b.API.Self.UserName)

	if thumbPath != "" {
		defer os.Remove(thumbPath)
	}

	// Upload with progress bar
	editUpload := tgbotapi.NewEditMessageText(chatID, sentWait.MessageID,
		fmt.Sprintf("🚀 <b>Đang tải lên Telegram...</b>\n📦 %s — %.1f MB\n\n%s 0%%",
			html.EscapeString(appName), fileSizeMB, progressBar(0)))
	editUpload.ParseMode = "HTML"
	b.SafeEdit(editUpload)

	uploadStartTime := time.Now()
	var lastProgressUpdate = uploadStartTime
	var lastPct = -1
	progressFn := func(uploaded, total int64) {
		if total <= 0 {
			return
		}
		pct := int(uploaded * 100 / total)
		// Throttle: update every 2s khi pct thay đổi
		if pct != lastPct && time.Since(lastProgressUpdate) > 2*time.Second {
			lastPct = pct
			lastProgressUpdate = time.Now()
			uploadedMB := float64(uploaded) / (1024 * 1024)
			elapsed := time.Since(uploadStartTime).Seconds()
			speedMBs := 0.0
			if elapsed > 0 {
				speedMBs = uploadedMB / elapsed
			}
			etaStr := "—"
			if speedMBs > 0 {
				remaining := (fileSizeMB - uploadedMB) / speedMBs
				if remaining < 60 {
					etaStr = fmt.Sprintf("%.0fs", remaining)
				} else {
					etaStr = fmt.Sprintf("%dm %ds", int(remaining)/60, int(remaining)%60)
				}
			}
			editProg := tgbotapi.NewEditMessageText(chatID, sentWait.MessageID,
				fmt.Sprintf("🚀 <b>Đang tải lên Telegram...</b>\n📦 %s\n📐 %.1f / %.1f MB · ⚡ %.1f MB/s · ⏱ %s\n\n%s %d%%",
					html.EscapeString(appName), uploadedMB, fileSizeMB, speedMBs, etaStr, progressBar(pct), pct))
			editProg.ParseMode = "HTML"
			b.SafeEdit(editProg)
		}
	}

	originID := 0
	if val, ok := b.State.Load(fmt.Sprintf("origin_%d", chatID)); ok {
		originID = val.(int)
	}
	err = b.MTProto.UploadFile(ctx, chatID, outputPath, caption, thumbPath, originID, progressFn)
	
	// Cleanup wait message
	b.SafeDelete(chatID, sentWait.MessageID)

	if err != nil {
		reply := tgbotapi.NewMessage(chatID, fmt.Sprintf("❌ <b>Lỗi gửi file:</b> <code>%s</code>", html.EscapeString(err.Error())))
		reply.ParseMode = "HTML"
		if originID, ok := b.State.Load(fmt.Sprintf("origin_%d", chatID)); ok {
			reply.ReplyToMessageID = originID.(int)
		}
		b.SafeSend(reply)
	} else {
		b.DB.UpdateUser(ctx, chatID, bson.M{
			"$inc": bson.M{"usageCount": 1},
			"$set": bson.M{"lastUsed": time.Now().UnixMilli(), "lastDownload": time.Now().UnixMilli()},
		})
	}
}

// progressBar tạo thanh tiến trình text dạng [█████░░░░░] dài 10 ký tự.
func progressBar(pct int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := pct / 10
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", 10-filled) + "]"
}

// allowedIconHosts giới hạn host được phép fetch icon — chống SSRF.
// Icon từ App Store luôn ở những domain này.
var allowedIconHosts = map[string]bool{
	"is1-ssl.mzstatic.com": true, "is2-ssl.mzstatic.com": true,
	"is3-ssl.mzstatic.com": true, "is4-ssl.mzstatic.com": true,
	"is5-ssl.mzstatic.com": true,
	"a1.mzstatic.com":      true, "a2.mzstatic.com": true,
	"a3.mzstatic.com": true, "a4.mzstatic.com": true,
	"a5.mzstatic.com": true,
	"mzstatic.com":    true,
}

func (b *Bot) downloadAndResizeIcon(iconURL string) string {
	parsed, err := url.Parse(iconURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return ""
	}
	// Chỉ cho phép Apple's CDN domains — chặn SSRF tới internal hosts
	host := parsed.Hostname()
	allowed := false
	for h := range allowedIconHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			allowed = true
			break
		}
	}
	if !allowed {
		log.Printf("⚠️ icon URL host not allowed: %s", host)
		return ""
	}

	// Timeout 10s — không cho 1 URL chậm làm treo bot
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(iconURL)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	img, err := jpeg.Decode(resp.Body)
	if err != nil {
		return ""
	}

	m := resize.Resize(320, 320, img, resize.Lanczos3)
	
	thumbPath := filepath.Join("tmp", uuid.New().String()+".jpg")
	out, err := os.Create(thumbPath)
	if err != nil {
		return ""
	}
	defer out.Close()

	jpeg.Encode(out, m, nil)
	return thumbPath
}

func (b *Bot) extractAppID(input string) string {
	if strings.Contains(input, "id") {
		parts := strings.Split(input, "id")
		if len(parts) > 1 {
			idPart := parts[1]
			var id string
			for _, r := range idPart {
				if r >= '0' && r <= '9' {
					id += string(r)
				} else {
					break
				}
			}
			return id
		}
	}
	isNum := true
	for _, r := range input {
		if r < '0' || r > '9' {
			isNum = false
			break
		}
	}
	if isNum && len(input) > 0 {
		return input
	}
	return ""
}

func (b *Bot) handleGetLink(msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	replyTo := msg.ReplyToMessage
	if replyTo == nil || replyTo.Document == nil {
		reply := tgbotapi.NewMessage(chatID, "⚠️ Hãy trả lời (reply) vào một tin nhắn chứa file IPA để tạo link cài!")
		reply.ParseMode = "HTML"
		reply.ReplyToMessageID = msg.MessageID
		b.SafeSend(reply)
		return
	}

	doc := replyTo.Document
	if !strings.HasSuffix(strings.ToLower(doc.FileName), ".ipa") {
		reply := tgbotapi.NewMessage(chatID, "⚠️ File này không phải định dạng <b>.ipa</b>!")
		reply.ParseMode = "HTML"
		reply.ReplyToMessageID = msg.MessageID
		b.SafeSend(reply)
		return
	}

	waitMsg := tgbotapi.NewMessage(chatID, "⏳ <b>Đang kết nối máy chủ Telegram để lấy file...</b>")
	waitMsg.ParseMode = "HTML"
	waitMsg.ReplyToMessageID = msg.MessageID
	sentWait, _ := b.SafeSend(waitMsg)

	fileID := uuid.New().String()
	safeName := strings.ReplaceAll(doc.FileName, " ", "_")
	finalFileName := fmt.Sprintf("%s_%s", fileID, safeName)
	publicDir := "public"
	os.MkdirAll(publicDir, 0755)
	destPath := filepath.Join(publicDir, finalFileName)

	ctx := context.Background()
	err := b.MTProto.DownloadFile(ctx, replyTo.MessageID, destPath, nil)
	if err != nil {
		editError := tgbotapi.NewEditMessageText(chatID, sentWait.MessageID, fmt.Sprintf("❌ <b>Lỗi:</b> <code>%s</code>", html.EscapeString(err.Error())))
		editError.ParseMode = "HTML"
		b.SafeEdit(editError)
		return
	}

	// directLink := fmt.Sprintf("%s/public/%s", b.Config.HostURL, finalFileName)
	// installLink := fmt.Sprintf("https://dl.thuthuatjb.com/ipa/install.html?url=%s", url.QueryEscape(directLink))
	
	// Use new shortened redirect link
	shortLink := fmt.Sprintf("%s/i/%s", b.Config.HostURL, finalFileName)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL("Cài đặt trực tiếp 📲", shortLink),
		),
	)

	editSuccess := tgbotapi.NewEditMessageText(chatID, sentWait.MessageID, fmt.Sprintf("✅ <b>Tạo link thành công!</b>\n\n📦 <b>File:</b> <code>%s</code>\n⚖️ <b>Dung lượng:</b> %.2f MB\n\n<i>⚠️ Link sẽ tự động xóa sau 1 giờ.</i>", html.EscapeString(doc.FileName), float64(doc.FileSize)/(1024*1024)))
	editSuccess.ParseMode = "HTML"
	b.SafeEdit(editSuccess)
	
	edit := tgbotapi.NewEditMessageReplyMarkup(chatID, sentWait.MessageID, keyboard)
	b.SafeEdit(edit)

	// Note: Cleanup is now handled by the main cleanup task in main.go
}
