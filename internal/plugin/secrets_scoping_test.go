package plugin_test

import (
	"context"
	"errors"
	"testing"

	"github.com/synapbus/synapbus/internal/plugin"
	"github.com/synapbus/synapbus/internal/plugin/plugintest"
)

// Satisfies SC-006: plugins cannot read each other's secrets through the
// Host.Secrets accessor.
func TestScopedSecrets_CrossPluginLookupReturnsNotFound(t *testing.T) {
	t.Cleanup(plugintest.ResetScopedSecrets)

	alphaSecrets := plugintest.NewScopedSecrets("alpha")
	betaSecrets := plugintest.NewScopedSecrets("beta")

	if err := alphaSecrets.Set(context.Background(), "api_key", []byte("secret")); err != nil {
		t.Fatal(err)
	}

	// Alpha can read its own.
	v, err := alphaSecrets.Get(context.Background(), "api_key")
	if err != nil {
		t.Fatalf("alpha could not read its own secret: %v", err)
	}
	if string(v) != "secret" {
		t.Fatalf("got %q want secret", string(v))
	}

	// Beta gets ErrSecretNotFound — same error as missing — with no value.
	_, err = betaSecrets.Get(context.Background(), "api_key")
	if !errors.Is(err, plugin.ErrSecretNotFound) {
		t.Fatalf("expected ErrSecretNotFound for cross-plugin read, got %v", err)
	}
}
