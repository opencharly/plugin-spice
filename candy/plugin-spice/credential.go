package spice

// credential.go resolves an `answer_secrets:` entry from the credential store
// over the SDK's InvokeProvider reverse leg — the SAME pattern plugin-jetkvm,
// plugin-adb and plugin-vm use. A wizard's answer_secrets maps an answer NAME to
// a credential-store KEY, so a password can live in the store and never in
// charly.yml.
//
// A missing key, an absent store, or a missing broker all resolve to the empty
// string (the key is optional), never a hard error — the wizard then fails
// visibly on an unsubstituted placeholder, which is the honest outcome.

import (
	"context"
	"encoding/json"

	"github.com/opencharly/spec/exec"
	"github.com/opencharly/spec/ops"
)

// credentialService is the service name verb:credential stores charly secrets
// under (byte-identical to the core adapter and every other consumer).
const credentialService = "charly/secret"

// credentialGetInput / credentialGetReply mirror verb:credential's `get` wire
// shape. The cross-module contract carries no shared Go type, so each consumer
// keeps a JSON-tag-compatible mirror, exactly as the core adapter does.
type credentialGetInput struct {
	Method  string `json:"method"`
	Service string `json:"service,omitempty"`
	Key     string `json:"key,omitempty"`
}

type credentialGetReply struct {
	Value string `json:"value,omitempty"`
	Error string `json:"error,omitempty"`
}

// credentialLookup fetches one key from verb:credential over the peer reverse
// leg. A missing key, an absent store, or a missing broker all resolve to the
// empty string, never a hard error.
func credentialLookup(ctx context.Context, brokerID uint32, key string) string {
	if key == "" || brokerID == 0 {
		return ""
	}
	ex, err := exec.ExecutorForInvoke(ctx, brokerID)
	if err != nil || ex == nil {
		return ""
	}
	payload, err := json.Marshal(credentialGetInput{Method: "get", Service: credentialService, Key: key})
	if err != nil {
		return ""
	}
	out, err := ex.InvokeProvider(ctx, "verb", "credential", ops.OpRun, payload, nil, ops.InvokeProviderOpts{})
	if err != nil || len(out) == 0 {
		return ""
	}
	var reply credentialGetReply
	if err := json.Unmarshal(out, &reply); err != nil {
		return ""
	}
	return reply.Value
}
