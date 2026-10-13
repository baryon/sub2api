//go:build unit

package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/stretchr/testify/require"
)

// Every platform in the platform list can hold accounts, so an admin can add one for any platform the account
// page offers (TASK-80: a TypeSafe / Jev API key was refused as "account platform is not supported").
func TestEveryListedPlatformCanHoldAccounts(t *testing.T) {
	for _, id := range domain.ConcretePlatformIDs() {
		got, err := normalizeAccountPlatform(id)
		require.NoError(t, err, id)
		require.Equal(t, id, got)
	}
	got, err := normalizeAccountPlatform("  TypeSafe ")
	require.NoError(t, err)
	require.Equal(t, PlatformTypeSafe, got)
}

func TestAccountsCannotBeOnTheCompositeOrAnUnknownPlatform(t *testing.T) {
	for _, id := range []string{PlatformComposite, "", "nonexistent"} {
		_, err := normalizeAccountPlatform(id)
		require.Error(t, err, id)
	}
}
