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
	"time"

	"github.com/opencharly/plugin-spice/candy/plugin-spice/params"
	"github.com/opencharly/sdk/kit"
)

// runOpenTerminal decodes `open-terminal` into the shared action.
func runOpenTerminal(ctx context.Context, s *SpiceSession, in *params.SpiceInput) (string, error) {
	return kit.OpenTerminal(ctx, spiceTransport{s: s}, kit.TerminalOpen{
		Combo:         in.TerminalCombo,
		PromptAnchors: in.PromptAnchors,
		TimeoutSec:    terminalTimeout(in),
		Artifact:      in.Artifact,
	})
}

// terminalTimeout is the open-terminal shell budget: the first command's
// authored timeout, else the shared 60s default. Pure, so the default/mapping
// is unit-locked without a console.
func terminalTimeout(in *params.SpiceInput) int {
	if len(in.Commands) > 0 && in.Commands[0].TimeoutSec > 0 {
		return in.Commands[0].TimeoutSec
	}
	return 60
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
	return kit.RunBootOrder(ctx, spiceTransport{s: s}, buildBootOrder(in, sudoPassword))
}

// buildBootOrder decodes the authored `boot-order` fields into the shared
// action's neutral input. It is pure (no session, no engine), so the
// param→neutral mapping is unit-locked without a VM console.
func buildBootOrder(in *params.SpiceInput, sudoPassword string) kit.BootOrder {
	return kit.BootOrder{
		Action:       string(in.BootOrderAction),
		Entry:        in.BootOrderEntry,
		Sequence:     in.BootOrderSequence,
		Binary:       in.BootOrderCommand,
		SudoPassword: sudoPassword,
	}
}

// runFlow decodes `flow` into the shared, bounded state machine.
func runFlow(ctx context.Context, s *SpiceSession, in *params.SpiceInput) (string, error) {
	spec, err := buildFlowSpec(ctx, in)
	if err != nil {
		return "", err
	}
	res, err := kit.RunConsoleFlow(ctx, spiceTransport{s: s}, spec)
	if err != nil {
		return kit.RenderFlowEvidence(res), fmt.Errorf("spice: flow: %w", err)
	}
	return fmt.Sprintf("flow completed at node %q after %d step(s):\n%s", res.Final, len(res.Steps), kit.RenderFlowEvidence(res)), nil
}

// buildFlowSpec decodes the authored `flow_*` params into the shared, bounded
// flow engine's neutral spec. The two guards and the full node/outcome mapping
// live HERE, so the SPICE decode is unit-testable without a VM console.
func buildFlowSpec(ctx context.Context, in *params.SpiceInput) (kit.ConsoleFlowSpec, error) {
	if strings.TrimSpace(in.FlowStart) == "" {
		return kit.ConsoleFlowSpec{}, fmt.Errorf("spice: flow requires flow_start (the entry node id)")
	}
	if len(in.FlowNodes) == 0 {
		return kit.ConsoleFlowSpec{}, fmt.Errorf("spice: flow requires a non-empty flow_nodes map")
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
	return kit.ConsoleFlowSpec{
		Start:            in.FlowStart,
		Nodes:            nodes,
		SudoPassword:     in.SudoPassword,
		MaxSteps:         in.FlowMaxSteps,
		MaxLoops:         in.FlowMaxLoops,
		ResumeFromScreen: in.FlowResume,
		ResumeOrder:      in.FlowResumeOrder,
		PromptAnchors:    in.PromptAnchors,
		Deadline:         flowDeadline(ctx),
	}, nil
}

// flowDeadlineMargin is how long before the host's per-attempt bound the flow
// stops, leaving time to render and return its evidence.
const flowDeadlineMargin = 20 * time.Second

// flowDeadline derives a flow's wall-clock budget from the dispatch context's
// deadline (the host binds each attempt by its never-hang ceiling), so a long
// flow FAILS CLEANLY with evidence instead of being SIGKILLed mid-node.
func flowDeadline(ctx context.Context) time.Time {
	if d, ok := ctx.Deadline(); ok {
		return d.Add(-flowDeadlineMargin)
	}
	return time.Time{}
}
