package config

import (
	"fmt"
	"strings"

	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
	ctrl "sigs.k8s.io/controller-runtime"
)

// MainConfig holds operator configuration loaded from ConfigMap + Secret.
type MainConfig struct {
	APIGateway     string
	KeycloakURL    string
	VaultIsEnabled bool
	VaultAddress   string
	RealmAPI       string
	Namespace      string
	RolePath       string
	KVMount        string
	RoleID         string
	RoleSecret     string
	ClientID       string
	ClientSecret   string
}

// Validate ensures all required fields are present.
func (c *MainConfig) Validate() error {

	var required map[string]string

	if !c.VaultIsEnabled {
		ctrl.Log.Info("Vault integration is disabled; skipping vault configuration validation")
		required = map[string]string{
			"api-gateway":   c.APIGateway,
			"keycloak-url":  c.KeycloakURL,
			"realm-api":     c.RealmAPI,
			"client-id":     c.ClientID,
			"client-secret": c.ClientSecret,
		}
	} else {
		ctrl.Log.Info("Vault integration is enabled; validating vault configuration also")

		required = map[string]string{
			"api-gateway":   c.APIGateway,
			"vault-address": c.VaultAddress,
			"keycloak-url":  c.KeycloakURL,
			"realm-api":     c.RealmAPI,
			"role-path":     c.RolePath,
			"kv-mount":      c.KVMount,
			"role-id":       c.RoleID,
			"role-secret":   c.RoleSecret,
		}
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
		APIGateway:     c.APIGateway,
		VaultAddress:   c.VaultAddress,
		KeycloakURL:    c.KeycloakURL,
		ClientID:       c.ClientID,
		ClientSecret:   c.ClientSecret,
		VaultIsEnabled: c.VaultIsEnabled,
		RealmAPI:       c.RealmAPI,
		Namespace:      c.Namespace,
		RolePath:       c.RolePath,
		KVMount:        c.KVMount,
		RoleID:         c.RoleID,
		RoleSecret:     c.RoleSecret,
	}
}
