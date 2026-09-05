package spice

// recorder.go — the DETACHED host-side session recorder (Cutover A, A-task-2b).
// `spice: session start` hands THIS binary (in recorder mode, env
// CHARLY_SPICE_RECORDER=1) to the runner's generic background-session service
// (plugin-check's compiled-in verb:session seam). The recorder owns the SPICE wire
// for the whole session: it dials the host-pre-resolved endpoint, polls the display
// at the session fps into <state_dir>/frames.mjpeg REUSING the record method's
// capture mechanics (the displaySource interface + the shared JPEG encoder —
// encodeFrame, R3: one encoder), and on SIGTERM/SIGINT finalizes: the deterministic
// FINAL marker + the evidence row.json ("instrument"/"origin"/"verb"/"artifact" —
// the shared #EvidenceRow shape, plan §4 A-task-1). While it runs, the PROVIDER
// spawns no process, knows no transport, and owns no pidfile.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Recorder-mode env contract between provider.go (the spawn env) and cmd/serve's
// recorder mode (the reader). The provider builds these keys in buildSessionSpawn.
const (
	EnvRecorder  = "CHARLY_SPICE_RECORDER"
	EnvEndpoint  = "CHARLY_SPICE_ENDPOINT"
	EnvFps       = "CHARLY_SPICE_FPS"
	EnvStateDir  = "CHARLY_SPICE_STATE_DIR"
	EnvSessionID = "CHARLY_SPICE_SESSION_ID"
	EnvVenue     = "CHARLY_SPICE_VENUE"
	EnvPhase     = "CHARLY_SPICE_PHASE"
)

// framesFile is the MJPEG artifact name inside the session state dir; finalMarker
// is the deterministic end-of-stream marker the stop path greps for.
const (
	framesFile   = "frames.mjpeg"
	finalMarker  = "FINAL"
	evidenceFile = "row.json"
)

// evidenceRow mirrors the shared #EvidenceRow shape (plan §4 A-task-1) — the
// minimal session subset the recorder writes and sessionStop reads back. No
// plugin-specific manifest code: the runner's evidence phase consumes the general
// shape.
type evidenceRow struct {
	Instrument string             `json:"instrument"`
	Origin     string             `json:"origin"`
	Verb       string             `json:"verb"`
	Venue      string             `json:"venue,omitempty"`
	Phase      string             `json:"phase,omitempty"`
	Artifact   []evidenceArtifact `json:"artifact,omitempty"`
}

type evidenceArtifact struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

// NewEndpoint builds a dialable endpoint (the provider-side spiceEndpoint; exported
// so cmd/serve's recorder mode can construct/decode one over the env contract).

// ParseEndpoint decodes the endpoint JSON the provider threads via
// CHARLY_SPICE_ENDPOINT (the spiceEndpoint wire shape).
func ParseEndpoint(raw []byte) (*spiceEndpoint, error) {
	var ep spiceEndpoint
	if err := json.Unmarshal(raw, &ep); err != nil {
		return nil, fmt.Errorf("decode endpoint JSON: %w", err)
	}
	return &ep, nil
}

// RecorderConfig is the detached-session recorder's full runtime contract.
type RecorderConfig struct {
	Endpoint  *spiceEndpoint
	Fps       int    // frames/second; 0 defaults to 5 (mirrors recordFps)
	StateDir  string // the run's state dir: frames.mjpeg + FINAL + row.json land here
	SessionID string // the venue-scoped session id — stamped into the evidence row
	Venue     string // evidence-row provenance
	Phase     string // evidence-row provenance (build|live|update|teardown)
}

// RunSessionRecorder is the detached-mode engine (cmd/serve, recorder mode): dials
// the endpoint, polls the display into frames.mjpeg until done closes, then
// finalizes the FINAL marker + row.json. Returns the captured frame count.
func RunSessionRecorder(cfg RecorderConfig, done <-chan struct{}) (int, error) {
	if cfg.Endpoint == nil {
		return 0, fmt.Errorf("recorder: nil endpoint")
	}
	s, err := dialEndpoint(cfg.Endpoint)
	if err != nil {
		return 0, fmt.Errorf("recorder: dial endpoint: %w", err)
	}
	return captureSession(s, cfg, done)
}

// captureSession is the recorder core, unit-testable with the display fake
// (record_test.go's fakeSpiceSession): polls the display source at the session fps,
// appending every frame as a JPEG into <state_dir>/frames.mjpeg until done closes,
// then finalizes. Returns the frame count.
func captureSession(s displaySource, cfg RecorderConfig, done <-chan struct{}) (int, error) {
	if cfg.StateDir == "" {
		return 0, fmt.Errorf("recorder: empty state dir")
	}
	if err := os.MkdirAll(cfg.StateDir, 0o755); err != nil {
		return 0, fmt.Errorf("recorder: create state dir: %w", err)
	}
	out, err := os.Create(filepath.Join(cfg.StateDir, framesFile))
	if err != nil {
		return 0, fmt.Errorf("recorder: open %s: %w", framesFile, err)
	}
	count := writeFrames(s, captureInterval(cfg.Fps), out, done)
	if err := out.Close(); err != nil {
		return 0, fmt.Errorf("recorder: close %s: %w", framesFile, err)
	}
	if err := finalizeSession(cfg, count); err != nil {
		return 0, err
	}
	return count, nil
}

// captureInterval maps a fps int to a poll interval (default 5 fps; 100 fps cap —
// identical semantics to recordFps).
func captureInterval(fps int) time.Duration {
	if fps <= 0 {
		fps = 5
	}
	d := time.Second / time.Duration(fps)
	if d < 10*time.Millisecond {
		d = 10 * time.Millisecond
	}
	return d
}

// writeFrames polls the display source at interval, appending each frame as a JPEG
// onto w until done closes. Video semantics identical to record.go's tick: every
// poll is one frame of the stream. Returns the frame count.
func writeFrames(s displaySource, interval time.Duration, w io.Writer, done <-chan struct{}) int {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	count := 0
	for {
		select {
		case <-done:
			return count
		case <-tick.C:
			img := s.Display()
			if img == nil {
				continue
			}
			b := encodeFrame(img)
			if len(b) == 0 {
				continue
			}
			w.Write(b)
			count++
		}
	}
}

// finalizeSession writes the deterministic end-of-stream marker + the evidence row
// into the state dir. Called once, on the SIGTERM path — the runner's stop is
// complete only when row.json is on disk.
func finalizeSession(cfg RecorderConfig, count int) error {
	marker := fmt.Sprintf("final frames=%d\n", count)
	if err := os.WriteFile(filepath.Join(cfg.StateDir, finalMarker), []byte(marker), 0o644); err != nil {
		return fmt.Errorf("recorder: write %s: %w", finalMarker, err)
	}
	row := evidenceRow{
		Instrument: cfg.SessionID,
		Origin:     "session",
		Verb:       "spice",
		Venue:      cfg.Venue,
		Phase:      cfg.Phase,
		Artifact: []evidenceArtifact{{
			Path: filepath.Join(cfg.StateDir, framesFile),
			Kind: "mjpeg",
		}},
	}
	b, err := json.MarshalIndent(row, "", "  ")
	if err != nil {
		return fmt.Errorf("recorder: marshal evidence row: %w", err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(filepath.Join(cfg.StateDir, evidenceFile), b, 0o644); err != nil {
		return fmt.Errorf("recorder: write %s: %w", evidenceFile, err)
	}
	return nil
}
