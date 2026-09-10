package middleware

import (
	"context"
	"testing"
	"time"
)

func TestNewTimeoutContextPositive(t *testing.T) {
	ctx, cancel := newTimeoutContext(context.Background(), 50*time.Millisecond)
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatalf("expected deadline to be set")
	}
	if time.Until(deadline) > 100*time.Millisecond {
		t.Errorf("deadline too far in the future: %v", time.Until(deadline))
	}
}

func TestNewTimeoutContextZero(t *testing.T) {
	parent := context.Background()
	ctx, cancel := newTimeoutContext(parent, 0)
	defer cancel()
	if ctx != parent {
		t.Errorf("expected parent context returned unchanged when deadline <= 0")
	}
	if _, ok := ctx.Deadline(); ok {
		t.Errorf("expected no deadline when deadline=0")
	}
}