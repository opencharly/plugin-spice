package spice

// console.go wires the SPICE transport into the SHARED, transport-agnostic
// console-wizard engine (sdk/kit's ConsoleWizard) and implements the `wizard`
// method: drive a text-console wizard (an OS installer, a first-boot flow) on
// the VM's SPICE console, OCR-gating each screen.
//
// It is the SPICE SIBLING of plugin-jetkvm's install driver: the SAME engine and
// the SAME recipe drive either transport (R3), so an Omarchy recipe authored
// once works over an IP-KVM (HID) or a VM console (SPICE keyboard). This file
// supplies only the SPICE-specific transport, plus the transport-neutral recipe
// entity resolution (a `kind: jetkvm` console-recipe entity is read by both).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"strings"
	"time"

	"github.com/opencharly/plugin-spice/candy/plugin-spice/params"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// spiceTransport adapts a live SpiceSession to kit.ConsoleTransport.
type spiceTransport struct {
	s *SpiceSession
}

func (t spiceTransport) Capture(ctx context.Context) ([]byte, error) {
	if err := t.s.WaitForDisplay(5 * time.Second); err != nil {
		return nil, err
	}
	img := t.s.Display()
	if img == nil {
		return nil, fmt.Errorf("no display frame available")
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encoding frame: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (t spiceTransport) PressKey(ctx context.Context, name string) error {
	_, err := runKey(t.s, name)
	return err
}

func (t spiceTransport) PressCombo(ctx context.Context, combo string) error {
	return pressCombo(t.s, combo)
}

func (t spiceTransport) Type(ctx context.Context, text string) error {
	_, err := runType(t.s, text)
	return err
}

// runWizard drives a console recipe on the VM's SPICE session through the shared
// engine. It resolves a referenced console-recipe entity (the transport-neutral
// recipe home) when `device:`/`recipe:` are authored, then runs the recipe.
func runWizard(ctx context.Context, ex *sdk.Executor, brokerID uint32, s *SpiceSession, in *params.SpiceInput) (string, error) {
	steps, answers, err := buildWizardPlan(ctx, ex, brokerID, in, nil)
	if err != nil {
		return "", err
	}
	w := &kit.ConsoleWizard{
		Steps:     steps,
		Answers:   answers,
		Transport: spiceTransport{s: s},
	}
	return w.Run(ctx)
}

// buildWizardPlan resolves the steps + answers a wizard will run, WITHOUT a
// transport. It is the one place entity resolution, recipe selection and the
// three-source answer merge live, so the contract is unit-testable with a stubbed
// entity resolver and a fake transport (R3 — one implementation, exercised by
// both the live run and the test).
//
// resolveEnt is injected: nil means "resolve a referenced entity over the
// reverse channel"; a test supplies a stub so no channel is needed.
func buildWizardPlan(ctx context.Context, ex *sdk.Executor, brokerID uint32, in *params.SpiceInput, resolveEnt func(context.Context, *sdk.Executor, string) (*params.SpiceConsoleRecipe, error)) ([]kit.ConsoleStep, map[string]string, error) {
	if len(in.Steps) == 0 && in.Device == "" {
		return nil, nil, fmt.Errorf("spice: wizard requires a steps recipe (author `steps:` inline, or reference a console-recipe entity with `device:`/`recipe:`)")
	}
	if resolveEnt == nil {
		resolveEnt = resolveConsoleRecipe
	}
	answers := in.Answers
	steps := paramsStepsToKit(in.Steps)
	if in.Device != "" {
		ent, err := resolveEnt(ctx, ex, in.Device)
		if err != nil {
			return nil, nil, err
		}
		if len(steps) == 0 {
			recipeName := in.Recipe
			if recipeName == "" {
				recipeName = defaultRecipeName
			}
			steps, err = kit.SelectRecipe(paramsRecipesToKit(ent.Recipes), steps, recipeName)
			if err != nil {
				return nil, nil, fmt.Errorf("spice: device %q: %w", in.Device, err)
			}
		}
		// The entity's three answer sources merge lowest-to-highest, with the
		// step's authored answers winning — the SAME contract the jetkvm
		// transport applies (answers_env from the host environment, then
		// answer_secrets, then authored answers).
		merged := kit.MergeAnswers(ent.AnswersEnv, ent.AnswerSecrets, ent.Answers, os.Getenv, func(key string) string {
			return credentialLookup(ctx, brokerID, key)
		})
		for name, v := range in.Answers {
			merged[name] = v
		}
		answers = merged
	}
	return steps, answers, nil
}

// pressCombo presses a modifier chord ("ctrl+c", "ctrl+alt+Delete"). It resolves
// each `+`-separated token to a scancode and holds all keys down, then releases
// them in reverse — the same chord semantics the jetkvm transport uses.
func pressCombo(s *SpiceSession, combo string) error {
	if err := s.WaitForInputs(5 * time.Second); err != nil {
		return err
	}
	parts := strings.Split(combo, "+")
	if len(parts) == 0 {
		return fmt.Errorf("empty key combo")
	}
	codes := make([]uint8, 0, len(parts))
	for _, p := range parts {
		code, ok := spiceKeyNameToScancode[strings.ToLower(strings.TrimSpace(p))]
		if !ok {
			return fmt.Errorf("unknown key in combo: %s", p)
		}
		codes = append(codes, code)
	}
	in := s.Inputs()
	for _, c := range codes {
		in.OnKeyDown(encodeScancode(c))
	}
	time.Sleep(50 * time.Millisecond)
	for i := len(codes) - 1; i >= 0; i-- {
		in.OnKeyUp(encodeScancode(codes[i]))
	}
	return nil
}

// --- console-recipe entity resolution ---------------------------------------

// recipeKindWord is the kind the console-recipe entity is authored under. It is
// the jetkvm kind: the recipe is transport-neutral DATA, so its home is the
// generic KVM device entity both transports read — not a spice-specific kind.
const recipeKindWord = "jetkvm"

// defaultRecipeName is the recipe a step drives when it authors no `recipe:`.
const defaultRecipeName = "install"

// resolveConsoleRecipe loads the project out-of-process and returns the recipe
// half of the named console-recipe entity. An absent entity is a clear error.
func resolveConsoleRecipe(ctx context.Context, ex *sdk.Executor, name string) (*params.SpiceConsoleRecipe, error) {
	if ex == nil {
		return nil, fmt.Errorf("spice: resolving console-recipe entity %q needs a host reverse channel (run it inside a deploy/check step, not a bare command)", name)
	}
	dir, err := recipeProjectDir(ctx, ex, name)
	if err != nil {
		return nil, fmt.Errorf("spice: resolving console-recipe entity %q: %w", name, err)
	}
	uf, ok, err := loaderkit.LoadUnifiedViaExecutor(ctx, ex, dir)
	if err != nil {
		return nil, fmt.Errorf("spice: loading project for console-recipe entity %q: %w", name, err)
	}
	if !ok || uf == nil {
		return nil, fmt.Errorf("spice: resolving console-recipe entity %q: no charly.yml loaded from %s", name, dir)
	}
	body, found := loaderkit.ResolveKindEntityBody(uf, recipeKindWord, name)
	if !found {
		return nil, fmt.Errorf("spice: no kind:%s console-recipe entity named %q in %s", recipeKindWord, name, dir)
	}
	var ent params.SpiceConsoleRecipeEntity
	if err := json.Unmarshal(body, &ent); err != nil {
		return nil, fmt.Errorf("spice: decoding console-recipe entity %q: %w", name, err)
	}
	if ent.Installer == nil {
		return nil, fmt.Errorf("spice: console-recipe entity %q carries no installer recipe", name)
	}
	return ent.Installer, nil
}

// recipeProjectDir resolves the project directory over the reverse channel — the
// canonical "deploy-plugins-connect" host seam, the same preamble plugin-jetkvm
// uses before feeding the plugin-side self-load helpers.
func recipeProjectDir(ctx context.Context, ex *sdk.Executor, name string) (string, error) {
	reqJSON, err := json.Marshal(spec.DeployPluginsConnectRequest{Path: name})
	if err != nil {
		return "", err
	}
	out, err := ex.HostBuild(ctx, "deploy-plugins-connect", reqJSON)
	if err != nil {
		return "", err
	}
	var reply spec.DeployPluginsConnectReply
	if err := json.Unmarshal(out, &reply); err != nil {
		return "", fmt.Errorf("spice: decode deploy-plugins-connect reply: %w", err)
	}
	return reply.Dir, nil
}

// paramsStepsToKit converts the schema-generated param steps into the shared
// engine's neutral step type.
func paramsStepsToKit(steps []params.SpiceConsoleStep) []kit.ConsoleStep {
	out := make([]kit.ConsoleStep, 0, len(steps))
	for _, s := range steps {
		out = append(out, kit.ConsoleStep{
			WaitFor: s.WaitFor, Action: s.Action, Key: s.KeyName, Combo: s.Combo,
			Text: s.Text, TimeoutSec: s.TimeoutSec, Optional: s.Optional,
			Artifact: s.Artifact, Description: s.Description,
		})
	}
	return out
}

// paramsRecipesToKit converts a map of named recipes into the neutral step form.
func paramsRecipesToKit(recipes map[string][]params.SpiceConsoleStep) map[string][]kit.ConsoleStep {
	if len(recipes) == 0 {
		return nil
	}
	out := make(map[string][]kit.ConsoleStep, len(recipes))
	for name, steps := range recipes {
		out[name] = paramsStepsToKit(steps)
	}
	return out
}
