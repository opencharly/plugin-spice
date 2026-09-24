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
// shape. This is the ESTABLISHED cross-boundary exception (RCA): the
// verb:credential contract carries NO shared Go type — it crosses a module
// boundary as JSON over the InvokeProvider reverse leg — so the core adapter AND
// every existing consumer (candy/plugin-jetkvm/credential.go,
// candy/plugin-adb, candy/plugin-vm) keep a byte-identical JSON-tag mirror. The
// same exception the SDD rules allow for a contract with no CUE-sourced shape;
// this mirrors plugin-jetkvm's PRE-EXISTING file verbatim, so there is one shape
// in the vocabulary, not a new one per plugin.
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
