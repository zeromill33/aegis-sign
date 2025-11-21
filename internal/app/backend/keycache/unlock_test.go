package keycache

import (
	"context"
	"testing"
)

type recorderNotifier struct {
	lastEvent UnlockEvent
}

func (r *recorderNotifier) NotifyUnlock(ctx context.Context, event UnlockEvent) error {
	r.lastEvent = event
	return nil
}

func (r *recorderNotifier) Ack(context.Context, UnlockResult) {}

func TestSetUnlockNotifier(t *testing.T) {
	recorder := &recorderNotifier{}
	SetUnlockNotifier(recorder)
	t.Cleanup(func() { SetUnlockNotifier(nil) })

	notifier := defaultUnlockNotifier()
	if err := notifier.NotifyUnlock(context.Background(), UnlockEvent{KeyID: "k"}); err != nil {
		t.Fatalf("notify failed: %v", err)
	}
	if recorder.lastEvent.KeyID != "k" {
		t.Fatalf("expected key recorded, got %s", recorder.lastEvent.KeyID)
	}
}
