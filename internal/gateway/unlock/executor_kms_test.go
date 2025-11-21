package unlock

import (
	"context"
	"testing"

	"github.com/aegis-sign/wallet/internal/app/backend/keycache"
	kmspkg "github.com/aegis-sign/wallet/internal/infra/kms"
	"github.com/aegis-sign/wallet/internal/infra/kms/mockkms"
	"github.com/stretchr/testify/require"
)

func TestKMSEnclaveExecutorSuccess(t *testing.T) {
	provider := mockkms.NewStaticProvider([]byte("plain"))
	attestor := mockkms.NewStaticAttestor(nil)
	client, err := kmspkg.NewClient(provider, attestor, kmspkg.Config{})
	require.NoError(t, err)

	exec := NewKMSEnclaveExecutor(client, nil)
	res := exec.Execute(context.Background(), JobPayload{Event: keycache.UnlockEvent{KeyID: "k1"}})
	require.True(t, res.Success)
	require.Nil(t, res.Err)
}

func TestKMSEnclaveExecutorMissingClient(t *testing.T) {
	exec := NewKMSEnclaveExecutor(nil, nil)
	res := exec.Execute(context.Background(), JobPayload{Event: keycache.UnlockEvent{KeyID: "k1"}})
	require.False(t, res.Success)
	require.Error(t, res.Err)
}
