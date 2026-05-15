package bot

import (
	"context"
	"fmt"
	"html"
	"strings"

	"ipa-downloader-bot/db"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.mongodb.org/mongo-driver/bson"
)

// handleAccounts hiển thị danh sách Apple IDs đã đăng nhập + nút chuyển/xóa.
func (b *Bot) handleAccounts(msg *tgbotapi.Message) {
	ctx := context.Background()
	user, _ := b.DB.GetUser(ctx, msg.Chat.ID)
	if user == nil {
		reply := tgbotapi.NewMessage(msg.Chat.ID, "Bạn chưa đăng nhập. Dùng /start để bắt đầu. 🔑")
		reply.ParseMode = "HTML"
		b.SafeSend(reply)
		return
	}

	// Migration: nếu chưa có Accounts nhưng có AppleID active → khởi tạo từ active.
	// Lưu ý: user.Password đã encrypted, giữ nguyên (Accounts[].Password cũng encrypted).
	accounts := user.Accounts
	if len(accounts) == 0 && user.AppleID != "" {
		accounts = []db.Account{{
			AppleID:  user.AppleID,
			Password: user.Password,
			AddedAt:  user.CreatedAt,
		}}
		b.DB.UpdateUser(ctx, msg.Chat.ID, bson.M{"$set": bson.M{"accounts": accounts}})
	}

	if len(accounts) == 0 {
		reply := tgbotapi.NewMessage(msg.Chat.ID, "Bạn chưa có Apple ID nào. Dùng /start để đăng nhập. 🔑")
		reply.ParseMode = "HTML"
		b.SafeSend(reply)
		return
	}

	var sb strings.Builder
	sb.WriteString("📋 <b>Tài khoản Apple ID của bạn:</b>\n\n")
	rows := [][]tgbotapi.InlineKeyboardButton{}
	for i, a := range accounts {
		active := ""
		if a.AppleID == user.AppleID {
			active = " ✅"
		}
		sb.WriteString(fmt.Sprintf("%d. <code>%s</code>%s\n", i+1, html.EscapeString(a.AppleID), active))

		// Button row: switch (nếu chưa active) + remove
		if a.AppleID != user.AppleID {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("Chuyển sang #%d ↔️", i+1), fmt.Sprintf("acc_use_%d", i)),
				tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("Xóa #%d 🗑", i+1), fmt.Sprintf("acc_rm_%d", i)),
			))
		} else {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("Xóa #%d 🗑", i+1), fmt.Sprintf("acc_rm_%d", i)),
			))
		}
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("➕ Thêm tài khoản", "acc_add"),
	))
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("⬅️ Quay lại", "back_start"),
	))

	reply := tgbotapi.NewMessage(msg.Chat.ID, sb.String())
	reply.ParseMode = "HTML"
	reply.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	b.SafeSend(reply)
}

// switchAccount chuyển active account, ipatool sẽ re-login khi cần dùng tới
// (vì keychain chỉ giữ 1 account, ta re-login silently lúc download/auth_info).
func (b *Bot) switchAccount(chatID int64, idx int) {
	ctx := context.Background()
	user, _ := b.DB.GetUser(ctx, chatID)
	if user == nil || idx < 0 || idx >= len(user.Accounts) {
		b.SafeSend(tgbotapi.NewMessage(chatID, "❌ Tài khoản không tồn tại."))
		return
	}

	target := user.Accounts[idx]
	if target.AppleID == user.AppleID {
		b.SafeSend(tgbotapi.NewMessage(chatID, "ℹ️ Đây đã là tài khoản đang hoạt động."))
		return
	}

	// Báo đang chuyển
	wait := tgbotapi.NewMessage(chatID, fmt.Sprintf("🔄 <b>Đang chuyển sang:</b> <code>%s</code>...", html.EscapeString(target.AppleID)))
	wait.ParseMode = "HTML"
	sent, _ := b.SafeSend(wait)

	// Reset keychain + re-login silent với credentials đã lưu (cần decrypt password)
	b.IPATool.ResetSession(chatID)
	err := b.IPATool.Login(chatID, target.AppleID, b.decPwd(target.Password), "")
	if err != nil {
		// Login fail (2FA cần / password đổi) → vẫn set active nhưng báo user
		cat, msg := b.IPATool.Categorize(err.Error())
		hint := msg
		if cat == 2 /* Cat2FA */ {
			hint = "Tài khoản này cần 2FA. Hãy dùng /start → Đăng nhập để xác thực lại."
		}
		b.DB.UpdateUser(ctx, chatID, bson.M{"$set": bson.M{"appleId": target.AppleID, "password": target.Password}})
		editFail := tgbotapi.NewEditMessageText(chatID, sent.MessageID, fmt.Sprintf("⚠️ <b>Đã chuyển nhưng cần đăng nhập lại:</b>\n%s", hint))
		editFail.ParseMode = "HTML"
		b.SafeEdit(editFail)
		return
	}

	// Login OK → set active
	b.DB.UpdateUser(ctx, chatID, bson.M{"$set": bson.M{"appleId": target.AppleID, "password": target.Password}})

	editOK := tgbotapi.NewEditMessageText(chatID, sent.MessageID, fmt.Sprintf("✅ <b>Đã chuyển sang:</b> <code>%s</code>", html.EscapeString(target.AppleID)))
	editOK.ParseMode = "HTML"
	b.SafeEdit(editOK)
}

// removeAccount xóa 1 entry khỏi Accounts. Nếu đó là active → chuyển sang account khác hoặc clear hết.
func (b *Bot) removeAccount(chatID int64, idx int) {
	ctx := context.Background()
	user, _ := b.DB.GetUser(ctx, chatID)
	if user == nil || idx < 0 || idx >= len(user.Accounts) {
		b.SafeSend(tgbotapi.NewMessage(chatID, "❌ Tài khoản không tồn tại."))
		return
	}

	removed := user.Accounts[idx]
	newAccounts := append([]db.Account{}, user.Accounts[:idx]...)
	newAccounts = append(newAccounts, user.Accounts[idx+1:]...)

	updateSet := bson.M{"accounts": newAccounts}
	wasActive := removed.AppleID == user.AppleID

	if wasActive {
		// Cần chuyển active sang account khác (nếu còn) hoặc clear
		b.IPATool.ResetSession(chatID)
		if len(newAccounts) > 0 {
			next := newAccounts[0]
			updateSet["appleId"] = next.AppleID
			updateSet["password"] = next.Password // ciphertext, giữ nguyên
			// Try silent re-login (cần decrypt password)
			b.IPATool.Login(chatID, next.AppleID, b.decPwd(next.Password), "")
		} else {
			updateSet["appleId"] = ""
			updateSet["password"] = ""
		}
	}

	b.DB.UpdateUser(ctx, chatID, bson.M{"$set": updateSet})

	notice := fmt.Sprintf("🗑 <b>Đã xóa:</b> <code>%s</code>", html.EscapeString(removed.AppleID))
	if wasActive && len(newAccounts) > 0 {
		notice += fmt.Sprintf("\n✅ Đã chuyển sang: <code>%s</code>", html.EscapeString(newAccounts[0].AppleID))
	} else if wasActive {
		notice += "\n⚠️ Không còn tài khoản nào. Dùng /start để đăng nhập."
	}
	reply := tgbotapi.NewMessage(chatID, notice)
	reply.ParseMode = "HTML"
	b.SafeSend(reply)
}
