package client_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/Arubacloud/arubacloud-resource-operator/internal/client"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/mocks"
	vault "github.com/hashicorp/vault/api"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestRenew(t *testing.T) {
	mockAuthToken := new(mocks.MockAuthTokenAPI)

	mockAuth := new(mocks.MockAuthAPI)

	mockAuth.On("Token").Return(mockAuthToken)
	mockAuthToken.On("RenewSelfWithContext", mock.Anything, mock.Anything).Return(&vault.Secret{
		Auth: &vault.SecretAuth{
			ClientToken:   "token-test",
			LeaseDuration: 3600,
			Renewable:     true,
		},
	}, nil)

	mockClient := &mocks.MockIVaultClient{}

	mockClient.On("Auth").Return(mockAuth)
	mockClient.On("SetToken", mock.AnythingOfType("string")).Return(nil)
	c, err := client.NewAppRoleClientHelper("test-namespace", "test-role-path", "test-role-id", "test-secret-id", "kv", mockClient)
	require.NoError(t, err)

	err = c.RenewSelfHelper(t.Context())

	require.NoError(t, err)
	mockAuthToken.AssertExpectations(t)
	mockAuth.AssertExpectations(t)
	mockClient.AssertExpectations(t)

}

func TestLogin(t *testing.T) {
	tests := []struct {
		name        string
		writeReturn *vault.Secret
		writeErr    error
		expectedTTL int
		expectedErr bool
	}{
		{
			name: "success",
			writeReturn: &vault.Secret{
				Auth: &vault.SecretAuth{
					ClientToken:   "token-test",
					LeaseDuration: 3600,
					Renewable:     true,
				},
			},
			writeErr:    nil,
			expectedTTL: 3600,
			expectedErr: false,
		},
		{
			name:        "vault write error",
			writeReturn: nil,
			writeErr:    fmt.Errorf("login failed"),
			expectedTTL: 0,
			expectedErr: true,
		},
		{
			name:        "nil auth returned",
			writeReturn: &vault.Secret{Auth: nil},
			writeErr:    nil,
			expectedTTL: 0,
			expectedErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockLogical := new(mocks.MockLogicalAPI)
			mockClient := new(mocks.MockIVaultClient)

			mockClient.On("SetNamespace", "test-namespace").Return()
			mockClient.On("Logical").Return(mockLogical)
			mockClient.On("SetToken", mock.Anything).Return(nil)

			mockLogical.On(
				"Write",
				"auth/test-role-path/login",
				mock.Anything,
			).Return(tt.writeReturn, tt.writeErr)

			_, err := client.NewAppRoleClient("test-namespace", "test-role-path", "test-role-id", "test-secret-id", "kv", mockClient)

			if tt.expectedErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)

			}

			if !tt.expectedErr {
				mockClient.AssertExpectations(t)
				mockLogical.AssertExpectations(t)
			}
		})
	}
}

// --- Test ---
func TestAutoRenew_TriggersRenewal(t *testing.T) {
	mockVault := new(mocks.MockIVaultClient)
	mockAuth := new(mocks.MockAuthAPI)
	mockToken := new(mocks.MockAuthTokenAPI)

	// Mock method call chain: client.Auth().Token().RenewSelfWithContext(...)
	mockVault.On("Auth").Return(mockAuth)
	mockAuth.On("Token").Return(mockToken)
	mockVault.On("SetToken", mock.AnythingOfType("string")).Return(nil)

	mockToken.On("RenewSelfWithContext", mock.Anything, mock.AnythingOfType("int")).
		Return(&vault.Secret{
			Auth: &vault.SecretAuth{LeaseDuration: 120, Renewable: true},
		}, nil).
		Maybe()

	// Controlled fake timer
	mockSleeper := new(mocks.MockSleeper)
	// ch := make(chan time.Time, 1)

	mockSleeper.On("After", mock.Anything).Return(func(d time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		go func() {
			// simulate time passing
			time.Sleep(5 * time.Millisecond)
			ch <- time.Now()
		}()
		return ch
	})

	// sleeper := &mockSleeper{ch: make(chan time.Time, 1)}

	c, _ := client.AutoRenewHelper("test-namespace", "test-role-path", "test-role-id", "test-secret-id", "kv", mockVault, 50, mockSleeper)

	// Start the goroutine
	defer c.Close()
	// Trigger renewal manually
	//	ch <- time.Now()

	// Give goroutine a moment to process (deterministic and small)
	time.Sleep(50 * time.Millisecond)

	mockToken.AssertCalled(t, "RenewSelfWithContext", mock.Anything, mock.AnythingOfType("int"))
}
