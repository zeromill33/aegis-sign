package signerapi

import (
	"context"
	"testing"
	"time"

	"github.com/aegis-sign/wallet/internal/app/backend/keycache"
	"github.com/stretchr/testify/require"
)

type stubUnlockQueue struct {
	lastEvent keycache.UnlockEvent
}

func (s *stubUnlockQueue) NotifyUnlock(ctx context.Context, event keycache.UnlockEvent) error {
	s.lastEvent = event
	return nil
}

func TestUnlockResponderEnqueue(t *testing.T) {
	queue := &stubUnlockQueue{}
	responder := NewUnlockResponder(UnlockResponderConfig{
		Queue:    queue,
		Keyspace: "prod",
		MinRetry: 50 * time.Millisecond,
		MaxRetry: 50 * time.Millisecond,
	})

	meta := responder.Handle(context.Background(), "key-1", keycache.NewUnlockRequiredError("dek", 100*time.Millisecond))

	require.Equal(t, "prod", queue.lastEvent.Keyspace)
	require.Equal(t, "key-1", queue.lastEvent.KeyID)
	require.Equal(t, "dek", queue.lastEvent.Reason)
	require.NotEmpty(t, meta.RequestID)
	require.Equal(t, 50*time.Millisecond, meta.RetryAfter)
}
