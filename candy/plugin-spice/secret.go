package spice

// secret.go resolves a console action's `*_secret` selector against the charly
// credential store over the SHARED sdk/deploykit CredentialAccess (R3) — the SAME
// abstraction charly's own secret resolution uses, so this plugin carries NO
// hand-written verb:credential wire mirror (the mirror was the defect the wizard
// PR #10 was blocked on). A missing key, an absent store, or a missing broker all
// resolve to the empty string (the selector is optional), never a hard error — the
// action then fails visibly on the missing secret, which is the honest outcome.

import (
	"context"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
)

// credentialService is the service verb:credential stores charly secrets under.
const credentialService = "charly/secret"

// resolveSecret returns the authored literal when present, otherwise the value of
// the named credential-store key read over the shared CredentialAccess. brokerID
// is the reverse-channel broker the host threaded onto this Invoke (zero when
// there is none, in which case only the literal is consulted).
func resolveSecret(ctx context.Context, brokerID uint32, literal, secretKey string) string {
	if literal != "" {
		return literal
	}
	if secretKey == "" {
		return ""
	}
	ex, err := sdk.ExecutorForInvoke(ctx, brokerID)
	if err != nil || ex == nil {
		return ""
	}
	access := deploykit.CredentialAccessViaExecutor(ctx, ex)
	value, _ := access.Resolve("", credentialService, secretKey, "")
	return value
}
