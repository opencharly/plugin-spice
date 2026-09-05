package spice

// session_method.go — the `spice: session` method (Cutover A, A-task-2b): the
// HOST-side detached recorder driven through the runner's GENERIC background-session
// service (plugin-check's compiled-in verb:session seam, candy/plugin-check/
// session_seam.go). The provider submits a GENERIC host-step request — its recorder
// command + session identity, NEVER a systemd unit — and the runner dispatches it;
// the provider never learns the transport, spawns no process, owns no pidfile.
//
// Wire shape (the runner's contract):
//
//	{ "op": "spawn"|"stop"|"status"|"sweep",
//	  "session_id": "<venue-scoped id>",
//	  "command":    ["<recorder argv>"],          // spawn only
//	  "dir":        "<recorder working dir>",     // spawn only
//	  "env":        {"K": "V", ...},              // spawn only
//	  "log_dir":    "<.check/<bed>/<calver>>" }   // the run dir the capture/ state lives in
//
// Submitted over the EXISTING InvokeProvider reverse leg — ClassVerb "session",
// OpExecute — the SAME E3b channel the record method's graphics resolution
// (cc.ResolveGraphicsEndpoint) rides, so the provider code is placement-invisible
// (compiled-in vs out-of-process). The recorder is THIS plugin's OWN binary in
// recorder mode (CHARLY_SPICE_RECORDER=1; cmd/serve) holding the SPICE wire — the
// provider itself never dials for a session.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/opencharly/plugin-spice/candy/plugin-spice/params"
	"github.com/opencharly/sdk"
	"github.com/opencharly/spec/ops"
)

// sessionRequest is the wire request the runner's session service decodes
// (plugin-check/candy/plugin-check/session_seam.go, the sessionSeamRequest shape).
type sessionRequest struct {
	Op        string            `json:"op"`
	SessionID string            `json:"session_id"`
	Command   []string          `json:"command,omitempty"`
	Dir       string            `json:"dir,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	LogDir    string            `json:"log_dir,omitempty"`
}

// sessionStatusReply is the runner's liveness answer (the status op, sessionSeamReply).
type sessionStatusReply struct {
	Alive bool `json:"alive,omitempty"`
}

// runSession dispatches the session method against the runner service. The endpoint
// is the PROVIDER-resolved one (the Invoke already ran ResolveGraphicsEndpoint); the
// recorder re-dials it detached. venueDefault is the CheckEnv snapshot's venue id,
// used when the authored input carries none (evidence-row provenance).
func runSession(ctx context.Context, brokerID uint32, ep *spiceEndpoint, in *params.SpiceInput, venueDefault string) (string, error) {
	// session identity: the runner injects session_id for instruments; a PLAN-STEP session
	// (authoring `spice: {method: session, action: start, record_name: x}` directly in a plan)
	// falls back to record_name, then "default" - the same fallback the record session uses.
	if in.SessionId == "" {
		in.SessionId = in.RecordName
	}
	if in.SessionId == "" {
		in.SessionId = "default"
	}
	// state_dir is runner-injected for instruments; a plan-step session derives its own
	// under the host temp dir (the row.json + frames live there; instrument sessions
	// always receive the run-dir state dir).
	if in.StateDir == "" {
		in.StateDir = filepath.Join(os.TempDir(), "charly-session", in.SessionId)
	}
	switch in.Action {
	case "start":
		return sessionStart(ctx, brokerID, ep, in, venueDefault)
	case "stop":
		return sessionStop(ctx, brokerID, in)
	case "status":
		return sessionStatus(ctx, brokerID, in)
	}
	return "", fmt.Errorf("session requires action: start|stop|status")
}

// buildSessionSpawn builds the spawn request the runner turns into the detached
// recorder process: THIS plugin's binary in recorder mode with the resolved endpoint
// + session identity threaded via the CHARLY_SPICE_* env (recorder.go). Extracted
// from sessionStart so the wire request is unit-testable without the reverse leg.
func buildSessionSpawn(in *params.SpiceInput, ep *spiceEndpoint, exe, venue, logDir string) sessionRequest {
	fps := in.Fps
	if fps <= 0 {
		fps = 5
	}
	epJSON, _ := json.Marshal(ep)
	env := map[string]string{
		EnvRecorder:  "1",
		EnvEndpoint:  string(epJSON),
		EnvFps:       strconv.Itoa(fps),
		EnvStateDir:  in.StateDir,
		EnvSessionID: in.SessionId,
	}
	if venue != "" {
		env[EnvVenue] = venue
	}
	if in.Phase != "" {
		env[EnvPhase] = in.Phase
	}
	return sessionRequest{
		Op:        "spawn",
		SessionID: in.SessionId,
		Command:   []string{exe, "__dummy-arg"},
		LogDir:    logDir,
		Env:       env,
	}
}

// sessionStart resolves nothing more (the endpoint came from Invoke) — it submits the
// recorder spawn to the runner service and reports the session as started. state_dir
// is REQUIRED: the recorder (and the runner's handle) land the capture + evidence
// there, and the A-task-4 instrument lifecycle reads row.json back from it.
func sessionStart(ctx context.Context, brokerID uint32, ep *spiceEndpoint, in *params.SpiceInput, venueDefault string) (string, error) {
	if in.StateDir == "" {
		return "", fmt.Errorf("session start: state_dir required")
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("session start: resolve own binary: %w", err)
	}
	venue := in.Venue
	if venue == "" {
		venue = venueDefault
	}
	// log_dir: the runner's session handle + the recorder state live in the check run
	// dir; the current CheckEnv snapshot (EnvJson) carries no run-dir field, so this
	// stays "" (the runner's cwd-relative default) until the A-task-4 instrument
	// lifecycle threads one over the wire.
	logDir := ""
	req := buildSessionSpawn(in, ep, exe, venue, logDir)
	if err := submitSession(ctx, brokerID, req); err != nil {
		return "", fmt.Errorf("session start: %w", err)
	}
	return fmt.Sprintf("session %s started", in.SessionId), nil
}

// sessionStop signals the runner to finalize the session (SIGTERM to the detached
// recorder), then verifies the recorder's finalize actually landed: the evidence
// row.json must exist in the state dir. Returns a one-line summary of the row.
func sessionStop(ctx context.Context, brokerID uint32, in *params.SpiceInput) (string, error) {
	if in.StateDir == "" {
		return "", fmt.Errorf("session stop: state_dir required to verify the evidence row")
	}
	req := sessionRequest{Op: "stop", SessionID: in.SessionId}
	if err := submitSession(ctx, brokerID, req); err != nil {
		return "", fmt.Errorf("session stop: %w", err)
	}
	rowPath := filepath.Join(in.StateDir, evidenceFile)
	raw, err := os.ReadFile(rowPath)
	if err != nil {
		return "", fmt.Errorf("session stop: %q: evidence row missing: %w", rowPath, err)
	}
	var row evidenceRow
	if err := json.Unmarshal(raw, &row); err != nil {
		return "", fmt.Errorf("session stop: decode evidence row %q: %w", rowPath, err)
	}
	return fmt.Sprintf("session %s stopped · evidence: instrument=%s origin=%s artifact=%s (%s)",
		in.SessionId, row.Instrument, row.Origin, rowArtifact(row), filepath.Join(in.StateDir, finalMarker)), nil
}

// rowArtifact renders the row's first artifact as "path[kind]" (a stop path always
// carries the frames.mjpeg artifact the recorder wrote).
func rowArtifact(row evidenceRow) string {
	if len(row.Artifact) == 0 {
		return "<none>"
	}
	a := row.Artifact[0]
	return fmt.Sprintf("%s[%s]", a.Path, a.Kind)
}

// sessionStatus asks the runner for the session handle's liveness and reports it.
func sessionStatus(ctx context.Context, brokerID uint32, in *params.SpiceInput) (string, error) {
	req := sessionRequest{Op: "status", SessionID: in.SessionId}
	raw, err := submitSessionReply(ctx, brokerID, req)
	if err != nil {
		return "", fmt.Errorf("session status: %w", err)
	}
	var rep sessionStatusReply
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &rep)
	}
	return fmt.Sprintf("session %s alive=%v", in.SessionId, rep.Alive), nil
}

// submitSession sends a session-service request; success is the empty reply.
func submitSession(ctx context.Context, brokerID uint32, req sessionRequest) error {
	_, err := submitSessionReply(ctx, brokerID, req)
	return err
}

// submitSessionReply sends the request over the EXISTING InvokeProvider reverse leg
// (ClassVerb "session", OpExecute). The runner (plugin-check, compiled-in) dispatches
// it; failures ride the returned error; a status op carries the result JSON back.
func submitSessionReply(ctx context.Context, brokerID uint32, req sessionRequest) ([]byte, error) {
	ex, err := sdk.ExecutorForInvoke(ctx, brokerID)
	if err != nil {
		return nil, fmt.Errorf("session: reverse leg: %w", err)
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("session: encode request: %w", err)
	}
	return ex.InvokeProvider(ctx, "verb", "session", ops.OpExecute, reqJSON, nil, ops.InvokeProviderOpts{})
}
