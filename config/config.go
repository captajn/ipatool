package config

import (
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	BotToken      string
	ApiID         int
	ApiHash       string
	MongoURI      string
	AdminID       int64
	WebPort       string
	HostURL       string
	EncryptionKey string // 32 bytes base64, mã hoá password trước khi lưu DB

	// Optimization & Limits
	DownloadLimitNormal  int64
	DownloadLimitPremium int64
	CooldownDuration     int64 // in minutes
	SessionTimeout       int64 // in minutes
}

func LoadConfig() *Config {
	err := godotenv.Load()
	if err != nil {
		log.Println("Warning: .env file not found, using system environment variables")
	}

	apiID, _ := strconv.Atoi(os.Getenv("API_ID"))
	adminID, _ := strconv.ParseInt(os.Getenv("ADMIN_ID"), 10, 64)

	// Limits & Timeouts (with defaults)
	dlLimitNormal, _ := strconv.ParseInt(getEnv("DOWNLOAD_LIMIT_NORMAL", "2048"), 10, 64)
	dlLimitPremium, _ := strconv.ParseInt(getEnv("DOWNLOAD_LIMIT_PREMIUM", "4096"), 10, 64)
	cooldown, _ := strconv.ParseInt(getEnv("COOLDOWN_DURATION", "15"), 10, 64)
	sessionTimeout, _ := strconv.ParseInt(getEnv("SESSION_TIMEOUT", "5"), 10, 64)

	return &Config{
		BotToken:      os.Getenv("BOT_TOKEN"),
		ApiID:         apiID,
		ApiHash:       os.Getenv("API_HASH"),
		MongoURI:      os.Getenv("MONGODB_URI"),
		AdminID:       adminID,
		WebPort:       os.Getenv("WEB_PORT"),
		HostURL:       os.Getenv("HOST_URL"),
		EncryptionKey: os.Getenv("ENCRYPTION_KEY"),

		DownloadLimitNormal:  dlLimitNormal * 1024 * 1024,  // Convert to bytes
		DownloadLimitPremium: dlLimitPremium * 1024 * 1024, // Convert to bytes
		CooldownDuration:     cooldown,
		SessionTimeout:       sessionTimeout,
	}
}

func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}
