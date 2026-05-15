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
	// Xóa ngay tin nhắn có chứa password — bảo vệ user
	defer b.SafeDelete(chatID, msg.MessageID)

	// SECURITY: Rate limit login để chống brute-force
	if !b.LoginLimit.Allow(chatID) {
		retry := b.LoginLimit.RetryAfter(chatID)
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
	password := strings.Join(args[1:], " ") // password có thể có space

	wait := tgbotapi.NewMessage(chatID, "🔑 <b>Đang đăng nhập...</b>")
	wait.ParseMode = "HTML"
	sent, _ := b.SafeSend(wait)

	// Reset session để bắt đầu sạch
	b.IPATool.ResetSession(chatID)

	ctx := context.Background()
	// Lưu temp credentials (encrypted) để dùng cho 2FA nếu cần
	b.DB.UpdateUser(ctx, chatID, bson.M{"$set": bson.M{"tempAppleId": email, "password": b.encPwd(password)}})

	stdout, stderr, err := b.IPATool.LoginWithResult(chatID, email, password, "")
	combined := stdout + "\n" + stderr

	is2FA := strings.Contains(combined, "auth-code") ||
		strings.Contains(combined, "2FA") ||
		strings.Contains(combined, "verification code") ||
		strings.Contains(combined, "6-digit") ||
		strings.Contains(combined, "Configurator_message")

	if is2FA {
		b.DB.UpdateUser(ctx, chatID, bson.M{"$set": bson.M{"loginStep": "awaiting_2fa"}})
		edit := tgbotapi.NewEditMessageText(chatID, sent.MessageID,
			"🔐 <b>Apple đã gửi mã xác thực 2FA đến thiết bị của bạn.</b>\nVui lòng nhập mã 6 chữ số:")
		edit.ParseMode = "HTML"
		b.SafeEdit(edit)
		b.State.Store(fmt.Sprintf("prompt_%d", chatID), sent.MessageID)
		return
	}

	if err != nil {
		b.DB.UnsetFields(ctx, chatID, bson.M{"loginStep": "", "tempAppleId": "", "password": ""})
		edit := tgbotapi.NewEditMessageText(chatID, sent.MessageID,
			fmt.Sprintf("❌ <b>Lỗi đăng nhập:</b>\n%s", b.IPATool.FormatError(combined)))
		edit.ParseMode = "HTML"
		b.SafeEdit(edit)
		return
	}

	// Verify với auth info
	info, _ := b.IPATool.AuthInfo(chatID)
	if strings.Contains(strings.ToLower(info), strings.ToLower(email)) {
		b.LoginLimit.Reset(chatID) // login OK → reset rate limit
		b.SafeDelete(chatID, sent.MessageID)
		b.finishLogin(chatID, email, password)
	} else {
		b.DB.UnsetFields(ctx, chatID, bson.M{"loginStep": "", "tempAppleId": "", "password": ""})
		edit := tgbotapi.NewEditMessageText(chatID, sent.MessageID,
			fmt.Sprintf("❌ <b>Lỗi:</b> Không thể xác định trạng thái đăng nhập.\n%s", b.IPATool.FormatError(combined)))
		edit.ParseMode = "HTML"
		b.SafeEdit(edit)
	}
}

func (b *Bot) startLogin(chatID int64) {
	ctx := context.Background()
	b.IPATool.ResetSession(chatID)
	b.DB.UpdateUser(ctx, chatID, bson.M{"$set": bson.M{"loginStep": "awaiting_apple_id"}})
	msg := tgbotapi.NewMessage(chatID, "Vui lòng nhập Apple ID: 📧")
	msg.ParseMode = "HTML"
	sent, _ := b.SafeSend(msg)
	b.State.Store(fmt.Sprintf("prompt_%d", chatID), sent.MessageID)
}

func (b *Bot) handleLoginStep(msg *tgbotapi.Message, user *db.User) {
	ctx := context.Background()
	// Always delete user's input message to keep chat clean
	defer b.SafeSend(tgbotapi.NewDeleteMessage(msg.Chat.ID, msg.MessageID))

	switch user.LoginStep {
	case "awaiting_apple_id":
		// Delete the previous prompt if exists
		if promptID, ok := b.State.Load(fmt.Sprintf("prompt_%d", msg.Chat.ID)); ok {
			b.SafeSend(tgbotapi.NewDeleteMessage(msg.Chat.ID, promptID.(int)))
		}

		b.DB.UpdateUser(ctx, msg.Chat.ID, bson.M{"$set": bson.M{"loginStep": "awaiting_password", "tempAppleId": msg.Text}})
		reply := tgbotapi.NewMessage(msg.Chat.ID, "Vui lòng nhập mật khẩu: 🔒")
		reply.ParseMode = "HTML"
		sent, _ := b.SafeSend(reply)
		b.State.Store(fmt.Sprintf("prompt_%d", msg.Chat.ID), sent.MessageID)
	case "awaiting_password":
		// Delete the previous prompt if exists
		if promptID, ok := b.State.Load(fmt.Sprintf("prompt_%d", msg.Chat.ID)); ok {
			b.SafeSend(tgbotapi.NewDeleteMessage(msg.Chat.ID, promptID.(int)))
			b.State.Delete(fmt.Sprintf("prompt_%d", msg.Chat.ID))
		}

		password := msg.Text
		stdout, stderr, err := b.IPATool.LoginWithResult(msg.Chat.ID, user.TempAppleID, password, "")
		
		combinedOutput := stdout + "\n" + stderr
		// Real-world 2FA detection from ipatool output
		is2FA := strings.Contains(combinedOutput, "auth-code") || 
		         strings.Contains(combinedOutput, "2FA") || 
		         strings.Contains(combinedOutput, "verification code") || 
		         strings.Contains(combinedOutput, "6-digit") ||
		         strings.Contains(combinedOutput, "Configurator_message")

		if is2FA {
			// Encrypt password trước khi lưu tạm (cần cho bước 2FA)
			b.DB.UpdateUser(ctx, msg.Chat.ID, bson.M{"$set": bson.M{"loginStep": "awaiting_2fa", "password": b.encPwd(password)}})
			reply := tgbotapi.NewMessage(msg.Chat.ID, "🔐 <b>Apple đã gửi mã xác thực 2FA đến thiết bị của bạn.</b>\nVui lòng nhập mã 6 chữ số:")
			reply.ParseMode = "HTML"
			sent, _ := b.SafeSend(reply)
			b.State.Store(fmt.Sprintf("prompt_%d", msg.Chat.ID), sent.MessageID)
			return
		}

		if err != nil {
			b.DB.UnsetFields(ctx, msg.Chat.ID, bson.M{"loginStep": "", "tempAppleId": ""})
			reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("❌ <b>Lỗi đăng nhập:</b>\n%s", b.IPATool.FormatError(combinedOutput)))
			reply.ParseMode = "HTML"
			b.SafeSend(reply)
			return
		}
		
		// If no error and not 2FA, it MUST be success. Still double check.
		info, _ := b.IPATool.AuthInfo(msg.Chat.ID)
		if strings.Contains(strings.ToLower(info), strings.ToLower(user.TempAppleID)) {
			b.finishLogin(msg.Chat.ID, user.TempAppleID, password)
		} else {
			b.DB.UnsetFields(ctx, msg.Chat.ID, bson.M{"loginStep": "", "tempAppleId": ""})
			reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("❌ <b>Lỗi:</b> Không thể xác định trạng thái đăng nhập.\n%s", b.IPATool.FormatError(combinedOutput)))
			reply.ParseMode = "HTML"
			b.SafeSend(reply)
		}
	case "awaiting_2fa":
		// Delete the previous prompt if exists
		if promptID, ok := b.State.Load(fmt.Sprintf("prompt_%d", msg.Chat.ID)); ok {
			b.SafeSend(tgbotapi.NewDeleteMessage(msg.Chat.ID, promptID.(int)))
			b.State.Delete(fmt.Sprintf("prompt_%d", msg.Chat.ID))
		}

		code := msg.Text
		// user.Password trong DB là ciphertext (đã encrypt ở bước awaiting_password) → decrypt khi dùng
		passwordPlain := b.decPwd(user.Password)
		stdout, stderr, err := b.IPATool.LoginWithResult(msg.Chat.ID, user.TempAppleID, passwordPlain, code)
		combinedOutput := stdout + "\n" + stderr

		if err != nil {
			b.DB.UnsetFields(ctx, msg.Chat.ID, bson.M{"loginStep": "", "tempAppleId": "", "password": ""})
			reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("❌ <b>Lỗi xác thực 2FA:</b>\n%s", b.IPATool.FormatError(combinedOutput)))
			reply.ParseMode = "HTML"
			b.SafeSend(reply)
			return
		}

		// Verify with auth info
		info, err := b.IPATool.AuthInfo(msg.Chat.ID)
		if err == nil && strings.Contains(strings.ToLower(info), strings.ToLower(user.TempAppleID)) {
			b.finishLogin(msg.Chat.ID, user.TempAppleID, passwordPlain)
		} else {
			b.DB.UnsetFields(ctx, msg.Chat.ID, bson.M{"loginStep": "", "tempAppleId": "", "password": ""})
			reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("❌ <b>Lỗi:</b> Xác thực 2FA thành công nhưng không thể lưu phiên.\n%s", b.IPATool.FormatError(info)))
			reply.ParseMode = "HTML"
			b.SafeSend(reply)
		}
	case "awaiting_app_ver_id":
		appID := user.TempAppleID
		appVerID := strings.TrimSpace(msg.Text)
		
		// Validation: must be numeric
		isNumeric := regexp.MustCompile(`^\d+$`).MatchString(appVerID)
		if !isNumeric {
			reply := tgbotapi.NewMessage(msg.Chat.ID, "❌ <b>ID phiên bản không hợp lệ.</b>\nVui lòng chỉ nhập số (ví dụ: 861234567): 📜")
			reply.ParseMode = "HTML"
			if originID, ok := b.State.Load(fmt.Sprintf("origin_%d", msg.Chat.ID)); ok {
				reply.ReplyToMessageID = originID.(int)
			}
			sent, _ := b.SafeSend(reply)
			// Update prompt ID so it can be deleted later
			b.State.Store(fmt.Sprintf("prompt_%d", msg.Chat.ID), sent.MessageID)
			return
		}

		// Delete the previous prompt if exists
		if promptID, ok := b.State.Load(fmt.Sprintf("prompt_%d", msg.Chat.ID)); ok {
			b.SafeSend(tgbotapi.NewDeleteMessage(msg.Chat.ID, promptID.(int)))
			b.State.Delete(fmt.Sprintf("prompt_%d", msg.Chat.ID))
		}

		b.DB.UpdateUser(ctx, msg.Chat.ID, bson.M{"$set": bson.M{"loginStep": ""}})
		b.executeDownload(msg.Chat.ID, appID, appVerID)
	}
}

func (b *Bot) finishLogin(chatID int64, appleID, password string) {
	ctx := context.Background()
	// Mã hoá password trước khi lưu — KHÔNG lưu plaintext vào DB
	encPwd := b.encPwd(password)

	// Lấy user hiện tại để biết Accounts đã có gì
	user, _ := b.DB.GetUser(ctx, chatID)
	accounts := []db.Account{}
	if user != nil && user.Accounts != nil {
		accounts = user.Accounts
	}

	// Xóa entry cũ với cùng email (nếu có) để re-login update password
	filtered := accounts[:0]
	for _, a := range accounts {
		if a.AppleID != appleID {
			filtered = append(filtered, a)
		}
	}
	filtered = append(filtered, db.Account{
		AppleID:  appleID,
		Password: encPwd, // ciphertext
		AddedAt:  time.Now().UnixMilli(),
	})

	b.DB.UpdateUser(ctx, chatID, bson.M{
		"$set": bson.M{
			"appleId":   appleID,
			"password":  encPwd, // ciphertext
			"loginStep": "",
			"accounts":  filtered,
		},
	})
	b.DB.UnsetFields(ctx, chatID, bson.M{"tempAppleId": ""})

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
