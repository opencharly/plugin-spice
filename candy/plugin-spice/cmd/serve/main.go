// Command serve is the OUT-OF-PROCESS entrypoint for the spice verb plugin: a thin
// shim serving the importable provider over go-plugin gRPC via sdk.Serve. The SAME
// NewProvider()/NewMeta() compile INTO charly in-process when listed in
// compiled_plugins; this binary is host-built + connected only when they are NOT —
// placement is invisible above the registry.
//
// HIDDEN RECORDER MODE (Cutover A, A-task-2b): with CHARLY_SPICE_RECORDER=1 the SAME
// binary skips serving and becomes the DETACHED host-side session recorder — the
// runner's generic background-session service spawns it for a `spice: session`
// start. It dials the SPICE endpoint from env, polls the display at fps into
// $CHARLY_SPICE_STATE_DIR/frames.mjpeg, and on SIGTERM/SIGINT finalizes (FINAL
// marker + evidence row.json) before exiting 0: the runner's stop is complete only
// when row.json is on disk.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	spice "github.com/opencharly/plugin-spice/candy/plugin-spice"
	"github.com/opencharly/sdk"
)

func main() {
	if os.Getenv(spice.EnvRecorder) == "1" {
		os.Exit(recorderMain())
	}
	sdk.Serve(spice.NewProvider(), spice.NewMeta())
}

// recorderMain is the detached recorder process entrypoint (see the package doc). It
// returns the process exit code.
func recorderMain() int {
	endpointJSON := os.Getenv(spice.EnvEndpoint)
	stateDir := os.Getenv(spice.EnvStateDir)
	sessionID := os.Getenv(spice.EnvSessionID)
	if endpointJSON == "" || stateDir == "" || sessionID == "" {
		fmt.Fprintf(os.Stderr, "charly-spice recorder: missing env (endpoint=%q state_dir=%q session_id=%q)\n", endpointJSON, stateDir, sessionID)
		return 2
	}
	ep, err := spice.ParseEndpoint([]byte(endpointJSON))
	if err != nil {
		fmt.Fprintf(os.Stderr, "charly-spice recorder: %v\n", err)
		return 2
	}
	fps, _ := strconv.Atoi(os.Getenv(spice.EnvFps))
	cfg := spice.RecorderConfig{
		Endpoint:  ep,
		Fps:       fps,
		StateDir:  stateDir,
		SessionID: sessionID,
		Venue:     os.Getenv(spice.EnvVenue),
		Phase:     os.Getenv(spice.EnvPhase),
	}

	done := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sig
		close(done) // deterministic finalize: FINAL marker + row.json
	}()

	count, err := spice.RunSessionRecorder(cfg, done)
	if err != nil {
		fmt.Fprintf(os.Stderr, "charly-spice recorder: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "charly-spice recorder: finalized session %s frames=%d state_dir=%s\n", sessionID, count, stateDir)
	return 0
}
