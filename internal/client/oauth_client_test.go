package client_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Arubacloud/arubacloud-resource-operator/internal/client"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/mocks"
	"github.com/Nerzal/gocloak/v13"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestOauthLogin(t *testing.T) {

	mockOauth := new(mocks.MockIOauth)
	mockOauthClient := new(mocks.MockIOauthClient)

	mockOauth.On("NewClient", mock.Anything).Return(mockOauthClient)

	mockOauthClient.On("LoginClient", mock.Anything, "client-id", "client-secret", "realm").Return(&gocloak.JWT{
		AccessToken: "access-token",
	}, nil)

	tm := client.NewTokenManager("http://keycloak.example.com", "realm", "client-id", "client-secret", mockOauth)

	jwt, err := tm.GetAccessToken(true, "tenant")

	require.NoError(t, err)
	assert.Equal(t, "access-token", jwt)

	mockOauth.AssertExpectations(t)

}

func TestTokenManager_GetAccessToken(t *testing.T) {
	tests := []struct {
		name          string
		setupMocks    func() (*mocks.MockIOauth, *mocks.MockIOauthClient)
		expectedToken string
		expectError   bool
		overrideCache *gocloak.JWT
	}{
		{
			name: "successful login",
			setupMocks: func() (*mocks.MockIOauth, *mocks.MockIOauthClient) {
				mockOauth := new(mocks.MockIOauth)
				mockOauthClient := new(mocks.MockIOauthClient)

				mockOauth.On("NewClient", mock.Anything).Return(mockOauthClient)
				mockOauthClient.On("LoginClient", mock.Anything, "client-id", "client-secret", "realm").
					Return(&gocloak.JWT{AccessToken: "access-token"}, nil)

				return mockOauth, mockOauthClient
			},
			expectedToken: "access-token",
			expectError:   false,
		},
		{
			name: "login failure",
			setupMocks: func() (*mocks.MockIOauth, *mocks.MockIOauthClient) {
				mockOauth := new(mocks.MockIOauth)
				mockOauthClient := new(mocks.MockIOauthClient)

				mockOauth.On("NewClient", mock.Anything).Return(mockOauthClient)
				mockOauthClient.On("LoginClient", mock.Anything, "client-id", "client-secret", "realm").
					Return(nil, errors.New("login failed"))
				return mockOauth, mockOauthClient
			},
			expectedToken: "",
			expectError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockKeycloak, _ := tt.setupMocks()

			tm := client.NewTokenManager(
				"http://keycloak.example.com",
				"realm",
				"client-id",
				"client-secret",
				mockKeycloak,
			)

			token, err := tm.GetAccessToken(true, "tenant")

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedToken, token)
			}

			mockKeycloak.AssertExpectations(t)
		})
	}
}

func TestIsExpired(t *testing.T) {
	tm := client.NewTokenManager("http://keycloak.example.com", "realm", "client-id", "client-secret", nil)

	// Token that expires in the future
	tokenValid := client.SetCachedTokenHelper(
		&gocloak.JWT{
			ExpiresIn: 300, // 5 minutes
		},
		time.Now())

	assert.False(t, tm.IsExpiredHelper(tokenValid), "Token should not be expired")

	// Token that expired in the past
	tokenExpired := client.SetCachedTokenHelper(
		&gocloak.JWT{
			ExpiresIn: 10, // 10 seconds
		}, time.Now().Add(-time.Minute))

	assert.True(t, tm.IsExpiredHelper(tokenExpired), "Token should be expired")
}
