package client

import (
	"context"
	"sync"
	"time"

	"github.com/Nerzal/gocloak/v13"
	ctrl "sigs.k8s.io/controller-runtime"
)

type IOauth interface {
	NewClient(string) IOauthClient
}

type IOauthClient interface {
	LoginClient(ctx context.Context, clientID, clientSecret, realm string, options ...string) (*gocloak.JWT, error)
}

type OauthClient struct {
	cli *gocloak.GoCloak
}

func (k *OauthClient) NewClient(baseURL string) IOauthClient {
	return &OauthClient{cli: gocloak.NewClient(baseURL)}
}

func (k *OauthClient) LoginClient(ctx context.Context, clientID, clientSecret, realm string, options ...string) (*gocloak.JWT, error) {
	return k.cli.LoginClient(ctx, clientID, clientSecret, realm, options...)
}

type TokenManager struct {
	client IOauthClient
	ctx    context.Context
	cache  *TokenCache

	mu           sync.Mutex
	clientID     string
	clientSecret string
	realm        string
	baseURL      string
}

type ITokenManager interface {
	GetAccessToken(checkCache bool, tenant string) (string, error)
	GetActiveToken(tenant string) string
	SetClientIdAndSecret(clientID string, clientSecret string)
	IsExpiredHelper(cToken *CachedToken) bool
}

type TokenCache struct {
	mu     sync.RWMutex
	tokens map[string]*CachedToken
}
type CachedToken struct {
	token     *gocloak.JWT
	retrieved time.Time
}

func (c *TokenCache) get(tenant string) *CachedToken {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if tenant == "" {
		return c.tokens["public"]
	}
	return c.tokens[tenant]
}

func (c *TokenCache) set(tenant string, token *gocloak.JWT) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if tenant == "" {
		c.tokens["public"] = &CachedToken{
			token:     token,
			retrieved: time.Now(),
		}
		return
	}

	c.tokens[tenant] = &CachedToken{
		token:     token,
		retrieved: time.Now(),
	}
}

// NewTokenManager creates a new Keycloak client credentials manager.
func NewTokenManager(baseURL, realm, clientID, clientSecret string, keycloak IOauth) ITokenManager {
	var cli IOauthClient
	if keycloak != nil {
		cli = keycloak.NewClient(baseURL)
	} else {
		cli = gocloak.NewClient(baseURL)
	}
	return &TokenManager{
		client:       cli,
		ctx:          context.Background(),
		cache:        &TokenCache{tokens: make(map[string]*CachedToken)},
		clientID:     clientID,
		clientSecret: clientSecret,
		realm:        realm,
		baseURL:      baseURL,
	}
}

func (tm *TokenManager) SetClientIdAndSecret(clientID string, clientSecret string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tm.clientID = clientID
	tm.clientSecret = clientSecret
}

// getToken retrieves a new token using client credentials.
func (tm *TokenManager) getToken() (*gocloak.JWT, error) {
	ctrl.Log.V(1).Info("Getting token with client credentials", "clientId", tm.clientID, "realm", tm.realm)
	token, err := tm.client.LoginClient(tm.ctx, tm.clientID, tm.clientSecret, tm.realm)
	if err != nil {
		return nil, err
	}
	return token, nil
}

func (tm *TokenManager) GetActiveToken(tenant string) string {
	ctrl.Log.V(1).Info("GetActiveToken", "tenant", tenant)
	token := tm.cache.get(tenant)
	// If we have a valid cached token
	if token != nil && !tm.isExpired(token) {
		ctrl.Log.V(1).Info("Found active token", "tenant", tenant)
		return token.token.AccessToken
	}
	return ""
}

// GetAccessToken returns a valid access token, refreshing it if expired.
func (tm *TokenManager) GetAccessToken(checkCache bool, tenant string) (string, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	ctrl.Log.V(1).Info("GetAccessToken, if checkCache is enabled search it on cache before", "checkCache", checkCache, "tenant", tenant)
	if checkCache {
		token := tm.cache.get(tenant)

		// If we have a valid cached token
		if token != nil && !tm.isExpired(token) {
			ctrl.Log.V(1).Info("found token in cache", "tenant", tenant)
			return token.token.AccessToken, nil
		}
	}
	// If expired or missing, renew
	tk, err := tm.getToken()
	if err != nil {
		return "", err
	}

	ctrl.Log.V(1).Info("Set Token in memory cache", "tenant", tenant)
	tm.cache.set(tenant, tk)
	return tk.AccessToken, nil
}

// isExpired checks if the token is expired (with 10s safety margin)
func (tm *TokenManager) isExpired(cToken *CachedToken) bool {
	const safetyMargin = 10 * time.Second
	ctrl.Log.V(1).Info("Checking expired token", "token", cToken.token.AccessToken)
	expiration := cToken.retrieved.Add(time.Duration(cToken.token.ExpiresIn) * time.Second)
	return time.Now().After(expiration.Add(-safetyMargin))
}

func SetCachedTokenHelper(token *gocloak.JWT, retrieved time.Time) *CachedToken {
	return &CachedToken{
		token:     token,
		retrieved: retrieved,
	}
}

func (tm *TokenManager) IsExpiredHelper(cToken *CachedToken) bool {
	return tm.isExpired(cToken)
}
