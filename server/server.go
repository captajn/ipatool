package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// safeFilenameRe chỉ cho phép tên file có chữ cái, số, dấu chấm, gạch ngang, gạch dưới.
// KHÔNG cho phép `/`, `\`, `..` (chống path traversal).
var safeFilenameRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

type Server struct {
	httpServer *http.Server
}

func Start(port string, publicDir string, hostURL string) *Server {
	r := gin.Default()

	// Ensure public directory exists
	absPublicDir, _ := filepath.Abs(publicDir)
	os.MkdirAll(absPublicDir, 0755)
	
	r.StaticFS("/public", http.Dir(absPublicDir))

	// Redirect handler for /i/:filename
	r.GET("/i/:filename", func(c *gin.Context) {
		filename := c.Param("filename")

		// SECURITY: Path traversal protection — chỉ cho phép tên file safe.
		if !safeFilenameRe.MatchString(filename) || strings.Contains(filename, "..") {
			c.String(http.StatusBadRequest, "Invalid filename")
			return
		}

		// Double-check: path resolved phải nằm trong absPublicDir
		filePath := filepath.Join(absPublicDir, filename)
		cleanPath, err := filepath.Abs(filePath)
		if err != nil || !strings.HasPrefix(cleanPath, absPublicDir+string(filepath.Separator)) {
			c.String(http.StatusBadRequest, "Invalid path")
			return
		}

		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.String(http.StatusNotFound, `
<!DOCTYPE html>
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
