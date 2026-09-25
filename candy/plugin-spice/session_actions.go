package spice

// session_actions.go implements the GENERIC console terminal-session / flow /
// boot-order / LUKS methods over the SHARED sdk/kit action layer — the SAME
// implementation the jetkvm verb calls (R3), so one authored action drives a VM
// console over SPICE or a physical machine over a JetKVM. This file owns ONLY
// the param→neutral decode; the driving mechanism lives in sdk/kit.
//
// It is the SPICE sibling of plugin-jetkvm's session.go: the two plugins differ
// only in the ConsoleTransport they supply (SPICE keyboard vs USB HID).

import (
	"context"
	"fmt"
	"strings"

	"github.com/opencharly/plugin-spice/candy/plugin-spice/params"
	"github.com/opencharly/sdk/kit"
)

// runOpenTerminal decodes `open-terminal` into the shared action.
func runOpenTerminal(ctx context.Context, s *SpiceSession, in *params.SpiceInput) (string, error) {
	timeout := 60
	if len(in.Commands) > 0 && in.Commands[0].TimeoutSec > 0 {
		timeout = in.Commands[0].TimeoutSec
	}
	return kit.OpenTerminal(ctx, spiceTransport{s: s}, kit.TerminalOpen{
		Combo:         in.TerminalCombo,
		PromptAnchors: in.PromptAnchors,
		TimeoutSec:    timeout,
		Artifact:      in.Artifact,
	})
}

// sessionCommands decodes the authored `commands:` into the neutral form.
func sessionCommands(in *params.SpiceInput) ([]kit.ConsoleCommand, error) {
	cmds := make([]kit.ConsoleCommand, 0, len(in.Commands))
	for i, c := range in.Commands {
		if strings.TrimSpace(c.Command) == "" {
			return nil, fmt.Errorf("spice: run-command: command %d is empty", i+1)
		}
		cmds = append(cmds, kit.ConsoleCommand{
			Command:     c.Command,
			Sudo:        c.Sudo,
			Expect:      c.Expect,
			TimeoutSec:  c.TimeoutSec,
			Artifact:    c.Artifact,
			Description: c.Description,
		})
	}
	return cmds, nil
}

// runCommands decodes `run-command` into the shared action.
func runCommands(ctx context.Context, s *SpiceSession, in *params.SpiceInput, sudoPassword string) (string, error) {
	cmds, err := sessionCommands(in)
	if err != nil {
		return "", err
	}
	return kit.RunCommands(ctx, spiceTransport{s: s}, cmds, sudoPassword, in.CloseTerminal, in.PromptAnchors)
}

// runCloseTerminal decodes `close-terminal` into the shared action.
func runCloseTerminal(ctx context.Context, s *SpiceSession) (string, error) {
	return kit.CloseTerminal(ctx, spiceTransport{s: s})
}

// runLUKSUnlock decodes `luks-unlock` into the shared action.
func runLUKSUnlock(ctx context.Context, s *SpiceSession, in *params.SpiceInput) (string, error) {
	return kit.LUKSUnlock(ctx, spiceTransport{s: s}, in.Passphrase, in.Outcomes, nil, 120, in.Artifact)
}

// runBootOrder decodes `boot-order` into the shared action.
func runBootOrder(ctx context.Context, s *SpiceSession, in *params.SpiceInput, sudoPassword string) (string, error) {
	return kit.RunBootOrder(ctx, spiceTransport{s: s}, kit.BootOrder{
		Action:       string(in.BootOrderAction),
		Entry:        in.BootOrderEntry,
		Sequence:     in.BootOrderSequence,
		Binary:       in.BootOrderCommand,
		SudoPassword: sudoPassword,
	})
}

// runFlow decodes `flow` into the shared, bounded state machine.
func runFlow(ctx context.Context, s *SpiceSession, in *params.SpiceInput) (string, error) {
	if strings.TrimSpace(in.FlowStart) == "" {
		return "", fmt.Errorf("spice: flow requires flow_start (the entry node id)")
	}
	if len(in.FlowNodes) == 0 {
		return "", fmt.Errorf("spice: flow requires a non-empty flow_nodes map")
	}
	nodes := make(map[string]kit.ConsoleFlowNode, len(in.FlowNodes))
	for id, n := range in.FlowNodes {
		waits := make([]kit.ConsoleFlowOutcome, 0, len(n.Wait))
		for _, o := range n.Wait {
			waits = append(waits, kit.ConsoleFlowOutcome{
				Name: o.Name, Match: o.Match, Reference: o.Reference,
				MaxDistance: o.MaxDistance, Failure: o.Failure,
			})
		}
		nodes[id] = kit.ConsoleFlowNode{
			ID:          id,
			Description: n.Description,
			Wait:        waits,
			Action: kit.ConsoleFlowAction{
				Key: n.Key, Combo: n.Combo, Text: n.Text,
				Command: n.Command, Sudo: n.Sudo, Expect: n.Expect, CloseTerminal: n.CloseTerminal,
			},
			Transitions: n.Transitions,
			Next:        n.Next,
			Artifact:    n.Artifact,
			TimeoutSec:  n.TimeoutSec,
		}
	}
	res, err := kit.RunConsoleFlow(ctx, spiceTransport{s: s}, kit.ConsoleFlowSpec{
		Start:            in.FlowStart,
		Nodes:            nodes,
		SudoPassword:     in.SudoPassword,
		MaxSteps:         in.FlowMaxSteps,
		MaxLoops:         in.FlowMaxLoops,
		ResumeFromScreen: in.FlowResume,
		ResumeOrder:      in.FlowResumeOrder,
		PromptAnchors:    in.PromptAnchors,
	})
	if err != nil {
		return kit.RenderFlowEvidence(res), fmt.Errorf("spice: flow: %w", err)
	}
	return fmt.Sprintf("flow completed at node %q after %d step(s):\n%s", res.Final, len(res.Steps), kit.RenderFlowEvidence(res)), nil
}
