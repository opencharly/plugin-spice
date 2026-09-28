package spice

// session_actions_test.go pins the SPICE-side DECODE of the generic console
// session/flow/boot-order params into the shared sdk/kit actions. The actions'
// behavior is tested in sdk/kit and exercised live by plugin-jetkvm's beds over
// the SAME shared implementation (R3); this file owns the param→neutral mapping
// and the guards, so the SPICE parity is unit-locked.

import (
	"context"
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

// TestBuildFlowSpec_GuardsSPICE pins the flow guards on the SPICE decode.
func TestBuildFlowSpec_GuardsSPICE(t *testing.T) {
	if _, err := buildFlowSpec(context.Background(), &params.SpiceInput{}); err == nil || !strings.Contains(err.Error(), "flow_start") {
		t.Fatalf("want flow_start guard, got %v", err)
	}
	if _, err := buildFlowSpec(context.Background(), &params.SpiceInput{FlowStart: "a"}); err == nil || !strings.Contains(err.Error(), "flow_nodes") {
		t.Fatalf("want flow_nodes guard, got %v", err)
	}
}

// TestBuildFlowSpec_DecodesNodesSPICE pins the full param→neutral node/outcome
// mapping: the entry, the node action, the transitions, the resume selection,
// and the outcome match/reference/failure fields. This is the SPICE-specific
// decode the block asked to prove; it runs with no VM console.
func TestBuildFlowSpec_DecodesNodesSPICE(t *testing.T) {
	in := &params.SpiceInput{
		FlowStart: "boot",
		FlowNodes: map[string]params.SpiceFlowNode{
			"boot": {
				Description: "wait for the boot menu",
				Wait: []params.SpiceFlowOutcome{
					{Name: "ready", Match: "Boot Menu"},
					{Name: "bad", Reference: "panic.png", MaxDistance: 8, Failure: true},
				},
				Key:           "Return",
				Combo:         "ctrl+c",
				Text:          "x",
				Command:       "id",
				Sudo:          true,
				Expect:        "uid",
				CloseTerminal: true,
				Transitions:   map[string]string{"ready": "shell", "bad": "boot"},
				Next:          "shell",
				Artifact:      "/tmp/boot.png",
				TimeoutSec:    42,
			},
		},
		FlowMaxSteps:    7,
		FlowMaxLoops:    3,
		FlowResume:      true,
		FlowResumeOrder: []string{"shell", "boot"},
		PromptAnchors:   []string{"#"},
		SudoPassword:    "pw",
	}

	spec, err := buildFlowSpec(context.Background(), in)
	if err != nil {
		t.Fatalf("buildFlowSpec: %v", err)
	}
	if spec.Start != "boot" || spec.MaxSteps != 7 || spec.MaxLoops != 3 || !spec.ResumeFromScreen {
		t.Fatalf("flow spec fields not decoded: %+v", spec)
	}
	n, ok := spec.Nodes["boot"]
	if !ok {
		t.Fatalf("node not decoded: %+v", spec.Nodes)
	}
	if len(n.Wait) != 2 || n.Wait[0].Match != "Boot Menu" || !n.Wait[1].Failure || n.Wait[1].MaxDistance != 8 {
		t.Fatalf("outcomes not decoded: %+v", n.Wait)
	}
	if n.Action.Key != "Return" || n.Action.Combo != "ctrl+c" || !n.Action.Sudo || !n.Action.CloseTerminal {
		t.Fatalf("node action not decoded: %+v", n.Action)
	}
	if n.Transitions["ready"] != "shell" || n.Next != "shell" || n.TimeoutSec != 42 {
		t.Fatalf("transitions/next/timeout not decoded: %+v", n)
	}
}

// TestBuildBootOrder_DecodesSPICE pins the boot-order param→neutral decode.
func TestBuildBootOrder_DecodesSPICE(t *testing.T) {
	in := &params.SpiceInput{
		BootOrderAction:   "set",
		BootOrderEntry:    "Boot0005",
		BootOrderSequence: "0005,0001",
		BootOrderCommand:  "/usr/bin/efibootmgr",
	}
	bo := buildBootOrder(in, "sudo-pw")
	if bo.Action != "set" || bo.Entry != "Boot0005" || bo.Sequence != "0005,0001" || bo.Binary != "/usr/bin/efibootmgr" || bo.SudoPassword != "sudo-pw" {
		t.Fatalf("boot-order not decoded: %+v", bo)
	}
}

// TestTerminalTimeoutSPICE pins the open-terminal budget: the first command's
// authored timeout wins, else the 60s default.
func TestTerminalTimeoutSPICE(t *testing.T) {
	if got := terminalTimeout(&params.SpiceInput{}); got != 60 {
		t.Fatalf("default timeout = %d, want 60", got)
	}
	in := &params.SpiceInput{Commands: []params.SpiceSessionCommand{{Command: "id", TimeoutSec: 12}}}
	if got := terminalTimeout(in); got != 12 {
		t.Fatalf("authored timeout = %d, want 12", got)
	}
}

// TestResolveSecret_LiteralAndNoBroker pins the shared CredentialAccess leg: an
// authored literal wins with no broker, and a missing broker/selector resolves to
// "" rather than erroring (the action then fails visibly on the missing secret).
func TestResolveSecret_LiteralAndNoBroker(t *testing.T) {
	if got := resolveSecret(context.Background(), 0, "literal", "IGNORED"); got != "literal" {
		t.Fatalf("literal must win, got %q", got)
	}
	if got := resolveSecret(context.Background(), 0, "", "SOME_KEY"); got != "" {
		t.Fatalf("no broker must resolve to empty, got %q", got)
	}
	if got := resolveSecret(context.Background(), 0, "", ""); got != "" {
		t.Fatalf("empty selector must resolve to empty, got %q", got)
	}
}
