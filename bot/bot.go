package bot

import (
	"context"
	"encoding/json"
	"log"
	"path/filepath"
	"sync"
	"time"

	"ipa-downloader-bot/config"
	"ipa-downloader-bot/db"
	"ipa-downloader-bot/pkg/crypto"
	"ipa-downloader-bot/pkg/ipatool"
	"ipa-downloader-bot/pkg/mtproto"
	"ipa-downloader-bot/pkg/ratelimit"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Bot struct {
	API         *tgbotapi.BotAPI
	DB          *db.MongoDB
	Config      *config.Config
	IPATool     *ipatool.Client
	MTProto     *mtproto.Client
	Crypto      *crypto.Cipher    // mã hoá password trước khi lưu DB
	LoginLimit  *ratelimit.Limiter // chống brute-force login
	ActionLimit *ratelimit.Limiter // chống spam tổng quát (download, switch, ...)
	State       sync.Map          // multi-step state per user
}

func NewBot(cfg *config.Config, database *db.MongoDB, cipher *crypto.Cipher) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(cfg.BotToken)
	if err != nil {
		return nil, err
	}

	absBaseDir, _ := filepath.Abs("./tmp")
	ipaClient := ipatool.NewClient(absBaseDir)
	mtprotoClient := mtproto.NewClient(cfg.ApiID, cfg.ApiHash, cfg.BotToken)

	return &Bot{
		API:         api,
		DB:          database,
		Config:      cfg,
		IPATool:     ipaClient,
		MTProto:     mtprotoClient,
		Crypto:      cipher,
		LoginLimit:  ratelimit.New(5, 10*time.Minute),  // 5 lần login fail / 10 phút
		ActionLimit: ratelimit.New(30, 1*time.Minute),  // 30 actions / phút
	}, nil
}

// userIDOf trả Telegram user ID của người gửi message.
// Trong private chat (DM), giá trị này == msg.Chat.ID.
// Trong group/supergroup, msg.From.ID khác msg.Chat.ID — phải dùng From.ID
// để xác định danh tính user (không bị trộn lẫn với group khác).
func userIDOf(msg *tgbotapi.Message) int64 {
	if msg.From != nil {
		return msg.From.ID
	}
	return msg.Chat.ID // fallback (channel post case)
}

// userIDOfCallback trả Telegram user ID của người bấm button.
// query.From là user click, query.Message.Chat là nơi button xuất hiện.
func userIDOfCallback(query *tgbotapi.CallbackQuery) int64 {
	if query.From != nil {
		return query.From.ID
	}
	return query.Message.Chat.ID
}

// isPrivateChat kiểm tra message có đến từ DM (private chat) hay không.
// Login PHẢI làm trong DM để password không lộ trong group.
func isPrivateChat(msg *tgbotapi.Message) bool {
	return msg.Chat != nil && msg.Chat.Type == "private"
}

// encPwd helper: mã hoá password để lưu DB. Trả "" cho input rỗng.
func (b *Bot) encPwd(plain string) string {
	if plain == "" {
		return ""
	}
	enc, err := b.Crypto.Encrypt(plain)
	if err != nil {
		log.Printf("⚠️ encrypt password failed: %v", err)
		return plain // fallback — không nên xảy ra
	}
	return enc
}

// decPwd helper: giải mã password đọc từ DB. Backward compat: nếu chuỗi không
// phải ciphertext (legacy plaintext) thì trả về nguyên xi.
func (b *Bot) decPwd(stored string) string {
	if stored == "" {
		return ""
	}
	if !crypto.IsEncrypted(stored) {
		// Legacy plaintext — giải mã không cần
		return stored
	}
	plain, err := b.Crypto.Decrypt(stored)
	if err != nil {
		log.Printf("⚠️ decrypt password failed: %v", err)
		return stored
	}
	return plain
}

func (b *Bot) Connect(ctx context.Context) error {
	return b.MTProto.Connect(ctx)
}

func (b *Bot) Stop() {
	b.MTProto.Disconnect()
}

func (b *Bot) Start(ctx context.Context) {
	log.Printf("🤖 Bot started as %s", b.API.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := b.API.GetUpdatesChan(u)

	for {
		select {
		case <-ctx.Done():
			log.Println("🤖 Bot stopping...")
			return
		case update, ok := <-updates:
			if !ok {
				return
			}
			go func(upd tgbotapi.Update) {
				// Bọc recover để 1 user gây panic không kéo theo cả bot.
				defer func() {
					if r := recover(); r != nil {
						var uid int64
						if upd.Message != nil {
							uid = upd.Message.Chat.ID
						} else if upd.CallbackQuery != nil && upd.CallbackQuery.Message != nil {
							uid = upd.CallbackQuery.Message.Chat.ID
						}
						log.Printf("‼️ [update handler] panic recovered (user=%d): %v", uid, r)
					}
				}()
				if upd.Message != nil {
					b.HandleMessage(upd.Message)
				} else if upd.CallbackQuery != nil {
					b.HandleCallback(upd.CallbackQuery)
				}
			}(update)
		}
	}
}

func (b *Bot) SafeSend(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	msg, err := b.API.Send(c)
	if err != nil {
		log.Printf("❌ Error sending message: %v", err)
	}
	return msg, err
}

// SafeDelete xóa message; dùng Request thay Send vì Telegram trả `true` (bool)
// chứ không phải Message object — nếu dùng Send sẽ lỗi unmarshal spam log.
func (b *Bot) SafeDelete(chatID int64, messageID int) {
	if messageID == 0 {
		return
	}
	_, _ = b.API.Request(tgbotapi.NewDeleteMessage(chatID, messageID))
}

func (b *Bot) SafeEdit(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	msg, err := b.API.Request(c)
	if err != nil {
		log.Printf("❌ Error editing message: %v", err)
		return tgbotapi.Message{}, err
	}
	var message tgbotapi.Message
	if err := json.Unmarshal(msg.Result, &message); err != nil {
		return message, nil
	}
	return message, nil
}
