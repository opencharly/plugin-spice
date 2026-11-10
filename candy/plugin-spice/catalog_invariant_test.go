package spice

// catalog_invariant_test.go pins the invariant the jetkvm, cua, punktfunk, gh, omarchy and
// herdr siblings keep and this plugin needs for the same reason: every method the SCHEMA
// advertises must be reachable, and every method the plugin routes must be in the schema.
// Without it a method can sit in the catalog as a phantom (advertised but unimplemented): the
// schema validates the authored `spice: …` step, and the step then fails only at dispatch with
// an unknown-method error, so the catalog and the skill that documents it mislead the agent
// that dispatches on it.
//
// THIS repo splits its routing in two, and the invariant has to read both halves or it is
// wrong in the most dangerous direction: `provider.go` special-cases `session` BEFORE dialing
// (the detached recorder holds the SPICE wire, so the provider never connects), and every
// other method reaches methods.go's `dispatch`. A test that read only the switch would report
// `session` as a phantom; one that read only the special case would report eight.
//
// It also pins the ONE Go-side word set keyed by method: `requiredModifiers`. A typo in a key
// there does not fail — it silently DISABLES a required-field check for the method it was
// meant to guard, so `spice: {method: "screenshot"}` would dispatch without an artifact path.

import (
	"os"
	"regexp"
	"sort"
	"testing"
)

var (
	dispatchSwitch = regexp.MustCompile(`(?s)func dispatch\(.*?\n\}`)
	caseArm        = regexp.MustCompile(`(?m)^[ \t]*case[ \t]+((?:"(?:[^"\\]|\\.)*"[ \t]*,?[ \t]*)+):`)
	quotedWord     = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)
	// provider.go routes one method before dispatch, as a plain comparison.
	preDispatch = regexp.MustCompile(`method == "([a-z][a-z0-9-]*)"`)
	// requiredModifiers is a map literal of method -> required input keys.
	modifiersMap = regexp.MustCompile(`(?s)var requiredModifiers = map\[string\]\[\]string\{(.*?)\n\}`)
	modifierKey  = regexp.MustCompile(`"([a-z][a-z0-9-]*)":`)
)

func readSource(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// schemaMethods returns the sorted method names the SERVED #SpiceInput.method enum accepts. It
// reads the schema through the CUE API rather than its text: the enum is now §10.1 S3, and a
// text parse of the union would find ZERO methods and leave every invariant here passing
// vacuously.
func schemaMethods(t *testing.T) []string {
	t.Helper()
	return fieldWordList(t, servedSchema(t), "SpiceInput", "method")
}

// routedMethods returns every method the plugin routes: methods.go's dispatch arms plus
// provider.go's pre-dispatch comparison.
func routedMethods(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	src := readSource(t, "methods.go")
	fn := dispatchSwitch.FindString(src)
	if fn == "" {
		t.Fatal("could not locate dispatch in methods.go")
	}
	for _, arm := range caseArm.FindAllStringSubmatch(fn, -1) {
		for _, q := range quotedWord.FindAllStringSubmatch(arm[1], -1) {
			seen[q[1]] = true
		}
	}
	prov := readSource(t, "provider.go")
	pre := preDispatch.FindAllStringSubmatch(prov, -1)
	if len(pre) == 0 {
		t.Fatal("could not locate the pre-dispatch `method == \"…\"` comparison in provider.go — if it moved, this test must move with it, not silently pass")
	}
	for _, m := range pre {
		seen[m[1]] = true
	}
	out := make([]string, 0, len(seen))
	for w := range seen {
		out = append(out, w)
	}
	sort.Strings(out)
	return out
}

// requiredModifierKeys returns the method keys of methods.go's requiredModifiers map.
func requiredModifierKeys(t *testing.T) []string {
	t.Helper()
	src := readSource(t, "methods.go")
	block := modifiersMap.FindStringSubmatch(src)
	if block == nil {
		t.Fatal("could not locate the requiredModifiers map in methods.go")
	}
	seen := map[string]bool{}
	for _, m := range modifierKey.FindAllStringSubmatch(block[1], -1) {
		seen[m[1]] = true
	}
	out := make([]string, 0, len(seen))
	for w := range seen {
		out = append(out, w)
	}
	sort.Strings(out)
	return out
}

// TestCatalogHasNoPhantomMethods asserts every schema method is routed — no method is
// advertised that the plugin cannot act on.
func TestCatalogHasNoPhantomMethods(t *testing.T) {
	reachable := map[string]bool{}
	routed := routedMethods(t)
	for _, m := range routed {
		reachable[m] = true
	}
	if len(reachable) == 0 {
		t.Fatal("the dispatch surface is empty — the invariant would pass over nothing")
	}
	for _, m := range schemaMethods(t) {
		if !reachable[m] {
			t.Errorf("schema method %q is a PHANTOM: neither provider.go's pre-dispatch comparison nor dispatch routes it — add an implementation or remove it from the schema", m)
		}
	}
}

// TestDispatchHasNoPhantomArms is the other direction: an arm for a word the schema no longer
// accepts is dead code, and it hides a rename that only half landed.
func TestDispatchHasNoPhantomArms(t *testing.T) {
	advertised := map[string]bool{}
	for _, m := range schemaMethods(t) {
		advertised[m] = true
	}
	for _, m := range routedMethods(t) {
		if !advertised[m] {
			t.Errorf("the plugin routes %q, which the served #SpiceInput.method enum does NOT accept — the arm is unreachable", m)
		}
	}
}

// TestEverySchemaMethodIsRouted is the two-way set equality in one assertion.
func TestEverySchemaMethodIsRouted(t *testing.T) {
	schema := schemaMethods(t)
	routed := routedMethods(t)
	if len(schema) != len(routed) {
		t.Fatalf("served enum has %d methods but the plugin routes %d — the catalog and the dispatch have drifted\n schema=%v\n routed=%v",
			len(schema), len(routed), schema, routed)
	}
	for i := range schema {
		if schema[i] != routed[i] {
			t.Errorf("served method %q has no matching routed arm (routed=%v)", schema[i], routed)
			break
		}
	}
}

// TestRequiredModifierKeysAreSchemaMethods pins the Go-side map whose keys ARE method words. A
// key that names no method is not a harmless typo: the check it carries never runs and the
// method it was meant to guard dispatches without its required input.
func TestRequiredModifierKeysAreSchemaMethods(t *testing.T) {
	advertised := map[string]bool{}
	for _, m := range schemaMethods(t) {
		advertised[m] = true
	}
	keys := requiredModifierKeys(t)
	if len(keys) == 0 {
		t.Fatal("requiredModifiers has no keys — the check would pass over nothing")
	}
	for _, k := range keys {
		if !advertised[k] {
			t.Errorf("requiredModifiers is keyed by %q, which the served #SpiceInput.method enum does NOT accept — the required-input check for that method never runs", k)
		}
	}
}
