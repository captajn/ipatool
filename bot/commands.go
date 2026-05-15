package bot

import (
	"context"
	"fmt"
	"html"
	"log"
	"os"
	"path/filepath"
	"time"

	"ipa-downloader-bot/db"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.mongodb.org/mongo-driver/bson"
)

func (b *Bot) HandleMessage(msg *tgbotapi.Message) {
	ctx := context.Background()
	user, err := b.DB.GetUser(ctx, msg.Chat.ID)
	if err != nil {
		log.Printf("Error getting user: %v", err)
		return
	}

	// 1. Always prioritize commands (like /start to reset state)
	if msg.IsCommand() {
		b.DB.UpdateUser(ctx, msg.Chat.ID, bson.M{"$set": bson.M{"lastUsed": time.Now().UnixMilli()}})
		switch msg.Command() {
		case "start":
			b.handleStart(msg)
		case "logout":
			b.handleLogout(msg)
		case "help":
			b.handleHelp(msg)
		case "privacy":
			b.handlePrivacy(msg)
		case "add":
			b.handleAddPremium(msg)
		case "rm":
			b.handleRemovePremium(msg)
		case "listpre":
			b.handleListPremium(msg)
		case "listuser":
			b.handleListUsers(msg)
		case "check":
			b.handleCheckUser(msg)
		case "getlink":
			b.handleGetLink(msg)
		case "get":
			b.handleGetCommand(msg, user)
		case "accounts":
			b.handleAccounts(msg)
		case "addaccount":
			b.startLogin(msg.Chat.ID)
		case "clearall":
			b.handleClearAll(msg)
		case "login":
			b.handleLoginCommand(msg)
		}
		return
	}

	// 2. Handle multi-step login/input steps
	if user != nil && user.LoginStep != "" && user.LoginStep != "awaiting_app_id" {
		b.DB.UpdateUser(ctx, msg.Chat.ID, bson.M{"$set": bson.M{"lastUsed": time.Now().UnixMilli()}})
		b.handleLoginStep(msg, user)
		return
	}

	if msg.Text != "" {
		b.DB.UpdateUser(ctx, msg.Chat.ID, bson.M{"$set": bson.M{"lastUsed": time.Now().UnixMilli()}})
	}

	// 3. Auto-detect App Store links / app IDs — show version selection menu
	if user != nil && user.AppleID != "" && msg.Text != "" {
		if appID := b.extractAppID(msg.Text); appID != "" {
			b.handleDownloadRequest(msg)
			return
		}
	}

	// 4. Not logged in
	if user == nil || user.AppleID == "" {
		if msg.Text != "" {
			reply := tgbotapi.NewMessage(msg.Chat.ID, "Vui lòng đăng nhập trước khi tải IPA. 🔑\nDùng /start để bắt đầu.")
			reply.ParseMode = "HTML"
			reply.ReplyToMessageID = msg.MessageID
			b.SafeSend(reply)
		}
	}
}

func (b *Bot) handleStart(msg *tgbotapi.Message) {
	ctx := context.Background()

	// Reset session state
	b.DB.UpdateUser(ctx, msg.Chat.ID, bson.M{"$set": bson.M{"loginStep": ""}})
	b.DB.UnsetFields(ctx, msg.Chat.ID, bson.M{"tempAppleId": "", "password": ""})

	// Cleanup any leftover bot prompts
	if promptID, ok := b.State.Load(fmt.Sprintf("prompt_%d", msg.Chat.ID)); ok {
		b.SafeDelete(msg.Chat.ID, promptID.(int))
		b.State.Delete(fmt.Sprintf("prompt_%d", msg.Chat.ID))
	}

	user, _ := b.DB.GetUser(ctx, msg.Chat.ID)

	if user == nil {
		user = &db.User{
			ID:         msg.Chat.ID,
			FirstName:  msg.From.FirstName,
			Username:   msg.From.UserName,
			CreatedAt:  time.Now().UnixMilli(),
			UsageCount: 0,
		}
		b.DB.SaveUser(ctx, user)
	}

	greeting := msg.From.FirstName
	if greeting == "" {
		greeting = msg.From.UserName
	}
	if greeting == "" {
		greeting = "bạn"
	}

	var text string
	var keyboard tgbotapi.InlineKeyboardMarkup

	if user.AppleID == "" {
		// Welcome cho user chưa đăng nhập
		text = fmt.Sprintf(
			"👋 <b>Xin chào %s!</b>\n\n"+
				"Bot giúp bạn tải file <b>IPA</b> trực tiếp từ App Store về Telegram.\n\n"+
				"🔑 <b>Bắt đầu:</b> Đăng nhập Apple ID để tải IPA\n"+
				"📲 <b>Tải app:</b> Gửi link App Store hoặc dùng /get\n"+
				"👥 <b>Nhiều tài khoản:</b> Thêm/chuyển Apple ID dễ dàng\n\n"+
				"<i>🔒 Tài khoản của bạn được mã hoá lưu trữ. Xem /privacy.</i>",
			html.EscapeString(greeting),
		)
		keyboard = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔑 Đăng nhập Apple ID", "login"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("ℹ️ Trợ giúp", "help"),
				tgbotapi.NewInlineKeyboardButtonData("🔒 Bảo mật", "privacy"),
			),
		)
	} else {
		// Status cho user đã đăng nhập
		premiumStatus := "Thường"
		if user.IsPremium {
			premiumStatus = "🌟 Premium"
			if user.PremiumExpiry != nil {
				premiumStatus += fmt.Sprintf(" (đến %s)", user.PremiumExpiry.Format("02/01/2006"))
			} else {
				premiumStatus += " (vĩnh viễn)"
			}
		}

		accountCount := len(user.Accounts)
		if accountCount == 0 && user.AppleID != "" {
			accountCount = 1
		}

		text = fmt.Sprintf(
			"👋 <b>Xin chào %s!</b>\n\n"+
				"📧 <b>Apple ID đang dùng:</b> <code>%s</code>\n"+
				"👥 <b>Số tài khoản:</b> %d\n"+
				"💎 <b>Gói:</b> %s\n"+
				"📦 <b>Đã tải:</b> %d lần\n\n"+
				"<i>💡 Gửi link App Store hoặc dùng /get để tải IPA.</i>",
			html.EscapeString(greeting),
			html.EscapeString(user.AppleID),
			accountCount,
			premiumStatus,
			user.UsageCount,
		)
		keyboard = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("📲 Tải IPA", "download"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("👥 Tài khoản", "accounts"),
				tgbotapi.NewInlineKeyboardButtonData("➕ Thêm tài khoản", "acc_add"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("ℹ️ Trợ giúp", "help"),
				tgbotapi.NewInlineKeyboardButtonData("🚪 Đăng xuất", "logout"),
			),
		)
	}

	reply := tgbotapi.NewMessage(msg.Chat.ID, text)
	reply.ParseMode = "HTML"
	reply.DisableWebPagePreview = true
	reply.ReplyMarkup = keyboard
	b.SafeSend(reply)
}

// handleClearAll xóa toàn bộ data của user (DB doc + keychain dir trên disk).
// Dùng để reset hoàn toàn — chủ yếu để test/troubleshoot.
func (b *Bot) handleClearAll(msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	ctx := context.Background()

	// Confirm bằng button: chỉ xóa khi user bấm xác nhận
	if msg.CommandArguments() != "confirm" {
		reply := tgbotapi.NewMessage(chatID, "⚠️ <b>Xóa toàn bộ dữ liệu của bạn?</b>\n\n"+
			"• Tất cả Apple IDs đã đăng nhập\n"+
			"• Lịch sử & cấu hình\n"+
			"• Phiên làm việc\n\n"+
			"Hành động này <b>không thể hoàn tác</b>. Gõ <code>/clearall confirm</code> để xác nhận.")
		reply.ParseMode = "HTML"
		b.SafeSend(reply)
		return
	}

	// Xóa keychain + DB
	b.IPATool.ResetSession(chatID)
	if _, err := b.DB.Users.DeleteOne(ctx, bson.M{"userId": chatID}); err != nil {
		log.Printf("⚠️ ClearAll DB delete user=%d: %v", chatID, err)
	}
	// Cũng xóa thư mục home của user (defensive — ResetSession mới chỉ xóa .ipatool/)
	homeDir := filepath.Join(b.IPATool.BaseDir, "users", fmt.Sprintf("%d", chatID))
	_ = os.RemoveAll(homeDir)

	reply := tgbotapi.NewMessage(chatID, "✅ <b>Đã xóa toàn bộ dữ liệu.</b>\nGửi /start để bắt đầu lại từ đầu. 🔄")
	reply.ParseMode = "HTML"
	b.SafeSend(reply)
}

func (b *Bot) handleLogout(msg *tgbotapi.Message) {
	ctx := context.Background()
	b.DB.UnsetFields(ctx, msg.Chat.ID, bson.M{"appleId": "", "password": ""})
	b.IPATool.ResetSession(msg.Chat.ID)
	reply := tgbotapi.NewMessage(msg.Chat.ID, "Đã đăng xuất và xóa phiên làm việc. 🚪")
	reply.ParseMode = "HTML"
	b.SafeSend(reply)
}

// handleGetCommand xử lý /get <link|appID> — tải thẳng phiên bản mới nhất, bỏ qua menu.
func (b *Bot) handleGetCommand(msg *tgbotapi.Message, user *db.User) {
	if user == nil || user.AppleID == "" {
		reply := tgbotapi.NewMessage(msg.Chat.ID, "Vui lòng đăng nhập trước khi tải IPA. 🔑\nDùng /start để bắt đầu.")
		reply.ParseMode = "HTML"
		reply.ReplyToMessageID = msg.MessageID
		b.SafeSend(reply)
		return
	}

	arg := msg.CommandArguments()
	if arg == "" {
		reply := tgbotapi.NewMessage(msg.Chat.ID, "📲 <b>Cách dùng:</b>\n<code>/get &lt;link App Store hoặc AppID&gt;</code>\n\nVí dụ:\n<code>/get https://apps.apple.com/app/id6446659989</code>\n<code>/get 6446659989</code>")
		reply.ParseMode = "HTML"
		reply.ReplyToMessageID = msg.MessageID
		b.SafeSend(reply)
		return
	}

	appID := b.extractAppID(arg)
	if appID == "" {
		reply := tgbotapi.NewMessage(msg.Chat.ID, "❌ <b>Không tìm thấy App ID hợp lệ.</b>\nGửi link App Store hoặc số App ID.")
		reply.ParseMode = "HTML"
		reply.ReplyToMessageID = msg.MessageID
		b.SafeSend(reply)
		return
	}

	b.State.Store(fmt.Sprintf("origin_%d", msg.Chat.ID), msg.MessageID)
	b.executeDownload(msg.Chat.ID, appID, "")
}

func (b *Bot) handleHelp(msg *tgbotapi.Message) {
	text := `ℹ️ <b>Hướng dẫn sử dụng:</b>

<b>🔑 Đăng nhập:</b>
• <code>/login &lt;email&gt; &lt;password&gt;</code> — nhanh 1 dòng (bot tự xóa tin nhắn)
• Hoặc /start → bấm "Đăng nhập" → nhập từng bước

<b>📲 Tải IPA:</b>
• Gửi link App Store: <code>https://apps.apple.com/...</code>
• Hoặc <code>/get &lt;link|AppID&gt;</code> tải nhanh phiên bản mới nhất
• Sau khi tải, /getlink để nhận link cài trực tiếp lên iPhone

<b>👥 Quản lý Apple ID:</b>
• /accounts — xem & chuyển đổi tài khoản
• /addaccount — thêm Apple ID mới
• /logout — đăng xuất tài khoản đang dùng
• /clearall — xóa <i>toàn bộ</i> dữ liệu (DB + keychain)

<b>📏 Giới hạn dung lượng:</b>
• Thường: 2GB
• Premium: 4GB

<b>🔒 Khác:</b>
• /privacy — chính sách bảo mật
• /start — quay về menu chính`
	reply := tgbotapi.NewMessage(msg.Chat.ID, text)
	reply.ParseMode = "HTML"
	reply.DisableWebPagePreview = true
	reply.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅️ Quay lại", "back_start"),
		),
	)
	b.SafeSend(reply)
}

func (b *Bot) handlePrivacy(msg *tgbotapi.Message) {
	text := `🔒 <b>Chính sách bảo mật:</b>

• Tất cả thông tin đăng nhập đều gửi <b>thẳng tới Apple</b> để xử lý — bot chỉ là cầu nối.
• Apple ID có thể bị khoá nếu nhập sai mật khẩu nhiều lần (cơ chế bảo vệ của Apple, không phải lỗi bot).
• Khuyến khích dùng <b>Apple ID phụ</b> nếu chưa tin tưởng.

<b>📂 Mã nguồn mở:</b>
Toàn bộ source code của bot sẽ được public rõ ràng trên GitHub để bạn tự kiểm tra. Bạn có thể tự host bot riêng nếu muốn.

<b>⚠️ Miễn trừ trách nhiệm:</b>
Bot không chịu trách nhiệm nếu có vấn đề xảy ra với tài khoản Apple ID của bạn.`
	reply := tgbotapi.NewMessage(msg.Chat.ID, text)
	reply.ParseMode = "HTML"
	reply.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅️ Quay lại", "back_start"),
		),
	)
	b.SafeSend(reply)
}
