package spice

// session_actions_test.go pins the SPICE-side DECODE of the generic console
// session/flow/boot-order params into the shared sdk/kit actions. The actions'
// behavior is tested in sdk/kit and exercised live by plugin-jetkvm's beds over
// the SAME shared implementation (R3); this file owns the param→neutral mapping
// and the guards, so the SPICE parity is unit-locked.

import (
	"strings"
	"testing"

	"github.com/opencharly/plugin-spice/candy/plugin-spice/params"
)

// TestSessionCommands_DecodesSPICE pins the param→neutral mapping.
func TestSessionCommands_DecodesSPICE(t *testing.T) {
	in := &params.SpiceInput{Commands: []params.SpiceSessionCommand{
		{Command: "id", Sudo: true, Expect: "uid", TimeoutSec: 30},
		{Command: "uname -r"},
	}}
	got, err := sessionCommands(in)
	if err != nil {
		t.Fatalf("sessionCommands: %v", err)
	}
	if len(got) != 2 || !got[0].Sudo || got[0].Expect != "uid" || got[1].Command != "uname -r" {
		t.Fatalf("decode wrong: %+v", got)
	}
}

// TestSessionCommands_BlankFailsSPICE names the offending index.
func TestSessionCommands_BlankFailsSPICE(t *testing.T) {
	in := &params.SpiceInput{Commands: []params.SpiceSessionCommand{{Command: "  "}}}
	if _, err := sessionCommands(in); err == nil || !strings.Contains(err.Error(), "command 1 is empty") {
		t.Fatalf("want blank-command failure, got %v", err)
	}
}

// TestRunFlow_GuardsSPICE pins the flow guards on the SPICE decode.
func TestRunFlow_GuardsSPICE(t *testing.T) {
	if _, err := runFlow(nil, nil, &params.SpiceInput{}); err == nil || !strings.Contains(err.Error(), "flow_start") {
		t.Fatalf("want flow_start guard, got %v", err)
	}
	if _, err := runFlow(nil, nil, &params.SpiceInput{FlowStart: "a"}); err == nil || !strings.Contains(err.Error(), "flow_nodes") {
		t.Fatalf("want flow_nodes guard, got %v", err)
	}
}
