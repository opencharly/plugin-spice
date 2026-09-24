package spice

import (
	"context"
	"os"
	"testing"

	"github.com/opencharly/plugin-spice/candy/plugin-spice/params"
)

// TestLiveSpiceWizard drives the REAL spiceTransport (Capture/PressKey/Type) over
// a REAL SPICE socket and runs the shared wizard engine end to end — the live
// proof the unit tests cannot give. It is GATED on SPICE_SOCK pointing at a
// running VM's spice.sock (a real hardware boundary the repo cannot fabricate):
// unset → SKIP, reported, never a silent pass.
//
//	SPICE_SOCK=/path/to/spice.sock SPICE_TEXT="Press Return to Start Install" \
//	  go test ./... -run LiveSpiceWizard -v
//
// SPICE_TEXT defaults to the Omarchy installer greeter; each step's screenshot is
// OCR'd live and the transport's key is sent on the real wire.
func TestLiveSpiceWizard(t *testing.T) {
	sock := os.Getenv("SPICE_SOCK")
	if sock == "" {
		t.Skip("set SPICE_SOCK=<vm spice.sock> to run the live SPICE wizard")
	}
	first := os.Getenv("SPICE_TEXT")
	if first == "" {
		first = "Press Return to Start Install"
	}
	s, err := DialSpiceUnix(sock, "")
	if err != nil {
		t.Fatalf("dial spice %s: %v", sock, err)
	}

	tr := spiceTransport{s: s}
	png, err := tr.Capture(context.Background())
	if err != nil {
		t.Fatalf("Capture over SPICE: %v", err)
	}
	t.Logf("captured %d PNG bytes over SPICE", len(png))

	// The two opening wizard steps: accept the greeter, accept the keyboard.
	// The step's real wait_for anchor is the screen-unique greeter text.
	in := &params.SpiceInput{Steps: []params.SpiceConsoleStep{
		{WaitFor: first, Action: "key", KeyName: "Return", TimeoutSec: 120},
		{WaitFor: "Select keyboard layout", Action: "key", KeyName: "Return", TimeoutSec: 120},
	}}
	out, err := runWizardWith(context.Background(), nil, tr, nil, in)
	if err != nil {
		t.Fatalf("wizard over real SPICE: %v", err)
	}
	t.Logf("wizard output:\n%s", out)
}
