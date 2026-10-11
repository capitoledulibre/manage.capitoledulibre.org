package ratelimit

import (
	"testing"
	"time"
)

func TestAllow(t *testing.T) {
	l := New(2, time.Hour)

	if !l.Allow("a") || !l.Allow("a") {
		t.Fatal("the first two events pass")
	}

	if l.Allow("a") {
		t.Fatal("the third event is refused")
	}

	if !l.Allow("b") {
		t.Fatal("keys are independent")
	}
}
