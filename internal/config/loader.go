package config

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Load reads the operator configuration from ConfigMap and Secret.
func Load(ctx context.Context, mgr ctrl.Manager, configMapName, configNamespace, secretName string) (*MainConfig, error) {
	c, err := client.New(ctrl.GetConfigOrDie(), client.Options{Scheme: mgr.GetScheme()})
	if err != nil {
		return nil, fmt.Errorf("unable to create k8s client: %w", err)
	}

	cfg, err := getConfigMap(ctx, c, configNamespace, configMapName)
	if err != nil {
		return nil, fmt.Errorf("failed to read configmap %s: %w", configMapName, err)
	}

	secret, err := getSecret(ctx, c, configNamespace, secretName)
	if err != nil {
		return nil, fmt.Errorf("failed to read secret %s: %w", secretName, err)
	}

	mainConfig := &MainConfig{
		APIGateway:     cfg.Data["api-gateway"],
		VaultIsEnabled: cfg.Data["vault-enabled"] == "true",
		VaultAddress:   cfg.Data["vault-address"],
		KeycloakURL:    cfg.Data["keycloak-url"],
		RealmAPI:       cfg.Data["realm-api"],
		Namespace:      cfg.Data["role-namespace"],
		RolePath:       cfg.Data["role-path"],
		KVMount:        cfg.Data["kv-mount"],
		RoleID:         string(secret.Data["role-id"]),
		RoleSecret:     string(secret.Data["role-secret"]),
		ClientID:       string(secret.Data["client-id"]),
		ClientSecret:   string(secret.Data["client-secret"]),
	}

	if err := mainConfig.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return mainConfig, nil
}

func getConfigMap(ctx context.Context, c client.Client, ns, name string) (*corev1.ConfigMap, error) {
	cfg := &corev1.ConfigMap{}
	if err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func getSecret(ctx context.Context, c client.Client, ns, name string) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	if err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, secret); err != nil {
		return nil, err
	}
	return secret, nil
}
