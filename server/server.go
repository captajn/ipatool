package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// safeFilenameRe chỉ cho phép tên file có chữ cái, số, dấu chấm, gạch ngang, gạch dưới.
// KHÔNG cho phép `/`, `\`, `..` (chống path traversal).
var safeFilenameRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

type Server struct {
	httpServer *http.Server
}

// ipRateLimiter: per-IP token bucket đơn giản, chống DDoS / spam download.
// Mỗi IP: maxReq request / window thời gian. Mặc định 60/phút (đủ cho cài app
// bình thường, chặn bot/scraper).
type ipRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*ipBucket
	maxReq  int
	window  time.Duration
}

type ipBucket struct {
	count int
	reset time.Time
}

func newIPRateLimiter(maxReq int, window time.Duration) *ipRateLimiter {
	rl := &ipRateLimiter{
		buckets: make(map[string]*ipBucket),
		maxReq:  maxReq,
		window:  window,
	}
	// Cleanup goroutine: xóa bucket hết hạn mỗi 5 phút, tránh memory leak
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for range t.C {
			rl.cleanup()
		}
	}()
	return rl
}

func (rl *ipRateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	b, ok := rl.buckets[ip]
	if !ok || now.After(b.reset) {
		rl.buckets[ip] = &ipBucket{count: 1, reset: now.Add(rl.window)}
		return true
	}
	if b.count >= rl.maxReq {
		return false
	}
	b.count++
	return true
}

func (rl *ipRateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	for ip, b := range rl.buckets {
		if now.After(b.reset) {
			delete(rl.buckets, ip)
		}
	}
}

// clientIP lấy IP thật của client. Ưu tiên X-Forwarded-For / X-Real-IP nếu có
// (khi chạy sau reverse proxy nginx/cloudflare).
func clientIP(c *gin.Context) string {
	if ip := c.GetHeader("X-Real-IP"); ip != "" {
		return ip
	}
	if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
		if idx := strings.Index(xff, ","); idx > 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
	if err != nil {
		return c.Request.RemoteAddr
	}
	return host
}

// securityHeaders gắn HTTP headers chống XSS / clickjacking / sniffing.
func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		// CSP rất chặt: không inline JS, không external resource
		c.Header("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:")
		c.Next()
	}
}

// rateLimitMiddleware reject request nếu IP vượt quá quota.
func rateLimitMiddleware(rl *ipRateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := clientIP(c)
		if !rl.allow(ip) {
			c.Header("Retry-After", "60")
			c.String(http.StatusTooManyRequests, "Too many requests. Try again later.")
			c.Abort()
			return
		}
		c.Next()
	}
}

func Start(port string, publicDir string, hostURL string) *Server {
	r := gin.Default()

	// Apply security middleware GLOBALLY
	r.Use(securityHeaders())

	// Rate limit: 60 request / phút / IP cho download endpoints.
	// Cài 1 app IPA ~10-20 file requests (parsing manifest + download IPA + icon...)
	// → 60/phút đủ rộng cho user thật, chặn bot.
	rl := newIPRateLimiter(60, time.Minute)
	r.Use(rateLimitMiddleware(rl))

	// Public directory
	absPublicDir, _ := filepath.Abs(publicDir)
	os.MkdirAll(absPublicDir, 0755)

	// Static file serving (Gin built-in, đã safe khỏi path traversal)
	r.StaticFS("/public", http.Dir(absPublicDir))

	// Redirect handler for /i/:filename
	r.GET("/i/:filename", func(c *gin.Context) {
		filename := c.Param("filename")

		// SECURITY: Path traversal protection — chỉ cho phép tên file safe.
		if !safeFilenameRe.MatchString(filename) || strings.Contains(filename, "..") {
			c.String(http.StatusBadRequest, "Invalid filename")
			return
		}

		// Double-check: resolved path phải nằm trong absPublicDir
		filePath := filepath.Join(absPublicDir, filename)
		cleanPath, err := filepath.Abs(filePath)
		if err != nil || !strings.HasPrefix(cleanPath, absPublicDir+string(filepath.Separator)) {
			c.String(http.StatusBadRequest, "Invalid path")
			return
		}

		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.String(http.StatusNotFound, `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Link đã hết hạn</title>
    <style>
        body { font-family: -apple-system, system-ui, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif; display: flex; align-items: center; justify-content: center; height: 100vh; margin: 0; background-color: #f0f2f5; color: #1c1e21; }
        .card { background: white; padding: 2rem; border-radius: 12px; box-shadow: 0 4px 12px rgba(0,0,0,0.1); text-align: center; max-width: 400px; }
        h1 { color: #ff4d4f; font-size: 1.5rem; }
        p { line-height: 1.6; margin: 1.5rem 0; }
        .icon { font-size: 3rem; margin-bottom: 1rem; }
    </style>
</head>
<body>
    <div class="card">
        <div class="icon">⏰</div>
        <h1>Link đã hết hạn</h1>
        <p>Xin lỗi, link cài đặt này đã hết hạn hoặc file đã bị xóa khỏi máy chủ (mặc định sau 1 giờ).<br><br>Vui lòng dùng Bot để tạo lại link mới. 🔄</p>
    </div>
</body>
</html>`)
			return
		}

		directURL := fmt.Sprintf("%s/public/%s", hostURL, filename)
		installURL := fmt.Sprintf("https://dl.thuthuatjb.com/ipa/install.html?url=%s", url.QueryEscape(directURL))

		c.Redirect(http.StatusFound, installURL)
	})

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
		// Timeouts chống slow-loris attack
		ReadTimeout:       15 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      5 * time.Minute, // upload IPA có thể chậm
		IdleTimeout:       30 * time.Second,
	}

	log.Printf("🌍 Web Server starting on port %s", port)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start web server: %v", err)
		}
	}()

	return &Server{httpServer: srv}
}

func (s *Server) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.httpServer.Shutdown(ctx); err != nil {
		log.Printf("Web Server Shutdown Error: %v", err)
	}
	log.Println("🌍 Web Server stopped")
}
