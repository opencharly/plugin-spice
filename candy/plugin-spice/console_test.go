package spice

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/opencharly/plugin-spice/candy/plugin-spice/params"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
)

// TestBuildWizardPlan_EntityRecipeAndAnswerMerge pins the entity path WITHOUT a
// reverse channel: a referenced entity supplies the named recipe and the
// TWO-source answers (env < authored), with the step's authored answers winning.
// (The credential-store secret source was removed with the hand-written wire type
// it needed; answers_env — the host environment — is the secret channel.) It calls the REAL buildWizardPlan.
func TestBuildWizardPlan_EntityRecipeAndAnswerMerge(t *testing.T) {
	ent := &params.SpiceConsoleRecipe{
		Recipes: map[string][]params.SpiceConsoleStep{
			"install":    {{WaitFor: "FromInstall", Action: "key", KeyName: "Return"}},
			"first_boot": {{WaitFor: "FromFirstBoot"}},
		},
		AnswersEnv: map[string]string{"username": "ENV_KEY", "password": "ENV_PW"},
		Answers:    map[string]string{"hostname": "authored-host"},
	}
	stub := func(context.Context, *sdk.Executor, string) (*params.SpiceConsoleRecipe, error) {
		return ent, nil
	}
	t.Setenv("ENV_KEY", "envuser")
	t.Setenv("ENV_PW", "envpass")

	in := &params.SpiceInput{Device: "omarchy-kvm", Recipe: "first_boot", Answers: map[string]string{"hostname": "step-wins"}}
	steps, answers, err := buildWizardPlan(context.Background(), nil, in, stub)
	if err != nil {
		t.Fatalf("buildWizardPlan: %v", err)
	}
	if len(steps) != 1 || steps[0].WaitFor != "FromFirstBoot" {
		t.Fatalf("named recipe not selected: %+v", steps)
	}
	if answers["username"] != "envuser" {
		t.Fatalf("answers_env not merged: %v", answers)
	}
	// The env-sourced password resolves from the host environment (ENV_PW).
	if answers["password"] != "envpass" {
		t.Fatalf("answers_env password not merged: %v", answers)
	}
	if answers["hostname"] != "step-wins" {
		t.Fatalf("authored step answer must win: %v", answers)
	}
}

// TestBuildWizardPlan_EntityStepsShortcut pins that the entity's bare steps:
// shortcut (no named recipes) is actually read — the single-recipe device shape.
func TestBuildWizardPlan_EntityStepsShortcut(t *testing.T) {
	ent := &params.SpiceConsoleRecipe{Steps: []params.SpiceConsoleStep{{WaitFor: "shortcut"}}}
	stub := func(context.Context, *sdk.Executor, string) (*params.SpiceConsoleRecipe, error) { return ent, nil }
	in := &params.SpiceInput{Device: "single-recipe-device"}
	steps, _, err := buildWizardPlan(context.Background(), nil, in, stub)
	if err != nil {
		t.Fatalf("buildWizardPlan: %v", err)
	}
	if len(steps) != 1 || steps[0].WaitFor != "shortcut" {
		t.Fatalf("the entity's steps: shortcut was not read: %+v", steps)
	}
}

// TestBuildWizardPlan_RequiresStepsOrDevice pins the guard.
func TestBuildWizardPlan_RequiresStepsOrDevice(t *testing.T) {
	if _, _, err := buildWizardPlan(context.Background(), nil, &params.SpiceInput{}, nil); err == nil {
		t.Fatal("empty wizard input must error")
	}
}

// TestBuildWizardPlan_InlineStepsNoEntity pins the inline-steps path.
func TestBuildWizardPlan_InlineStepsNoEntity(t *testing.T) {
	in := &params.SpiceInput{
		Steps:   []params.SpiceConsoleStep{{WaitFor: "inline-anchor"}},
		Answers: map[string]string{"k": "v"},
	}
	steps, answers, err := buildWizardPlan(context.Background(), nil, in, nil)
	if err != nil {
		t.Fatalf("buildWizardPlan: %v", err)
	}
	if len(steps) != 1 || steps[0].WaitFor != "inline-anchor" || answers["k"] != "v" {
		t.Fatalf("inline path wrong: %+v / %+v", steps, answers)
	}
}

// fakeTransport records input and replays a single fixed screen, so the shared
// engine can be driven end to end with no device. It implements the SAME
// kit.ConsoleTransport the real spiceTransport does, proving the wiring.
type fakeTransport struct {
	keys, combos, types []string
}

func (f *fakeTransport) Capture(context.Context) ([]byte, error) { return []byte("screen-bytes"), nil }
func (f *fakeTransport) PressKey(_ context.Context, k string) error {
	f.keys = append(f.keys, k)
	return nil
}
func (f *fakeTransport) PressCombo(_ context.Context, c string) error {
	f.combos = append(f.combos, c)
	return nil
}
func (f *fakeTransport) Type(_ context.Context, s string) error {
	f.types = append(f.types, s)
	return nil
}

var _ kit.ConsoleTransport = (*fakeTransport)(nil)

// TestWizardEngineWiring drives the shared engine over a fake transport through
// runWizardWith — the SAME function runWizard calls — so the `wizard` method's
// whole production path (recipe conversion, plan build, OCR wait, input dispatch,
// and the exact ConsoleWizard config that ships) is exercised without a VM.
func TestWizardEngineWiring(t *testing.T) {
	in := &params.SpiceInput{
		Steps: []params.SpiceConsoleStep{
			{WaitFor: "always-here", Action: "key", KeyName: "Return", Description: "accept"},
			{WaitFor: "always-here", Action: "type", Text: "{{user}}", Description: "type user"},
		},
		Answers: map[string]string{"user": "someone"},
	}
	steps, answers, err := buildWizardPlan(context.Background(), nil, in, nil)
	if err != nil {
		t.Fatalf("buildWizardPlan: %v", err)
	}
	_ = steps
	_ = answers
	ft := &fakeTransport{}
	// Drive the PRODUCTION wiring: runWizardWith builds the ConsoleWizard exactly
	// as runWizard does (production PollInterval/timeout defaults). Only OCR is
	// substituted, because a fabricated screen cannot yield real tesseract text.
	fakeOCR := func([]byte) (string, error) { return "always-here", nil }
	out, err := runWizardWith(context.Background(), nil, ft, fakeOCR, in)
	if err != nil {
		t.Fatalf("wizard run: %v", err)
	}
	if !strings.Contains(out, "step 2") {
		t.Fatalf("engine should report both steps ran, got %q", out)
	}
	if len(ft.keys) != 1 || ft.keys[0] != "Return" {
		t.Fatalf("key not pressed: %+v", ft.keys)
	}
	if len(ft.types) != 1 || ft.types[0] != "someone" {
		t.Fatalf("answer substitution / type failed: %+v", ft.types)
	}
}

// TestComboScancodes pins the pure chord resolver.
func TestComboScancodes(t *testing.T) {
	codes, err := comboScancodes("ctrl+c")
	if err != nil || len(codes) != 2 {
		t.Fatalf("ctrl+c: %v %v", codes, err)
	}
	if _, err := comboScancodes("ctrl+nope"); err == nil {
		t.Fatal("an unknown key must be rejected")
	}
	// The empty/whitespace-only cases must reach the DEDICATED empty-combo error
	// (not fall through to "unknown key"), so the guard is reachable + tested.
	for _, c := range []string{"", "   "} {
		_, err := comboScancodes(c)
		if err == nil {
			t.Fatalf("comboScancodes(%q) must be rejected", c)
		}
		if !strings.Contains(err.Error(), "empty key combo") {
			t.Fatalf("comboScancodes(%q) must report the empty-combo error, got %v", c, err)
		}
	}
}

// TestSpiceTransportCapture exercises the REAL spiceTransport.Capture over a real
// *SpiceSession whose driver already holds a decoded frame — the in-package test
// can populate the unexported driver directly, so the production adapter code
// (WaitForDisplay → Display → PNG encode) executes with no wire. PNG bytes that
// decode back to the same bounds prove it.
func TestSpiceTransportCapture(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	s := &SpiceSession{driver: newSpiceDriver()}
	s.driver.displayImg = img

	tr := spiceTransport{s: s}
	pngBytes, err := tr.Capture(context.Background())
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	decoded, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("captured bytes are not a PNG: %v", err)
	}
	if decoded.Bounds() != img.Bounds() {
		t.Fatalf("captured bounds %v, want %v", decoded.Bounds(), img.Bounds())
	}
}

// TestSpiceTransportCapture_NoFrame pins the failure when no frame is present.
func TestSpiceTransportCapture_NoFrame(t *testing.T) {
	s := &SpiceSession{driver: newSpiceDriver()}
	tr := spiceTransport{s: s}
	if _, err := tr.Capture(context.Background()); err == nil {
		t.Fatal("Capture with no display frame must error")
	}
}

// TestChordEvents pins the EXACT wire sequence a modifier chord sends, including
// the reverse-order release — the path the added a-z keys exist for (`ctrl+c`,
// the Omarchy deferred-provisioning combo). pressCombo's own body is the loop
// over these events; this test locks the events themselves.
func TestChordEvents(t *testing.T) {
	downs, ups, err := chordEvents("ctrl+c")
	if err != nil {
		t.Fatalf("chordEvents(ctrl+c): %v", err)
	}
	if len(downs) != 2 || len(ups) != 2 {
		t.Fatalf("ctrl+c must produce 2 downs + 2 ups, got %d/%d", len(downs), len(ups))
	}
	// DOWN order: ctrl then c. UP order: c then ctrl (reverse).
	if string(downs[0]) == string(downs[1]) {
		t.Fatal("ctrl+c must press two DISTINCT scancodes")
	}
	if string(ups[0]) != string(downs[1]) || string(ups[1]) != string(downs[0]) {
		t.Fatalf("release must be the reverse of press: downs=%v ups=%v", downs, ups)
	}
	if _, _, err := chordEvents("nope+x"); err == nil {
		t.Fatal("an unknown key in a chord must error")
	}
}

// TestResolveConsoleRecipe_NilExecutor pins the resolver's guard: with no host
// reverse channel (a bare command, not a deploy/check step) it must fail with a
// clear message rather than nil-panic. The reverse-channel body itself
// (recipeProjectDir -> loaderkit) needs a host plugin dispatch — a boundary this
// standalone test cannot stand up — so it is covered by the live consumer bed
// (check-omarchy-vm-console), not by a stub.
func TestResolveConsoleRecipe_NilExecutor(t *testing.T) {
	_, err := resolveConsoleRecipe(context.Background(), nil, "omarchy-kvm")
	if err == nil {
		t.Fatal("resolveConsoleRecipe with no executor must error, not panic")
	}
	if !strings.Contains(err.Error(), "reverse channel") {
		t.Fatalf("the no-executor error must name the reverse channel, got %v", err)
	}
}
