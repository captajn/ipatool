// Một-shot tool: xoá toàn bộ data của 1 user trên DB + keychain để test lại từ đầu.
// Cách dùng:  go run ./cmd/reset <chatID>
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"ipa-downloader-bot/config"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("Usage: go run ./cmd/reset <chatID>")
	}
	chatID, err := strconv.ParseInt(os.Args[1], 10, 64)
	if err != nil {
		log.Fatalf("Invalid chatID: %v", err)
	}

	cfg := config.LoadConfig()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(cfg.MongoURI))
	if err != nil {
		log.Fatalf("Mongo connect: %v", err)
	}
	defer client.Disconnect(ctx)

	users := client.Database("ipa-downloader-bot-new").Collection("users")
	result, err := users.DeleteOne(ctx, bson.M{"userId": chatID})
	if err != nil {
		log.Fatalf("Delete: %v", err)
	}
	fmt.Printf("✅ DB: deleted %d document(s) for userId=%d\n", result.DeletedCount, chatID)

	// Xóa keychain dir
	homeDir := filepath.Join("tmp", "users", strconv.FormatInt(chatID, 10))
	if err := os.RemoveAll(homeDir); err != nil {
		log.Printf("⚠️ Remove %s: %v", homeDir, err)
	} else {
		fmt.Printf("✅ Disk: removed %s\n", homeDir)
	}
}
