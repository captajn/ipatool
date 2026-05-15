package itunes

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type AppInfo struct {
	TrackName        string `json:"trackName"`
	ArtistName       string `json:"artistName"`
	PrimaryGenreName string `json:"primaryGenreName"`
	ArtworkUrl512    string `json:"artworkUrl512"`
	ArtworkUrl100    string `json:"artworkUrl100"`
	Description      string `json:"description"`
	BundleId         string `json:"bundleId"`
	Version          string `json:"version"`
	FileSizeBytes    string `json:"fileSizeBytes"`
}

type Response struct {
	ResultCount int       `json:"resultCount"`
	Results     []AppInfo `json:"results"`
}

// Danh sách quốc gia phổ biến, xoay vòng để chắc chắn lấy được info
// dù app chỉ có ở một vùng nhất định.
var defaultCountries = []string{
	"us", "vn", "jp", "gb", "de", "fr", "kr", "cn", "sg", "th", "au", "tw", "ca", "in",
}

var (
	httpClient = &http.Client{Timeout: 6 * time.Second}
	cache      sync.Map // map[appID]*cachedEntry
)

type cachedEntry struct {
	info *AppInfo
	at   time.Time
}

const cacheTTL = 10 * time.Minute

// GetAppInfo trả về thông tin app, thử lần lượt các quốc gia
// cho tới khi có kết quả. Có cache trong RAM 10 phút.
func GetAppInfo(appID string) (*AppInfo, error) {
	if v, ok := cache.Load(appID); ok {
		if e, ok := v.(*cachedEntry); ok && time.Since(e.at) < cacheTTL {
			return e.info, nil
		}
	}

	var lastErr error
	for _, country := range defaultCountries {
		info, err := lookupOne(appID, country)
		if err != nil {
			lastErr = err
			continue
		}
		if info != nil {
			cache.Store(appID, &cachedEntry{info: info, at: time.Now()})
			return info, nil
		}
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("app not found in any region")
}

func lookupOne(appID, country string) (*AppInfo, error) {
	u := fmt.Sprintf("https://itunes.apple.com/lookup?id=%s&country=%s", url.QueryEscape(appID), url.QueryEscape(country))
	resp, err := httpClient.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var data Response
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	if data.ResultCount == 0 || len(data.Results) == 0 {
		return nil, nil
	}
	return &data.Results[0], nil
}
