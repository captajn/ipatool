// Package ratelimit cung cấp token bucket per-user đơn giản, dùng để chống
// brute-force login và spam request nặng (tải IPA, switch account, v.v.).
//
// Cách dùng:
//
//	rl := ratelimit.New(5, time.Minute)         // 5 lần / phút / user
//	if !rl.Allow(userID) {
//		// từ chối, báo "too many requests"
//	}
package ratelimit

import (
	"sync"
	"time"
)

// Limiter giới hạn số lần action / window cho từng userID.
type Limiter struct {
	mu      sync.Mutex
	max     int           // số lần tối đa trong window
	window  time.Duration // khoảng thời gian
	buckets map[int64]*bucket
}

type bucket struct {
	count int
	reset time.Time
}

// New tạo Limiter mới. Dùng riêng cho từng "loại" action (1 cho login, 1 cho download...).
func New(max int, window time.Duration) *Limiter {
	return &Limiter{
		max:     max,
		window:  window,
		buckets: make(map[int64]*bucket),
	}
}

// Allow trả true nếu user còn quota, false nếu đã vượt limit.
// Khi false, RetryAfter() trả thời gian phải đợi.
func (l *Limiter) Allow(userID int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, ok := l.buckets[userID]
	if !ok || now.After(b.reset) {
		l.buckets[userID] = &bucket{count: 1, reset: now.Add(l.window)}
		return true
	}
	if b.count >= l.max {
		return false
	}
	b.count++
	return true
}

// RetryAfter trả thời gian còn lại trước khi user có thể thử lại.
func (l *Limiter) RetryAfter(userID int64) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if b, ok := l.buckets[userID]; ok {
		if d := time.Until(b.reset); d > 0 {
			return d
		}
	}
	return 0
}

// Reset xóa quota của 1 user (vd: khi login thành công, reset login limiter).
func (l *Limiter) Reset(userID int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, userID)
}

// Cleanup xóa các bucket đã hết hạn — chạy định kỳ để tránh memory leak.
func (l *Limiter) Cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for id, b := range l.buckets {
		if now.After(b.reset) {
			delete(l.buckets, id)
		}
	}
}
