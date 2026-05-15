package ratelimit

import (
	"testing"
	"time"
)

func TestAllowWithinLimit(t *testing.T) {
	rl := New(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !rl.Allow(123) {
			t.Errorf("call %d: expected allow", i+1)
		}
	}
	if rl.Allow(123) {
		t.Error("expected reject after limit")
	}
}

func TestSeparateUsers(t *testing.T) {
	rl := New(1, time.Minute)
	if !rl.Allow(1) {
		t.Error("user 1 should be allowed")
	}
	if !rl.Allow(2) {
		t.Error("user 2 should be allowed (separate bucket)")
	}
	if rl.Allow(1) {
		t.Error("user 1 should be rejected")
	}
}

func TestWindowReset(t *testing.T) {
	rl := New(1, 50*time.Millisecond)
	if !rl.Allow(1) {
		t.Error("first call should allow")
	}
	if rl.Allow(1) {
		t.Error("second call should reject")
	}
	time.Sleep(60 * time.Millisecond)
	if !rl.Allow(1) {
		t.Error("after window expires, should allow")
	}
}

func TestReset(t *testing.T) {
	rl := New(1, time.Minute)
	rl.Allow(1)
	rl.Reset(1)
	if !rl.Allow(1) {
		t.Error("after Reset, should allow again")
	}
}
