package bot

import (
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func (b *Bot) HandleCallback(query *tgbotapi.CallbackQuery) {
	chatID := query.Message.Chat.ID
	msgID := query.Message.MessageID

	switch query.Data {
	case "login":
		b.SafeDelete(chatID, msgID)
		b.startLogin(chatID)
	case "download":
		b.SafeDelete(chatID, msgID)
		b.promptDownload(chatID)
	case "logout":
		b.SafeDelete(chatID, msgID)
		b.handleLogout(query.Message)
	case "acc_add":
		b.SafeDelete(chatID, msgID)
		b.startLogin(chatID)
	case "accounts":
		b.SafeDelete(chatID, msgID)
		b.handleAccounts(query.Message)
	case "help":
		b.SafeDelete(chatID, msgID)
		b.handleHelp(query.Message)
	case "privacy":
		b.SafeDelete(chatID, msgID)
		b.handlePrivacy(query.Message)
	case "back_start":
		b.SafeDelete(chatID, msgID)
		b.handleStart(query.Message)
	}

	if strings.HasPrefix(query.Data, "download_latest_") {
		appID := strings.TrimPrefix(query.Data, "download_latest_")
		b.SafeDelete(chatID, msgID)
		b.executeDownload(chatID, appID, "")
	} else if strings.HasPrefix(query.Data, "download_old_") {
		appID := strings.TrimPrefix(query.Data, "download_old_")
		b.SafeDelete(chatID, msgID)
		b.promptOldVersion(chatID, appID)
	} else if strings.HasPrefix(query.Data, "acc_use_") {
		idxStr := strings.TrimPrefix(query.Data, "acc_use_")
		idx, _ := strconv.Atoi(idxStr)
		b.SafeDelete(chatID, msgID)
		b.switchAccount(chatID, idx)
	} else if strings.HasPrefix(query.Data, "acc_rm_") {
		idxStr := strings.TrimPrefix(query.Data, "acc_rm_")
		idx, _ := strconv.Atoi(idxStr)
		b.SafeDelete(chatID, msgID)
		b.removeAccount(chatID, idx)
	}

	b.API.Request(tgbotapi.NewCallback(query.ID, ""))
}
