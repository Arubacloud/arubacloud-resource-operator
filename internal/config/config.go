package config

import (
	"fmt"
	"strings"

	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
	ctrl "sigs.k8s.io/controller-runtime"
)

// MainConfig holds operator configuration loaded from ConfigMap + Secret.
type MainConfig struct {
	APIGateway   string
	KeycloakURL  string
	VaultAddress string
	RealmAPI     string
	Namespace    string
	RolePath     string
	KVMount      string
	RoleID       string
	RoleSecret   string
}

// Validate ensures all required fields are present.
func (c *MainConfig) Validate() error {
	required := map[string]string{
		"api-gateway":   c.APIGateway,
		"vault-address": c.VaultAddress,
		"keycloak-url":  c.KeycloakURL,
		"realm-api":     c.RealmAPI,
		"role-path":     c.RolePath,
		"kv-mount":      c.KVMount,
		"role-id":       c.RoleID,
		"role-secret":   c.RoleSecret,
	}

	ctrl.Log.V(1).Info("Validate configurations", "required", required)

	for key, val := range required {
		if strings.TrimSpace(val) == "" {
			return fmt.Errorf("missing required configuration value: %s", key)
		}
	}
	return nil
}

// ToReconcilerConfig converts MainConfig into ReconcilerConfig.
func (c *MainConfig) ToReconcilerConfig() reconciler.ReconcilerConfig {
	return reconciler.ReconcilerConfig{
		APIGateway:   c.APIGateway,
		VaultAddress: c.VaultAddress,
		KeycloakURL:  c.KeycloakURL,
		RealmAPI:     c.RealmAPI,
		Namespace:    c.Namespace,
		RolePath:     c.RolePath,
		KVMount:      c.KVMount,
		RoleID:       c.RoleID,
		RoleSecret:   c.RoleSecret,
	}
}
