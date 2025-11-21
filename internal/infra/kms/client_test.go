package kms

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClientRetriesUntilSuccess(t *testing.T) {
	provider := &fakeProvider{failures: 2}
	attestor := &fakeAttestor{}
	client, err := NewClient(provider, attestor, Config{InitialBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond})
	require.NoError(t, err)

	data, err := client.Decrypt(context.Background(), "key1", []byte("cipher"))
	require.NoError(t, err)
	require.Equal(t, "plain", string(data))
	require.Equal(t, int64(3), provider.decryptCalls.Load())
}

func TestClientFailsAfterMaxAttempts(t *testing.T) {
	provider := &fakeProvider{failures: 5}
	attestor := &fakeAttestor{}
	client, err := NewClient(provider, attestor, Config{MaxAttempts: 2, InitialBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond})
	require.NoError(t, err)

	_, err = client.GenerateDataKey(context.Background(), "key1")
	require.Error(t, err)
	require.GreaterOrEqual(t, provider.generateCalls.Load(), int64(2))
}

type fakeProvider struct {
	failures      int
	decryptCalls  atomic.Int64
	generateCalls atomic.Int64
}

func (f *fakeProvider) Decrypt(ctx context.Context, req DecryptRequest) ([]byte, error) {
	f.decryptCalls.Add(1)
	if f.failures > 0 {
		f.failures--
		return nil, errors.New("kms error")
	}
	return []byte("plain"), nil
}

func (f *fakeProvider) GenerateDataKey(ctx context.Context, req GenerateDataKeyRequest) ([]byte, error) {
	f.generateCalls.Add(1)
	return nil, errors.New("not implemented")
}

type fakeAttestor struct{}

func (fakeAttestor) Document(ctx context.Context) ([]byte, error) { return []byte("doc"), nil }

func (fakeAttestor) Verify([]byte) error { return nil }
