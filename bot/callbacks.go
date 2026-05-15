package bot

import (
	"context"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.mongodb.org/mongo-driver/bson"
)

func (b *Bot) HandleCallback(query *tgbotapi.CallbackQuery) {
	chatID := query.Message.Chat.ID
	msgID := query.Message.MessageID
	userID := userIDOfCallback(query)

	// Update lastUsed để cleanup goroutine không nhầm xóa session.
	b.DB.UpdateUser(context.Background(), userID, bson.M{"$set": bson.M{"lastUsed": time.Now().UnixMilli()}})

	// Wrap query.Message với From của người click — để các handler sau dùng userIDOf(msg)
	// trả về đúng người click chứ không phải bot.
	pseudoMsg := *query.Message
	pseudoMsg.From = query.From

	// Login phải DM. Nếu không phải private chat → redirect.
	loginActions := map[string]bool{"login": true, "acc_add": true}
	if loginActions[query.Data] && pseudoMsg.Chat != nil && pseudoMsg.Chat.Type != "private" {
		b.replyDMRequired(&pseudoMsg)
		b.API.Request(tgbotapi.NewCallback(query.ID, "Vui lòng login trong DM"))
		return
	}

	switch query.Data {
	case "login":
		b.SafeDelete(chatID, msgID)
		b.startLogin(chatID, userID)
	case "download":
		b.SafeDelete(chatID, msgID)
		b.promptDownload(chatID, userID)
	case "logout":
		b.SafeDelete(chatID, msgID)
		b.handleLogout(&pseudoMsg)
	case "acc_add":
		b.SafeDelete(chatID, msgID)
		b.startLogin(chatID, userID)
	case "accounts":
		b.SafeDelete(chatID, msgID)
		b.handleAccounts(&pseudoMsg)
	case "help":
		b.SafeDelete(chatID, msgID)
		b.handleHelp(&pseudoMsg)
	case "privacy":
		b.SafeDelete(chatID, msgID)
		b.handlePrivacy(&pseudoMsg)
	case "back_start":
		b.SafeDelete(chatID, msgID)
		b.handleStart(&pseudoMsg)
	}

	if strings.HasPrefix(query.Data, "download_latest_") {
		appID := strings.TrimPrefix(query.Data, "download_latest_")
		b.SafeDelete(chatID, msgID)
		b.executeDownload(chatID, userID, appID, "")
	} else if strings.HasPrefix(query.Data, "download_old_") {
		appID := strings.TrimPrefix(query.Data, "download_old_")
		b.SafeDelete(chatID, msgID)
		b.promptOldVersion(chatID, userID, appID)
	} else if strings.HasPrefix(query.Data, "acc_use_") {
		idxStr := strings.TrimPrefix(query.Data, "acc_use_")
		idx, _ := strconv.Atoi(idxStr)
		b.SafeDelete(chatID, msgID)
		b.switchAccount(chatID, userID, idx)
	} else if strings.HasPrefix(query.Data, "acc_rm_") {
		idxStr := strings.TrimPrefix(query.Data, "acc_rm_")
		idx, _ := strconv.Atoi(idxStr)
		b.SafeDelete(chatID, msgID)
		b.removeAccount(chatID, userID, idx)
	}

	b.API.Request(tgbotapi.NewCallback(query.ID, ""))
}
