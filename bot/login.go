package bot

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"ipa-downloader-bot/db"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.mongodb.org/mongo-driver/bson"
)

// handleLoginCommand xử lý `/login <email> <password>` — đăng nhập nhanh 1 dòng.
// Sau khi parse xong, xóa luôn tin nhắn user để password ko hiện lâu trong chat.
func (b *Bot) handleLoginCommand(msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	userID := userIDOf(msg)
	// Xóa ngay tin nhắn có chứa password — bảo vệ user
	defer b.SafeDelete(chatID, msg.MessageID)

	// SECURITY: Login PHẢI làm trong DM, không cho làm trong group
	if !isPrivateChat(msg) {
		b.replyDMRequired(msg)
		return
	}

	// SECURITY: Rate limit login để chống brute-force (key bằng userID)
	if !b.LoginLimit.Allow(userID) {
		retry := b.LoginLimit.RetryAfter(userID)
		reply := tgbotapi.NewMessage(chatID, fmt.Sprintf("🚫 <b>Quá nhiều lần thử đăng nhập.</b>\nVui lòng đợi <b>%d phút</b> rồi thử lại.", int(retry.Minutes())+1))
		reply.ParseMode = "HTML"
		b.SafeSend(reply)
		return
	}

	args := strings.Fields(msg.CommandArguments())
	if len(args) < 2 {
		reply := tgbotapi.NewMessage(chatID, "📲 <b>Cách dùng:</b>\n<code>/login &lt;email&gt; &lt;password&gt;</code>\n\n"+
			"Ví dụ:\n<code>/login me@icloud.com MyPass123</code>\n\n"+
			"<i>⚠️ Sau khi gửi, bot tự xóa tin nhắn của bạn để bảo mật.</i>")
		reply.ParseMode = "HTML"
		b.SafeSend(reply)
		return
	}
	email := args[0]
	password := strings.Join(args[1:], " ")

	wait := tgbotapi.NewMessage(chatID, "🔑 <b>Đang đăng nhập...</b>")
	wait.ParseMode = "HTML"
	sent, _ := b.SafeSend(wait)

	b.IPATool.ResetSession(userID)

	ctx := context.Background()
	b.DB.UpdateUser(ctx, userID, bson.M{"$set": bson.M{"tempAppleId": email, "password": b.encPwd(password)}})

	stdout, stderr, err := b.IPATool.LoginWithResult(userID, email, password, "")
	combined := stdout + "\n" + stderr

	is2FA := strings.Contains(combined, "auth-code") ||
		strings.Contains(combined, "2FA") ||
		strings.Contains(combined, "verification code") ||
		strings.Contains(combined, "6-digit") ||
		strings.Contains(combined, "Configurator_message")

	if is2FA {
		b.DB.UpdateUser(ctx, userID, bson.M{"$set": bson.M{"loginStep": "awaiting_2fa"}})
		edit := tgbotapi.NewEditMessageText(chatID, sent.MessageID,
			"🔐 <b>Apple đã gửi mã xác thực 2FA đến thiết bị của bạn.</b>\nVui lòng nhập mã 6 chữ số:")
		edit.ParseMode = "HTML"
		b.SafeEdit(edit)
		b.State.Store(fmt.Sprintf("prompt_%d", userID), sent.MessageID)
		return
	}

	if err != nil {
		b.DB.UnsetFields(ctx, userID, bson.M{"loginStep": "", "tempAppleId": "", "password": ""})
		edit := tgbotapi.NewEditMessageText(chatID, sent.MessageID,
			fmt.Sprintf("❌ <b>Lỗi đăng nhập:</b>\n%s", b.IPATool.FormatError(combined)))
		edit.ParseMode = "HTML"
		b.SafeEdit(edit)
		return
	}

	info, _ := b.IPATool.AuthInfo(userID)
	if strings.Contains(strings.ToLower(info), strings.ToLower(email)) {
		b.LoginLimit.Reset(userID)
		b.SafeDelete(chatID, sent.MessageID)
		b.finishLogin(chatID, userID, email, password)
	} else {
		b.DB.UnsetFields(ctx, userID, bson.M{"loginStep": "", "tempAppleId": "", "password": ""})
		edit := tgbotapi.NewEditMessageText(chatID, sent.MessageID,
			fmt.Sprintf("❌ <b>Lỗi:</b> Không thể xác định trạng thái đăng nhập.\n%s", b.IPATool.FormatError(combined)))
		edit.ParseMode = "HTML"
		b.SafeEdit(edit)
	}
}

// startLogin bắt đầu flow đăng nhập từng bước.
// chatID: nơi gửi prompt (luôn là DM của user vì login chỉ làm trong DM).
// userID: định danh user trong DB / ipatool keychain.
func (b *Bot) startLogin(chatID, userID int64) {
	ctx := context.Background()
	b.IPATool.ResetSession(userID)
	// Set loginStep + lastUsed cùng lúc — cleanup goroutine dùng lastUsed để biết user còn active.
	b.DB.UpdateUser(ctx, userID, bson.M{"$set": bson.M{
		"loginStep": "awaiting_apple_id",
		"lastUsed":  time.Now().UnixMilli(),
	}})
	msg := tgbotapi.NewMessage(chatID, "Vui lòng nhập Apple ID: 📧")
	msg.ParseMode = "HTML"
	sent, _ := b.SafeSend(msg)
	b.State.Store(fmt.Sprintf("prompt_%d", userID), sent.MessageID)
}

func (b *Bot) handleLoginStep(msg *tgbotapi.Message, user *db.User) {
	ctx := context.Background()
	chatID := msg.Chat.ID
	userID := userIDOf(msg) // Trong DM userID == chatID, nhưng explicit để rõ
	// Always delete user's input message to keep chat clean
	defer b.SafeDelete(chatID, msg.MessageID)

	switch user.LoginStep {
	case "awaiting_apple_id":
		if promptID, ok := b.State.Load(fmt.Sprintf("prompt_%d", userID)); ok {
			b.SafeDelete(chatID, promptID.(int))
		}

		b.DB.UpdateUser(ctx, userID, bson.M{"$set": bson.M{"loginStep": "awaiting_password", "tempAppleId": msg.Text}})
		reply := tgbotapi.NewMessage(chatID, "Vui lòng nhập mật khẩu: 🔒")
		reply.ParseMode = "HTML"
		sent, _ := b.SafeSend(reply)
		b.State.Store(fmt.Sprintf("prompt_%d", userID), sent.MessageID)
	case "awaiting_password":
		if promptID, ok := b.State.Load(fmt.Sprintf("prompt_%d", userID)); ok {
			b.SafeDelete(chatID, promptID.(int))
			b.State.Delete(fmt.Sprintf("prompt_%d", userID))
		}

		password := msg.Text
		stdout, stderr, err := b.IPATool.LoginWithResult(userID, user.TempAppleID, password, "")

		combinedOutput := stdout + "\n" + stderr
		is2FA := strings.Contains(combinedOutput, "auth-code") ||
			strings.Contains(combinedOutput, "2FA") ||
			strings.Contains(combinedOutput, "verification code") ||
			strings.Contains(combinedOutput, "6-digit") ||
			strings.Contains(combinedOutput, "Configurator_message")

		if is2FA {
			b.DB.UpdateUser(ctx, userID, bson.M{"$set": bson.M{"loginStep": "awaiting_2fa", "password": b.encPwd(password)}})
			reply := tgbotapi.NewMessage(chatID, "🔐 <b>Apple đã gửi mã xác thực 2FA đến thiết bị của bạn.</b>\nVui lòng nhập mã 6 chữ số:")
			reply.ParseMode = "HTML"
			sent, _ := b.SafeSend(reply)
			b.State.Store(fmt.Sprintf("prompt_%d", userID), sent.MessageID)
			return
		}

		if err != nil {
			b.DB.UnsetFields(ctx, userID, bson.M{"loginStep": "", "tempAppleId": ""})
			reply := tgbotapi.NewMessage(chatID, fmt.Sprintf("❌ <b>Lỗi đăng nhập:</b>\n%s", b.IPATool.FormatError(combinedOutput)))
			reply.ParseMode = "HTML"
			b.SafeSend(reply)
			return
		}

		info, _ := b.IPATool.AuthInfo(userID)
		if strings.Contains(strings.ToLower(info), strings.ToLower(user.TempAppleID)) {
			b.finishLogin(chatID, userID, user.TempAppleID, password)
		} else {
			b.DB.UnsetFields(ctx, userID, bson.M{"loginStep": "", "tempAppleId": ""})
			reply := tgbotapi.NewMessage(chatID, fmt.Sprintf("❌ <b>Lỗi:</b> Không thể xác định trạng thái đăng nhập.\n%s", b.IPATool.FormatError(combinedOutput)))
			reply.ParseMode = "HTML"
			b.SafeSend(reply)
		}
	case "awaiting_2fa":
		if promptID, ok := b.State.Load(fmt.Sprintf("prompt_%d", userID)); ok {
			b.SafeDelete(chatID, promptID.(int))
			b.State.Delete(fmt.Sprintf("prompt_%d", userID))
		}

		code := msg.Text
		passwordPlain := b.decPwd(user.Password)
		stdout, stderr, err := b.IPATool.LoginWithResult(userID, user.TempAppleID, passwordPlain, code)
		combinedOutput := stdout + "\n" + stderr

		if err != nil {
			b.DB.UnsetFields(ctx, userID, bson.M{"loginStep": "", "tempAppleId": "", "password": ""})
			reply := tgbotapi.NewMessage(chatID, fmt.Sprintf("❌ <b>Lỗi xác thực 2FA:</b>\n%s", b.IPATool.FormatError(combinedOutput)))
			reply.ParseMode = "HTML"
			b.SafeSend(reply)
			return
		}

		info, err := b.IPATool.AuthInfo(userID)
		if err == nil && strings.Contains(strings.ToLower(info), strings.ToLower(user.TempAppleID)) {
			b.finishLogin(chatID, userID, user.TempAppleID, passwordPlain)
		} else {
			b.DB.UnsetFields(ctx, userID, bson.M{"loginStep": "", "tempAppleId": "", "password": ""})
			reply := tgbotapi.NewMessage(chatID, fmt.Sprintf("❌ <b>Lỗi:</b> Xác thực 2FA thành công nhưng không thể lưu phiên.\n%s", b.IPATool.FormatError(info)))
			reply.ParseMode = "HTML"
			b.SafeSend(reply)
		}
	case "awaiting_app_ver_id":
		appID := user.TempAppleID
		appVerID := strings.TrimSpace(msg.Text)

		isNumeric := regexp.MustCompile(`^\d+$`).MatchString(appVerID)
		if !isNumeric {
			reply := tgbotapi.NewMessage(chatID, "❌ <b>ID phiên bản không hợp lệ.</b>\nVui lòng chỉ nhập số (ví dụ: 861234567): 📜")
			reply.ParseMode = "HTML"
			if originID, ok := b.State.Load(fmt.Sprintf("origin_%d", userID)); ok {
				reply.ReplyToMessageID = originID.(int)
			}
			sent, _ := b.SafeSend(reply)
			b.State.Store(fmt.Sprintf("prompt_%d", userID), sent.MessageID)
			return
		}

		if promptID, ok := b.State.Load(fmt.Sprintf("prompt_%d", userID)); ok {
			b.SafeDelete(chatID, promptID.(int))
			b.State.Delete(fmt.Sprintf("prompt_%d", userID))
		}

		b.DB.UpdateUser(ctx, userID, bson.M{"$set": bson.M{"loginStep": ""}})
		b.executeDownload(chatID, userID, appID, appVerID)
	}
}

// finishLogin lưu credentials sau khi login thành công.
// chatID: nơi gửi tin nhắn báo thành công.
// userID: định danh user trong DB.
func (b *Bot) finishLogin(chatID, userID int64, appleID, password string) {
	ctx := context.Background()
	encPwd := b.encPwd(password)

	user, _ := b.DB.GetUser(ctx, userID)
	accounts := []db.Account{}
	if user != nil && user.Accounts != nil {
		accounts = user.Accounts
	}

	filtered := accounts[:0]
	for _, a := range accounts {
		if a.AppleID != appleID {
			filtered = append(filtered, a)
		}
	}
	filtered = append(filtered, db.Account{
		AppleID:  appleID,
		Password: encPwd,
		AddedAt:  time.Now().UnixMilli(),
	})

	b.DB.UpdateUser(ctx, userID, bson.M{
		"$set": bson.M{
			"appleId":   appleID,
			"password":  encPwd,
			"loginStep": "",
			"accounts":  filtered,
		},
	})
	b.DB.UnsetFields(ctx, userID, bson.M{"tempAppleId": ""})

	totalAccounts := len(filtered)
	successMsg := "✅ <b>Đăng nhập thành công!</b>"
	if totalAccounts > 1 {
		successMsg += fmt.Sprintf("\n\nBạn đang có <b>%d tài khoản</b>. Dùng /accounts để chuyển/quản lý.", totalAccounts)
	} else {
		successMsg += "\n\nGửi link App Store hoặc /get để tải IPA, /accounts để thêm tài khoản khác."
	}
	msg := tgbotapi.NewMessage(chatID, successMsg)
	msg.ParseMode = "HTML"
	b.SafeSend(msg)
}
