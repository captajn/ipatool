package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"ipa-downloader-bot/bot"
	"ipa-downloader-bot/config"
	"ipa-downloader-bot/db"
	"ipa-downloader-bot/pkg/crypto"
	"ipa-downloader-bot/server"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.mongodb.org/mongo-driver/bson"
)

func main() {
	// 1. Load Config
	cfg := config.LoadConfig()
	if cfg.BotToken == "" {
		log.Fatal("BOT_TOKEN is required")
	}

	// 2a. Init encryption (fail-fast nếu key missing — KHÔNG để bot chạy với key default)
	if cfg.EncryptionKey == "" {
		newKey, _ := crypto.GenerateKey()
		log.Println("❌ ENCRYPTION_KEY is required to protect user passwords in DB.")
		log.Println("📋 Generated a new key for you. Add this line to your .env:")
		log.Printf("    ENCRYPTION_KEY=%s\n", newKey)
		log.Fatal("Bot will not start without ENCRYPTION_KEY.")
	}
	cipher, err := crypto.New(cfg.EncryptionKey)
	if err != nil {
		log.Fatalf("Invalid ENCRYPTION_KEY: %v", err)
	}
	log.Println("🔐 Encryption initialized (AES-256-GCM)")

	// 2b. Connect DB
	mongoDB, err := db.Connect(cfg.MongoURI)
	if err != nil {
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	log.Println("📦 Connected to MongoDB")

	// 3. Ensure tmp directory exists + purge leftover downloads (from previous crashes)
	if err := os.MkdirAll("tmp", 0755); err != nil {
		log.Fatalf("Failed to create tmp directory: %v", err)
	}
	if err := os.RemoveAll("tmp/downloads"); err != nil {
		log.Printf("⚠️ Cleanup tmp/downloads failed: %v", err)
	} else {
		log.Println("🧹 Đã xóa tmp/downloads (leftover từ phiên trước)")
	}

	// 4. Start Web Server
	publicDir := "public"
	var webServer *server.Server
	if cfg.WebPort != "" {
		webServer = server.Start(cfg.WebPort, publicDir, cfg.HostURL)
	}

	// 5. Start Bot
	tgBot, err := bot.NewBot(cfg, mongoDB, cipher)
	if err != nil {
		log.Fatalf("Failed to initialize bot: %v", err)
	}

	// Connect MTProto
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := tgBot.Connect(ctx); err != nil {
		log.Printf("⚠️ Warning: Failed to connect MTProto: %v. Some features may not work.", err)
	} else {
		log.Println("🚀 MTProto connected")
	}

	// 6. Background Tasks

	// Combined Cleanup Task (Sessions + Files) — bọc recover để không bao giờ làm chết bot.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("‼️ [cleanup goroutine] panic recovered: %v", r)
			}
		}()

		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		lastFileCleanup := time.Now()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				func() {
					defer func() {
						if r := recover(); r != nil {
							log.Printf("‼️ [cleanup tick] panic recovered: %v", r)
						}
					}()

					// 6a. Session Cleanup (Optimized query: only fetch users that actually need cleanup)
					sessionCtx, sCancel := context.WithTimeout(ctx, 30*time.Second)
					defer sCancel()
					timeoutMs := cfg.SessionTimeout * 60 * 1000
					users, err := mongoDB.GetActiveSessionUsers(sessionCtx, timeoutMs)
					if err == nil {
						for _, u := range users {
							mongoDB.UpdateUser(sessionCtx, u.ID, bson.M{"$set": bson.M{"loginStep": ""}})
							mongoDB.UnsetFields(sessionCtx, u.ID, bson.M{"tempAppleId": "", "password": ""})

							if promptID, ok := tgBot.State.Load(fmt.Sprintf("prompt_%d", u.ID)); ok {
								if id, isInt := promptID.(int); isInt {
									tgBot.SafeDelete(u.ID, id)
								}
								tgBot.State.Delete(fmt.Sprintf("prompt_%d", u.ID))
							}

							tgBotMsg := tgbotapi.NewMessage(u.ID, "⏰ <b>Phiên làm việc đã hết hạn do bạn không hoạt động quá lâu.</b>\nVui lòng thao tác lại từ đầu. 🔄")
							tgBotMsg.ParseMode = "HTML"
							tgBot.API.Send(tgBotMsg)
							log.Printf("⏰ [Dọn dẹp] Đã xóa phiên làm việc hết hạn của người dùng %d", u.ID)
						}
					}

					// 6b. File & Directory Cleanup (Every hour)
					if time.Since(lastFileCleanup) > 1*time.Hour {
						cleanupStalePublicFiles(publicDir)
						tgBot.IPATool.CleanupUserDirectories(24 * time.Hour)
						lastFileCleanup = time.Now()
					}
				}()
			}
		}
	}()

	// 7. Start Bot in goroutine to allow signal handling
	go tgBot.Start(ctx)

	// 8. Graceful Shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	log.Println("🚀 Bot is running. Press Ctrl+C to stop.")
	<-stop

	log.Println("🛑 Shutting down...")
	cancel() // Cancel background tasks

	if webServer != nil {
		webServer.Stop()
	}
	tgBot.Stop()
	
	disconnectCtx, dCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dCancel()
	mongoDB.Client.Disconnect(disconnectCtx)

	log.Println("👋 Shutdown complete")
}

func cleanupStalePublicFiles(publicDir string) {
	os.MkdirAll(publicDir, 0755)
	files, err := os.ReadDir(publicDir)
	if err != nil {
		return
	}

	now := time.Now()
	for _, f := range files {
		info, err := f.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > 1*time.Hour {
			os.Remove(filepath.Join(publicDir, f.Name()))
			log.Printf("🧹 [Dọn dẹp] Đã xóa file quá hạn: %s", f.Name())
		}
	}
}
