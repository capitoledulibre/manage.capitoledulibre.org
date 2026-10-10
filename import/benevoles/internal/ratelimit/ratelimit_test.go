package ratelimit

import (
	"testing"
	"time"
)

func TestAllow(t *testing.T) {
	l := New(2, time.Hour)
	if !l.Allow("a") || !l.Allow("a") {
		t.Fatal("first two events must pass")
	}
	if l.Allow("a") {
		t.Fatal("third event must be refused")
	}
	if !l.Allow("b") {
		t.Fatal("other keys are independent")
	}
}
