package channels

import (
	"context"
	"fmt"

	"metiq/internal/plugins/sdk"
	"metiq/internal/secrets"
)

// resolveAccountSecrets returns a copy of cfg with every top-level SecretRef
// (see secrets.SecretRefFromConfig) replaced by its resolved value, plus the
// gateway-store entry name behind each such field so rotated values can be
// written back. Unresolvable refs fail the connect rather than reaching the
// plugin as a raw object.
func resolveAccountSecrets(ctx context.Context, store *secrets.Store, cfg map[string]any) (map[string]any, map[string]string, error) {
	resolved := cloneAccountParams(cfg)
	stored := map[string]string{}
	var lifecycle *secrets.Lifecycle
	for field, value := range cfg {
		ref, ok := secrets.SecretRefFromConfig(value)
		if !ok {
			continue
		}
		if store == nil {
			return nil, nil, fmt.Errorf("config field %q is a secret reference but no secret store is configured", field)
		}
		if lifecycle == nil {
			lifecycle = secrets.NewLifecycle(store)
		}
		secret, err := lifecycle.ResolveRef(ctx, ref)
		if err != nil {
			return nil, nil, fmt.Errorf("config field %q: resolve secret reference: %w", field, err)
		}
		resolved[field] = secret
		if name, ok := secrets.StoredSecretName(ref); ok {
			stored[field] = name
		}
	}
	return resolved, stored, nil
}

// accountCredentialWriter lets one connected account rotate only the
// gateway-store entries its own config fields reference.
type accountCredentialWriter struct {
	store     *secrets.Store
	updatedBy string
	stored    map[string]string
}

func (w accountCredentialWriter) PersistCredential(ctx context.Context, field, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	name, ok := w.stored[field]
	if !ok || w.store == nil {
		return fmt.Errorf("%w: config field %q is not a stored-secret reference", sdk.ErrCredentialNotWritable, field)
	}
	if _, err := w.store.RotateStoredSecret(name, value, w.updatedBy); err != nil {
		return fmt.Errorf("persist config field %q: %w", field, err)
	}
	return nil
}
