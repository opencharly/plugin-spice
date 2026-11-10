package spice

// schema_served_test.go is the schema-LOAD gate, and the §10.1 S3 gate.
//
// `charly box validate` does NOT compile a served schema: a dangling reference inside a
// served def is green at plugin load and only surfaces when the host validates an authored
// step (opencharly/spec#301). So this file composes the plugin's OWN Describe reply and
// compiles the body it serves, with the same CUE library the runtime links — the served
// body, not the file on disk, is the surface the host splices onto its base and validates
// every authored `spice:` step against.
//
// It pins the S3 half of D-PAT-3 as well: every closed set of words is a vocabulary table,
// its word list is DERIVED from that table, and the enum the author meets is `or()` of the
// derived list.
//
// THIS repo's own trap is the shared field NAME: `#SpiceInput.action` and
// `#SpiceConsoleStep.action` are two different word sets, and a sweep that gives them one
// table would widen both. TestTheTwoActionSetsAreDistinct pins that they stay apart.

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	pb "github.com/opencharly/spec/proto"
)

// vocabHarness LIFTS the hidden vocabulary tables and their derived word lists into one
// public definition. An `_`-prefixed label is hidden by CUE and cannot be looked up from
// outside the compilation unit, so the served body alone exposes only the enums; this
// fragment adds no semantics, it re-exports the tables the schema already carries so the
// test can read them instead of restating their words.
const vocabHarness = `
#TestVocabulary: {
	tables: {
		method:        _method
		action:        _action
		consoleAction: _consoleAction
	}
	words: {
		method:        _methodWord
		action:        _actionWord
		consoleAction: _consoleActionWord
	}
}
`

// vocabProbe pairs a vocabulary table (named as in vocabHarness) with ONE authored body that
// uses a word from it, so the enum is exercised through the same def the host validates
// against.
type vocabProbe struct {
	name string
	use  func(word string) string
}

var vocabProbes = []vocabProbe{
	{"method", func(w string) string {
		return fmt.Sprintf(`{#SpiceInput: {method: %q}}`, w)
	}},
	{"action", func(w string) string {
		return fmt.Sprintf(`{#SpiceInput: {method: "record", action: %q}}`, w)
	}},
	{"consoleAction", func(w string) string {
		return fmt.Sprintf(`{#SpiceConsoleStep: {wait_for: "x", action: %q}}`, w)
	}},
}

// servedBody returns the CUE source the plugin serves over Describe — the body the host
// splices onto its base, which is what every test below judges.
func servedBody(t *testing.T) string {
	t.Helper()
	caps, err := NewMeta().Describe(context.Background(), &pb.Empty{})
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	body := caps.GetSchemaCue()
	if body == "" {
		t.Fatal("Describe served an EMPTY CUE body")
	}
	return body
}

func servedSchema(t *testing.T) cue.Value {
	t.Helper()
	return compile(t, servedBody(t)+vocabHarness)
}

func compile(t *testing.T, src string) cue.Value {
	t.Helper()
	v := cuecontext.New().CompileString(src)
	if err := v.Err(); err != nil {
		t.Fatalf("served schema does not compile: %v", err)
	}
	return v
}

// fieldWordList returns the sorted words of the enum an AUTHOR meets, addressed as a def plus
// one of its fields. The field is looked up as an OPTIONAL constraint, because `#SpiceInput`'s
// `action` and `#SpiceConsoleStep`'s `action` are both optional.
func fieldWordList(t *testing.T, v cue.Value, def, field string) []string {
	t.Helper()
	sels := []cue.Selector{cue.Def(def)}
	if field != "" {
		sels = append(sels, cue.Str(field).Optional())
	}
	seen := map[string]bool{}
	collectEnumWords(t, "#"+def+"."+field, v.LookupPath(cue.MakePath(sels...)), 0, seen)
	words := make([]string, 0, len(seen))
	for w := range seen {
		words = append(words, w)
	}
	sort.Strings(words)
	return words
}

func collectEnumWords(t *testing.T, label string, val cue.Value, depth int, seen map[string]bool) {
	t.Helper()
	if err := val.Err(); err != nil {
		t.Fatalf("%s does not resolve in the served schema: %v", label, err)
	}
	if depth > 16 {
		t.Fatalf("%s: the disjunction nests deeper than 16 levels", label)
	}
	if val.IncompleteKind() == cue.StringKind && val.IsConcrete() {
		w, err := val.String()
		if err != nil {
			t.Fatalf("%s: a concrete string does not stringify: %v", label, err)
		}
		seen[w] = true
		return
	}
	_, alts := val.Expr()
	if len(alts) == 0 {
		t.Fatalf("%s has no alternatives — it is not a word list", label)
	}
	for _, alt := range alts {
		collectEnumWords(t, label, alt, depth+1, seen)
	}
}

// liftedVocabulary is the harness's tables and derived lists, decoded once.
type liftedVocabulary struct {
	Tables map[string]map[string]any
	Words  map[string][]string
}

func liftVocabulary(t *testing.T, v cue.Value) liftedVocabulary {
	t.Helper()
	var out liftedVocabulary
	if err := v.LookupPath(cue.ParsePath("#TestVocabulary")).Decode(&out); err != nil {
		t.Fatalf("lift the vocabulary tables: %v", err)
	}
	return out
}

// TestAdvertisedInputDefResolvesInTheServedSchema reads the plugin's REAL advertisement: the
// verb capability names the def it validates `spice:` steps against, and a def that does not
// resolve makes the load gate silently validate against nothing.
func TestAdvertisedInputDefResolvesInTheServedSchema(t *testing.T) {
	caps, err := NewMeta().Describe(context.Background(), &pb.Empty{})
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	v := servedSchema(t)
	verbs := 0
	for _, pc := range caps.GetProvided() {
		if pc.GetClass() != "verb" {
			continue
		}
		verbs++
		if pc.GetWord() != "spice" {
			t.Errorf("verb capability word = %q, want %q", pc.GetWord(), "spice")
		}
		if pc.GetInputDef() != "#SpiceInput" {
			t.Errorf("verb capability InputDef = %q, want %q", pc.GetInputDef(), "#SpiceInput")
		}
		if err := v.LookupPath(cue.ParsePath(pc.GetInputDef())).Err(); err != nil {
			t.Errorf("advertised def %q does not resolve in the served schema: %v", pc.GetInputDef(), err)
		}
	}
	if verbs != 1 {
		t.Errorf("Describe advertises %d verb capabilities, want exactly 1", verbs)
	}
}

// TestS3VocabularyTablesDriveTheirEnums proves, for every vocabulary table, that (a) the
// derived word list IS the table's row set, and (b) each word is accepted where an author uses
// it while a non-word is refused.
func TestS3VocabularyTablesDriveTheirEnums(t *testing.T) {
	v := servedSchema(t)
	ctx := v.Context()
	lifted := liftVocabulary(t, v)
	if len(lifted.Tables) != len(vocabProbes) {
		t.Errorf("the schema carries %d vocabulary tables but %d are probed — a table nothing exercises is a table nothing keeps wired",
			len(lifted.Tables), len(vocabProbes))
	}
	for _, p := range vocabProbes {
		rows, ok := lifted.Tables[p.name]
		if !ok {
			t.Errorf("no vocabulary table %q in the served schema", p.name)
			continue
		}
		if len(rows) == 0 {
			t.Errorf("vocabulary table %q has no rows", p.name)
			continue
		}
		words, ok := lifted.Words[p.name]
		if !ok {
			t.Errorf("no derived word list for %q in the served schema", p.name)
			continue
		}
		keys := make([]string, 0, len(rows))
		for w := range rows {
			keys = append(keys, w)
		}
		sort.Strings(keys)
		sort.Strings(words)
		if fmt.Sprint(keys) != fmt.Sprint(words) {
			t.Errorf("%q's derived list %v is not its table's row set %v", p.name, words, keys)
		}
		for _, w := range keys {
			if err := v.Unify(ctx.CompileString(p.use(w))).Err(); err != nil {
				t.Errorf("%q word %q is refused where an author uses it: %v", p.name, w, err)
			}
		}
		if err := v.Unify(ctx.CompileString(p.use("zzz-not-a-word"))).Err(); err == nil {
			t.Errorf("%q accepts a non-word — the enum is not the table", p.name)
		}
	}
}

// TestServedEnumsAreTheDerivedListsCrossCheck reads each enum through the FIELD an author
// writes, not through the table: the table and the enum can each be right while the field
// still cites its own literal. Note it addresses the TWO `action` fields separately — the
// cross-check is what tells `_action` from `_consoleAction`.
func TestServedEnumsAreTheDerivedListsCrossCheck(t *testing.T) {
	v := servedSchema(t)
	lifted := liftVocabulary(t, v)
	for _, tc := range []struct{ def, field, table string }{
		{"SpiceInput", "method", "method"},
		{"SpiceInput", "action", "action"},
		{"SpiceConsoleStep", "action", "consoleAction"},
	} {
		fieldWords := fieldWordList(t, v, tc.def, tc.field)
		tableWords := append([]string(nil), lifted.Words[tc.table]...)
		sort.Strings(tableWords)
		if fmt.Sprint(fieldWords) != fmt.Sprint(tableWords) {
			t.Errorf("#%s.%s accepts %v but the _%s table's word list is %v — the field cites its own literal instead of the derivation",
				tc.def, tc.field, fieldWords, tc.table, tableWords)
		}
	}
}

// TestTheTwoActionSetsAreDistinct is this repo's own hazard, pinned. `#SpiceInput.action` and
// `#SpiceConsoleStep.action` share a field NAME and nothing else: one is a record/session
// action, the other a console step's. A sweep that gives them one table widens both, and the
// widening is silent — `spice: {method: "record", action: "key-combo"}` would validate and then
// fail at dispatch, and a console step carrying `action: "stop"` would validate and do nothing.
//
// The check is direction-correct: for each of the two fields, every word of the OTHER field's
// table must be REFUSED by it. A merged set accepts them on both sides and fails here twice.
func TestTheTwoActionSetsAreDistinct(t *testing.T) {
	v := servedSchema(t)
	ctx := v.Context()
	lifted := liftVocabulary(t, v)
	fields := []struct {
		name, def string
		probe     vocabProbe
	}{
		{"_action / #SpiceInput.action", "SpiceInput", vocabProbes[1]},
		{"_consoleAction / #SpiceConsoleStep.action", "SpiceConsoleStep", vocabProbes[2]},
	}
	for _, f := range fields {
		other := "action"
		if f.def == "SpiceInput" {
			other = "consoleAction"
		}
		foreign := lifted.Words[other]
		if len(foreign) == 0 {
			t.Fatalf("the _%s table has no words — this test would pass over nothing", other)
		}
		for _, w := range foreign {
			if err := v.Unify(ctx.CompileString(f.probe.use(w))).Err(); err == nil {
				t.Errorf("#%s.action accepts %q, which belongs to the _%s vocabulary — the two `action` fields were merged into one table, so neither is a closed set any more",
					f.def, w, other)
			}
		}
	}
	// and the two tables really are two sets, neither empty
	a := append([]string(nil), lifted.Words["action"]...)
	b := append([]string(nil), lifted.Words["consoleAction"]...)
	sort.Strings(a)
	sort.Strings(b)
	if fmt.Sprint(a) == fmt.Sprint(b) {
		t.Errorf("_action and _consoleAction carry the SAME word list %v — the two sets have become one", a)
	}
	if len(a) == 0 || len(b) == 0 {
		t.Errorf("a vocabulary table is empty: _action=%v _consoleAction=%v", a, b)
	}
}

// TestEnumsFollowTheirTables is the only way a runtime test can see the S3 requirement itself
// — that the enum DERIVES from the table rather than restating it. Equality cannot see it: a
// field written out by hand accepts exactly the table's words. So this test MUTATES the table:
// a top-level `_x` struct is open, so a second declaration of the same label adds a row, the
// derived list's comprehension recomputes, and the field must then accept the injected word.
func TestEnumsFollowTheirTables(t *testing.T) {
	const injected = "probe-extra-word"
	for _, p := range vocabProbes {
		src := servedBody(t) + vocabHarness + fmt.Sprintf("\n_%s: {%q: {}}\n", p.name, injected)
		v := compile(t, src)
		ctx := v.Context()
		if err := v.Unify(ctx.CompileString(p.use(injected))).Err(); err != nil {
			t.Errorf("the %q table gained a row but the enum an author meets refuses it: %v — the field does not derive from _%sWord",
				p.name, err, p.name)
		}
	}
}

// TestVocabularyTablesKeepMoreThanOneRow is the coarse floor against a table that lost its
// tail: an S3 table that shrinks is self-consistent, so no cross-check in this file can see it.
func TestVocabularyTablesKeepMoreThanOneRow(t *testing.T) {
	lifted := liftVocabulary(t, servedSchema(t))
	for _, p := range vocabProbes {
		if got := len(lifted.Words[p.name]); got < 2 {
			t.Errorf("the _%s table's derived list reads %d word(s) %v — a table that lost its tail, or a reader that returned the default",
				p.name, got, lifted.Words[p.name])
		}
	}
}
