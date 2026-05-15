package bot

import (
	"context"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.mongodb.org/mongo-driver/bson"
)

func (b *Bot) handleAddPremium(msg *tgbotapi.Message) {
	if msg.Chat.ID != b.Config.AdminID {
		return
	}
	parts := strings.Split(msg.Text, " ")
	if len(parts) < 2 {
		reply := tgbotapi.NewMessage(msg.Chat.ID, "Sử dụng: /add <userID> [months/f]")
		reply.ParseMode = "HTML"
		b.API.Send(reply)
		return
	}
	targetID, _ := strconv.ParseInt(parts[1], 10, 64)
	
	var expiry *time.Time
	if len(parts) > 2 && parts[2] != "f" {
		months, _ := strconv.Atoi(parts[2])
		t := time.Now().AddDate(0, months, 0)
		expiry = &t
	}

	ctx := context.Background()
	b.DB.UpdateUser(ctx, targetID, bson.M{"$set": bson.M{"isPremium": true, "premiumExpiry": expiry}})
	
	expiryText := "Vĩnh viễn"
	if expiry != nil {
		expiryText = expiry.Format("02/01/2006")
	}
	replyAdmin := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("Đã cấp Premium cho %d đến %s. 🎉", targetID, expiryText))
	replyAdmin.ParseMode = "HTML"
	b.API.Send(replyAdmin)

	replyUser := tgbotapi.NewMessage(targetID, fmt.Sprintf("Bạn đã được cấp Premium đến %s! 🎉", expiryText))
	replyUser.ParseMode = "HTML"
	b.API.Send(replyUser)
}

func (b *Bot) handleRemovePremium(msg *tgbotapi.Message) {
	if msg.Chat.ID != b.Config.AdminID {
		return
	}
	parts := strings.Split(msg.Text, " ")
	if len(parts) < 2 {
		return
	}
	targetID, _ := strconv.ParseInt(parts[1], 10, 64)

	ctx := context.Background()
	b.DB.UnsetFields(ctx, targetID, bson.M{"isPremium": "", "premiumExpiry": ""})
	reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("Đã xóa Premium của %d. 🗑️", targetID))
	reply.ParseMode = "HTML"
	b.API.Send(reply)
}

func (b *Bot) handleListPremium(msg *tgbotapi.Message) {
	if msg.Chat.ID != b.Config.AdminID {
		return
	}
	ctx := context.Background()
	users, _ := b.DB.GetPremiumUsers(ctx)
	
	text := "<b>Danh sách người dùng Premium:</b> 🎉\n"
	for _, u := range users {
		expiry := "Vĩnh viễn"
		if u.PremiumExpiry != nil {
			expiry = u.PremiumExpiry.Format("02/01/2006")
		}
		text += fmt.Sprintf("• ID: %d, Hết hạn: %s\n", u.ID, expiry)
	}
	reply := tgbotapi.NewMessage(msg.Chat.ID, text)
	reply.ParseMode = "HTML"
	b.API.Send(reply)
}

func (b *Bot) handleListUsers(msg *tgbotapi.Message) {
	if msg.Chat.ID != b.Config.AdminID {
		return
	}
	ctx := context.Background()
	users, _ := b.DB.GetAllUsers(ctx)
	
	content := "Danh sách người dùng:\n\n"
	for _, u := range users {
		content += fmt.Sprintf("ID: %d, Tên: %s, Premium: %v, AppleID: %s\n", u.ID, u.FirstName, u.IsPremium, u.AppleID)
	}

	path := filepath.Join("tmp", "users.txt")
	os.WriteFile(path, []byte(content), 0644)
	defer os.Remove(path)

	doc := tgbotapi.NewDocument(msg.Chat.ID, tgbotapi.FilePath(path))
	b.API.Send(doc)
}

func (b *Bot) handleCheckUser(msg *tgbotapi.Message) {
	if msg.Chat.ID != b.Config.AdminID {
		return
	}
	parts := strings.Split(msg.Text, " ")
	if len(parts) < 2 {
		return
	}
	targetID, _ := strconv.ParseInt(parts[1], 10, 64)

	ctx := context.Background()
	u, _ := b.DB.GetUser(ctx, targetID)
	if u == nil {
		reply := tgbotapi.NewMessage(msg.Chat.ID, "Không tìm thấy người dùng.")
		reply.ParseMode = "HTML"
		b.API.Send(reply)
		return
	}

	text := fmt.Sprintf("<b>Thông tin người dùng:</b>\nID: %d\nTên: %s\nPremium: %v\nAppleID: %s\nSử dụng: %d",
		u.ID, html.EscapeString(u.FirstName), u.IsPremium, html.EscapeString(u.AppleID), u.UsageCount)
	reply := tgbotapi.NewMessage(msg.Chat.ID, text)
	reply.ParseMode = "HTML"
	b.API.Send(reply)
}
