package client

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	vault "github.com/hashicorp/vault/api"
	ctrl "sigs.k8s.io/controller-runtime"
)

// VaultClient defines the interface your app will use
type IVaultClient interface {
	Logical() LogicalAPI
	SetToken(token string)
	KVv2(mount string) KvAPI
	SetNamespace(namespace string)
	Auth() AuthAPI
}

type LogicalAPI interface {
	Write(path string, data map[string]any) (*vault.Secret, error)
}

type KvAPI interface {
	Get(ctx context.Context, path string) (*vault.KVSecret, error)
}

type AuthAPI interface {
	Token() AuthTokenAPI
}

type AuthTokenAPI interface {
	RenewSelfWithContext(ctx context.Context, increment int) (*vault.Secret, error)
}

// Sleeper interface allows deterministic testing of time-based logic
type Sleeper interface {
	After(d time.Duration) <-chan time.Time
}

// realSleeper uses time.After (for real production)
type realSleeper struct{}

type VaultClientAPI struct {
	c *vault.Client
}
type logicalAPI struct {
	l *vault.Logical
}

type kvAPI struct {
	kv *vault.KVv2
}

type authAPI struct {
	auth *vault.Auth
}

type authTokenAPI struct {
	token *vault.TokenAuth
}

// AppRoleClient implements VaultClient using AppRole auth
type AppRoleClient struct {
	client    IVaultClient
	namespace string
	rolePath  string
	roleID    string
	secretID  string
	renewable bool
	mu        sync.Mutex
	ttl       time.Duration
	sleeper   Sleeper
	cancel    context.CancelFunc
	KVMount   string
}

func VaultClient(address string) IVaultClient {
	config := vault.DefaultConfig()
	config.Address = address
	client, err := vault.NewClient(config)
	if err != nil {
		ctrl.Log.Error(err, "Vault client initialization failed")
		os.Exit(1)
	}
	return &VaultClientAPI{c: client}
}

// NewAppRoleClient creates and authenticates an AppRoleClient
func NewAppRoleClient(namespace string, rolePath string, roleID string, secretID string, kvMount string, cli IVaultClient) (*AppRoleClient, error) {

	c := &AppRoleClient{
		client:    cli,
		namespace: namespace,
		rolePath:  rolePath,
		KVMount:   kvMount,
		roleID:    roleID,
		secretID:  secretID,
		sleeper:   realSleeper{},
	}

	if err := c.login(); err != nil {
		return nil, err
	}

	// Start background renewal loop
	c.StartAutoRenew()

	return c, nil
}

func (c *AppRoleClient) Close() {
	if c.cancel != nil {
		c.cancel()
	}
}

// login authenticates using AppRole
func (c *AppRoleClient) login() error {
	data := map[string]any{
		"role_id":   c.roleID,
		"secret_id": c.secretID,
	}

	if c.namespace != "" {
		c.client.SetNamespace(c.namespace)
	}

	appRoleURI := fmt.Sprintf("auth/%v/login", c.rolePath)
	ctrl.Log.V(1).Info("trying Vault login", "uri", appRoleURI)

	secret, err := c.client.Logical().Write(appRoleURI, data)
	ctrl.Log.V(1).Info("[DEBUG] Secret received", "secret", secret)

	if err != nil {
		return fmt.Errorf("AppRole login failed: %w", err)
	}

	if secret.Auth == nil {
		return fmt.Errorf("no auth info returned from Vault")
	}

	c.client.SetToken(secret.Auth.ClientToken)
	c.ttl = time.Duration(secret.Auth.LeaseDuration) * time.Second
	c.renewable = secret.Auth.Renewable
	ctrl.Log.V(1).Info("[Vault] Authenticated! TTL: %d seconds, Renewable: %v", c.ttl, c.renewable)
	return nil
}

// GetSecret reads a KVv2 secret
func (c *AppRoleClient) GetSecret(ctx context.Context, path string) (map[string]any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	secret, err := c.client.KVv2(c.KVMount).Get(ctx, path)
	if err != nil {
		return nil, err
	}
	return secret.Data, nil
}

// autoRenew starts a background goroutine to renew the token
func (c *AppRoleClient) autoRenew(ctx context.Context) {
	if !c.renewable {
		ctrl.Log.V(1).Info("[vaultclient] token not renewable, stopping autoRenew")
		return
	}

	if c.sleeper == nil {
		c.sleeper = realSleeper{} // use real sleeper in production
	}
	renewBefore := c.ttl / 5 // renew at 80% of TTL
	if renewBefore < 10*time.Second {
		renewBefore = 10 * time.Second
	}
	for {
		wait := c.ttl - renewBefore
		if wait <= 0 {
			wait = 1 * time.Second
		}
		select {
		case <-ctx.Done():
			ctrl.Log.V(1).Info("[vaultclient] autoRenew stopped")
			return
		case <-c.sleeper.After(wait):
			if err := c.renewSelf(ctx); err != nil {
				ctrl.Log.V(1).Info("[vaultclient] renew failed — re-login", "error", err)
				_ = c.login()
			}
		}
	}
}

func (c *AppRoleClient) StartAutoRenew() {
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	go func() {
		// Use the same context for clean stop
		// but protect against immediate cancellation
		if ctx.Err() != nil {
			ctrl.Log.V(1).Info("[vaultclient] autoRenew context was canceled immediately — recreating")
			ctx = context.Background()
		}
		c.autoRenew(ctx)
	}()
}

func (c *AppRoleClient) renewSelf(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	secret, err := c.client.Auth().Token().RenewSelfWithContext(ctx, int(c.ttl.Seconds()))
	if err != nil {
		return fmt.Errorf("renew self: %w", err)
	}
	if secret == nil || secret.Auth == nil {
		return fmt.Errorf("invalid renew response")
	}

	c.ttl = time.Duration(secret.Auth.LeaseDuration) * time.Second
	c.renewable = secret.Auth.Renewable
	c.client.SetToken(secret.Auth.ClientToken)
	ctrl.Log.V(1).Info("[vaultclient] token renewed", "ttl", c.ttl)
	return nil
}

// implement IVaultClient methods
func (v *VaultClientAPI) SetNamespace(namespace string) {
	v.c.SetNamespace(namespace)
}

func (v *VaultClientAPI) Auth() AuthAPI {
	return &authAPI{auth: v.c.Auth()}
}

func (v *VaultClientAPI) SetToken(token string) {
	v.c.SetToken(token)
}

func (v *VaultClientAPI) Logical() LogicalAPI {
	return &logicalAPI{l: v.c.Logical()}
}
func (v *VaultClientAPI) KVv2(mount string) KvAPI {
	return &kvAPI{kv: v.c.KVv2(mount)}
}

// implement AuthAPI methods
func (a *authAPI) Token() AuthTokenAPI {
	return &authTokenAPI{token: a.auth.Token()}
}

// implement AuthTokenAPI methods
func (a *authTokenAPI) RenewSelfWithContext(ctx context.Context, increment int) (*vault.Secret, error) {
	return a.token.RenewSelfWithContext(ctx, increment)
}

// implement KvAPI methods
func (k *kvAPI) Get(ctx context.Context, path string) (*vault.KVSecret, error) {
	return k.kv.Get(ctx, path)
}

// implement Sleeper methods
func (r realSleeper) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}

// implement LogicalAPI methods
func (la *logicalAPI) Write(path string, data map[string]any) (*vault.Secret, error) {
	return la.l.Write(path, data)
}

// Helper functions for testing
func (a *AppRoleClient) LoginHelper() error {
	return a.login()
}

func (a *AppRoleClient) RenewSelfHelper(ctx context.Context) error {
	return a.renewSelf(ctx)
}

func NewAppRoleClientHelper(namespace string, rolePath string, roleID string, secretID string, kvMount string, mockClient IVaultClient) (*AppRoleClient, error) {
	return &AppRoleClient{
		client:    mockClient,
		namespace: namespace,
		rolePath:  rolePath,
		roleID:    roleID,
		KVMount:   kvMount,
		secretID:  secretID,
	}, nil
}

func AutoRenewHelper(namespace string, rolePath string, roleID string, secretID string, kvMount string, mockClient IVaultClient, timeToWaitMs int, sleep Sleeper) (*AppRoleClient, error) {
	c := &AppRoleClient{
		client:    mockClient,
		namespace: namespace,
		rolePath:  rolePath,
		roleID:    roleID,
		KVMount:   kvMount,
		secretID:  secretID,
		renewable: true,
		sleeper:   sleep,
	}

	c.ttl = time.Duration(timeToWaitMs) * time.Millisecond

	go c.autoRenew(context.Background())

	return c, nil
}
