package spice

import (
	"context"
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/opencharly/plugin-spice/candy/plugin-spice/params"
	"github.com/opencharly/spec/spec"
)

// session_test.go covers the `spice: session` method (Cutover A, A-task-2b): the
// recorder loop (dial-loop capture → frames.mjpeg, finalize → FINAL + row.json), the
// spawn request the provider hands to the runner's generic background-session
// service, and the runSession validation gates. The reverse-leg submission itself
// (InvokeProvider ClassVerb session) is exercised by the R10 bed — the venue-driving
// path, like record's.

// TestCaptureSessionWritesFramesAndFinalizes is the recorder-loop unit test with
// the display fake (record_test.go pattern): polling frames land in
// <state_dir>/frames.mjpeg, and closing the done channel (the SIGTERM trap's
// analog) finalizes with the FINAL marker + the evidence row.json.
func TestCaptureSessionWritesFramesAndFinalizes(t *testing.T) {
	stateDir := t.TempDir()
	fake := &fakeSpiceSession{img: solidRGBA(32, 32, color.RGBA{R: 90, G: 140, B: 200, A: 255})}
	done := make(chan struct{})
	cfg := RecorderConfig{
		Fps:       100, // 10ms poll interval
		StateDir:  stateDir,
		SessionID: "bed.member.cap",
		Venue:     "check-some-vm",
		Phase:     "live",
	}
	type res struct {
		count int
		err   error
	}
	rc := make(chan res, 1)
	go func() {
		c, err := captureSession(fake, cfg, done)
		rc <- res{c, err}
	}()
	// Let a few poll intervals elapse, then close done (the SIGTERM analog).
	time.Sleep(45 * time.Millisecond)
	close(done)
	got := <-rc
	if got.err != nil {
		t.Fatalf("captureSession: %v", got.err)
	}
	if got.count < 2 {
		t.Fatalf("captured %d frames, want >= 2 (dial-loop -> frames)", got.count)
	}

	mjpeg, err := os.ReadFile(filepath.Join(stateDir, framesFile))
	if err != nil {
		t.Fatalf("frames.mjpeg: %v", err)
	}
	if frames := splitMJpeg(mjpeg); len(frames) != got.count {
		t.Errorf("splitMJpeg frames = %d, want %d", len(frames), got.count)
	}

	marker, err := os.ReadFile(filepath.Join(stateDir, finalMarker))
	if err != nil {
		t.Fatalf("FINAL marker missing after stop: %v", err)
	}
	if want := "final frames=" + itoa(got.count); string(marker) != want+"\n" {
		t.Errorf("FINAL content = %q, want %q", marker, want)
	}

	raw, err := os.ReadFile(filepath.Join(stateDir, evidenceFile))
	if err != nil {
		t.Fatalf("row.json missing after stop: %v", err)
	}
	var row evidenceRow
	if err := json.Unmarshal(raw, &row); err != nil {
		t.Fatalf("decode row.json: %v", err)
	}
	tw := evidenceRow{
		Instrument: "bed.member.cap",
		Origin:     "session",
		Verb:       "spice",
		Venue:      "check-some-vm",
		Phase:      "live",
		Artifact:   []evidenceArtifact{{Path: filepath.Join(stateDir, framesFile), Kind: "mjpeg"}},
	}
	if row.Instrument != tw.Instrument || row.Origin != tw.Origin || row.Verb != tw.Verb ||
		row.Venue != tw.Venue || row.Phase != tw.Phase || len(row.Artifact) != 1 ||
		row.Artifact[0].Path != tw.Artifact[0].Path || row.Artifact[0].Kind != tw.Artifact[0].Kind {
		t.Errorf("row.json = %+v, want %+v", row, tw)
	}
}

// TestFinalizeSessionMissingStateDir guards the recorder's honest failure when the
// spawn env left no state dir.
func TestCaptureSessionEmptyStateDir(t *testing.T) {
	if _, err := captureSession(&fakeSpiceSession{}, RecorderConfig{}, make(chan struct{})); err == nil {
		t.Fatal("captureSession with empty state dir: want error")
	}
}

// TestBuildSessionSpawn asserts the exact spawn request the provider submits to the
// runner's generic session service: this plugin's binary in recorder mode + the
// endpoint/identity env, with the venue default from the CheckEnv snapshot applied.
func TestBuildSessionSpawn(t *testing.T) {
	ep := &spiceEndpoint{Address: "127.0.0.1:5950", Password: "ticket"}
	in := &params.SpiceInput{SessionId: "bed.member.cap", StateDir: "/var/run/checks/bed/x", Fps: 5, Phase: "live"}
	req := buildSessionSpawn(in, ep, "/usr/lib/charly/plugin-spice", "check-some-vm", "")
	if req.Op != "spawn" {
		t.Errorf("op = %q, want spawn", req.Op)
	}
	if req.SessionID != "bed.member.cap" {
		t.Errorf("session_id = %q", req.SessionID)
	}
	if len(req.Command) != 2 || req.Command[0] != "/usr/lib/charly/plugin-spice" || req.Command[1] != "__dummy-arg" {
		t.Errorf("command = %v, want [<self> __dummy-arg]", req.Command)
	}
	if req.Env[EnvRecorder] != "1" {
		t.Errorf("CHARLY_SPICE_RECORDER = %q, want 1", req.Env[EnvRecorder])
	}
	if req.Env[EnvFps] != "5" {
		t.Errorf("CHARLY_SPICE_FPS = %q, want 5", req.Env[EnvFps])
	}
	if req.Env[EnvStateDir] != "/var/run/checks/bed/x" {
		t.Errorf("CHARLY_SPICE_STATE_DIR = %q", req.Env[EnvStateDir])
	}
	if req.Env[EnvSessionID] != "bed.member.cap" {
		t.Errorf("CHARLY_SPICE_SESSION_ID = %q", req.Env[EnvSessionID])
	}
	if req.Env[EnvVenue] != "check-some-vm" {
		t.Errorf("CHARLY_SPICE_VENUE = %q, want check-some-vm (CheckEnv default)", req.Env[EnvVenue])
	}
	if req.Env[EnvPhase] != "live" {
		t.Errorf("CHARLY_SPICE_PHASE = %q, want live", req.Env[EnvPhase])
	}
	// the endpoint rides the env as the spiceEndpoint JSON (address + password).
	var gotEP spiceEndpoint
	if err := json.Unmarshal([]byte(req.Env[EnvEndpoint]), &gotEP); err != nil {
		t.Fatalf("CHARLY_SPICE_ENDPOINT not JSON: %v", err)
	}
	if gotEP.Address != "127.0.0.1:5950" || gotEP.Password != "ticket" {
		t.Errorf("endpoint JSON = %+v", gotEP)
	}
	// fps defaulting: zero fps in the input spawns the 5 fps default.
	def := buildSessionSpawn(&params.SpiceInput{SessionId: "s", StateDir: "/x"}, ep, "/e", "", "")
	if def.Env[EnvFps] != "5" {
		t.Errorf("default fps = %q, want 5", def.Env[EnvFps])
	}
}

// TestRunSessionValidation guards the required-modifier semantics of the session
// method WITHOUT the reverse leg: each gate fails before any submission.
func TestRunSessionValidation(t *testing.T) {
	ctx := t.Context()
	// session_id/state_dir have provider-side fallbacks (plan-step sessions); the
	// gates that remain are action validation + the submission dispatch under a stub cc.
	if _, err := runSession(ctx, stubCC{}, nil, &params.SpiceInput{Action: "bogus"}, ""); err == nil {
		t.Error("bogus action: want error")
	}
	// a well-formed session reaches the submission (the stub cc answers an error -
	// proving the InvokeProvider dispatch path is exercised, not skipped).
	if _, err := runSession(ctx, stubCC{}, nil, &params.SpiceInput{SessionId: "s", Action: "start"}, ""); err == nil {
		t.Error("start with stub cc: want error (stub answers an error)")
	}
}

type stubCC struct{ spec.CheckContext }

func (stubCC) InvokeProvider(ctx context.Context, class, word, op string, paramsJSON, env []byte) ([]byte, error) {
	return nil, fmt.Errorf("stub: submission reached the reverse-leg dispatch (class %s word %s op %s)", class, word, op)
}

// helpers ----------------------------------------------------------------

func itoa(v int) string { return strconv.Itoa(v) }

// TestWaitForEvidenceRow covers the stop path's bounded row wait: the row appears
// (the recorder's SIGTERM trap finalizes) → the bytes are returned; the row never
// appears → the deadline error names the path + timeout. The timeout path is the
// crashed-recorder case (no row, ever) — the stop must fail fast, not hang.
func TestWaitForEvidenceRow(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	rowPath := filepath.Join(dir, evidenceFile)

	// Row-appears path: the row lands after a short delay (the recorder's finalize
	// lagging the stop return) — the wait must return the bytes.
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = os.WriteFile(rowPath, []byte(`{"instrument":"s"}`), 0o644)
	}()
	raw, err := waitForEvidenceRow(ctx, rowPath, 2*time.Second)
	if err != nil {
		t.Fatalf("row-appears path: %v", err)
	}
	if string(raw) != `{"instrument":"s"}` {
		t.Fatalf("row-appears path: got %q", raw)
	}

	// Timeout path: no row ever lands — the wait must fail with the path + timeout
	// named (the crashed-recorder case).
	missing := filepath.Join(dir, "missing", evidenceFile)
	_, err = waitForEvidenceRow(ctx, missing, 150*time.Millisecond)
	if err == nil {
		t.Fatal("timeout path: want error")
	}
	if !strings.Contains(err.Error(), "evidence row missing after") {
		t.Fatalf("timeout path: error %q does not name the deadline", err)
	}

	// Cancelled-ctx path: a cancelled context aborts the wait promptly.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = waitForEvidenceRow(cctx, missing, 5*time.Second)
	if err == nil {
		t.Fatal("cancelled-ctx path: want error")
	}
}

