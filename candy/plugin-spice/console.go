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

// The transport's readiness + chord-hold timings, NAMED rather than inline
// literals:
//   - readinessWait bounds WaitForDisplay/WaitForInputs — the async channel's
//     readiness poll (the Shells-com library populates them from per-channel
//     goroutines with no readiness callback, so a bounded poll is the only
//     synchronization it exposes).
//   - chordHold is how long a modifier chord is held before release — the SPICE
//     transport's own hold (plugin-jetkvm's key verb defaults to 40ms and is
//     configurable via hold_ms; SPICE has no such knob, so its transport fixes one
//     value large enough that the guest's key handler registers the chord).
const (
	readinessWait = 5 * time.Second
	chordHold     = 50 * time.Millisecond
)

// spiceTransport adapts a live SpiceSession to kit.ConsoleTransport.
type spiceTransport struct {
	s *SpiceSession
}

func (t spiceTransport) Capture(ctx context.Context) ([]byte, error) {
	if err := t.s.WaitForDisplay(readinessWait); err != nil {
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
func runWizard(ctx context.Context, ex *sdk.Executor, s *SpiceSession, in *params.SpiceInput) (string, error) {
	return runWizardWith(ctx, ex, spiceTransport{s: s}, nil, in)
}

// runWizardWith is runWizard over an injected transport and OCR. Production
// passes the real spiceTransport and a nil OCR (the engine's default OCRBytes);
// a test passes a fake transport and a fake OCR, because a FABRICATED screen
// cannot yield real tesseract text. Every element that ships is therefore
// exercised: the plan build, the three-source merge, and the engine drive with
// the PRODUCTION PollInterval/timeout defaults — only the two boundaries a
// synthetic screen forces (screen bytes + their OCR) are substituted.
func runWizardWith(ctx context.Context, ex *sdk.Executor, tr kit.ConsoleTransport, ocr func([]byte) (string, error), in *params.SpiceInput) (string, error) {
	steps, answers, err := buildWizardPlan(ctx, ex, in, nil)
	if err != nil {
		return "", err
	}
	w := &kit.ConsoleWizard{
		Steps:     steps,
		Answers:   answers,
		Transport: tr,
		OCR:       ocr,
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
func buildWizardPlan(ctx context.Context, ex *sdk.Executor, in *params.SpiceInput, resolveEnt func(context.Context, *sdk.Executor, string) (*params.SpiceConsoleRecipe, error)) ([]kit.ConsoleStep, map[string]string, error) {
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
			// The entity's `steps:` is the single-recipe SHORTCUT; SelectRecipe
			// returns it when no named recipe matches and name=="install".
			steps, err = kit.SelectRecipe(paramsRecipesToKit(ent.Recipes), paramsStepsToKit(ent.Steps), recipeName)
			if err != nil {
				return nil, nil, fmt.Errorf("spice: device %q: %w", in.Device, err)
			}
		}
		// The entity's answer sources merge lowest-to-highest, with the step's
		// authored answers winning: answers_env (the HOST environment) then the
		// entity's authored answers. There is no credential-store source: the
		// env path is the secret channel for a wizard over SPICE (an earlier
		// draft's answer_secrets feature was removed with the hand-written wire
		// type it needed).
		merged := kit.MergeAnswers(ent.AnswersEnv, nil, ent.Answers, os.Getenv, nil)
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
	if err := s.WaitForInputs(readinessWait); err != nil {
		return err
	}
	downs, ups, err := chordEvents(combo)
	if err != nil {
		return err
	}
	in := s.Inputs()
	for _, b := range downs {
		in.OnKeyDown(b)
	}
	time.Sleep(chordHold)
	for _, b := range ups {
		in.OnKeyUp(b)
	}
	return nil
}

// chordEvents resolves a chord to its encoded DOWN sequence and the matching UP
// sequence (released in reverse). It is PURE, so the exact wire bytes a `ctrl+c`
// (the Omarchy combo) sends are unit-locked with no session: DOWN = each
// scancode in order, UP = the same scancodes reversed.
func chordEvents(combo string) (downs, ups [][]byte, err error) {
	codes, err := comboScancodes(combo)
	if err != nil {
		return nil, nil, err
	}
	downs = make([][]byte, 0, len(codes))
	for _, c := range codes {
		downs = append(downs, encodeScancode(c))
	}
	ups = make([][]byte, 0, len(codes))
	for i := len(codes) - 1; i >= 0; i-- {
		ups = append(ups, encodeScancode(codes[i]))
	}
	return downs, ups, nil
}

// comboScancodes resolves a modifier chord ("ctrl+c", "ctrl+alt+Delete") to its
// ordered scancode list. It is PURE, so the chord contract (split on '+', trim,
// case-insensitive key names, unknowns rejected) is unit-locked with no session.
func comboScancodes(combo string) ([]uint8, error) {
	// Handle an empty/whitespace-only chord BEFORE splitting: strings.Split("", "+")
	// yields [""], so without this check the loop would report the empty segment as
	// "unknown key" (an unhelpful message) and the empty-combo error would be
	// unreachable.
	if strings.TrimSpace(combo) == "" {
		return nil, fmt.Errorf("empty key combo")
	}
	parts := strings.Split(combo, "+")
	codes := make([]uint8, 0, len(parts))
	for _, p := range parts {
		code, ok := spiceKeyNameToScancode[strings.ToLower(strings.TrimSpace(p))]
		if !ok {
			return nil, fmt.Errorf("unknown key in combo: %s", p)
		}
		codes = append(codes, code)
	}
	return codes, nil
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
