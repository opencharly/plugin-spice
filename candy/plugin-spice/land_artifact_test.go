package spice

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

// land_artifact_test.go covers the produced-artifact leg the provider routes
// through sdk.LandArtifact (provider.go landArtifactLeg): the host-side leg (nil
// executor + blank venue path) must validate the file the plugin already wrote IN
// PLACE - no pull, no write - and map a validator failure to the exact
// "spice: <method>: <err>" wire reply the pre-migration VerbVerdict artifact block
// produced. The tests would not compile without the LandArtifact routing (B12).

// writePNG writes a two-tone PNG to path (the not_uniform-passing shape).
func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x < w/2 && y < h/2 {
				img.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
			} else {
				img.SetRGBA(x, y, color.RGBA{A: 255})
			}
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		t.Fatalf("encode %s: %v", path, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
}

// replyResult decodes the wire reply JSON into {status,message}.
type replyResult struct {
	Status  string
	Message string
}

func decodeReply(t *testing.T, reply *pb.InvokeReply) replyResult {
	t.Helper()
	if reply == nil {
		t.Fatal("nil reply")
	}
	var res replyResult
	if err := json.Unmarshal(reply.ResultJson, &res); err != nil {
		t.Fatalf("decode reply %q: %v", reply.ResultJson, err)
	}
	return res
}

func artifactOp(path string, minBytes int) *spec.Op {
	return &spec.Op{PluginInput: map[string]any{
		"artifact":           path,
		"artifact_min_bytes": minBytes,
	}}
}

// TestLandArtifactLegFailureMessageShape is the B12 guard for the observable the
// migration promises to preserve: an artifact-validator failure must surface as the
// same wire reply shape the VerbVerdict artifact leg produced - status fail with a
// message naming the verb, the method, and the artifact reality.
func TestLandArtifactLegFailureMessageShape(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	small := filepath.Join(dir, "tiny.png")
	writePNG(t, small, 4, 4) // real PNG, too small for min_bytes

	// screenshot: the two artifact methods share the leg - assert both shapes.
	for _, m := range []string{"screenshot", "cursor"} {
		reply, err := landArtifactLeg(ctx, m, small, artifactOp(small, 1<<20))
		if err != nil {
			t.Fatalf("%s failure leg: unexpected error %v", m, err)
		}
		res := decodeReply(t, reply)
		if res.Status != "fail" {
			t.Errorf("%s failure: status = %q, want fail", m, res.Status)
		}
		want := "spice: " + m + ": "
		if !strings.HasPrefix(res.Message, want) {
			t.Errorf("%s failure: message = %q, want prefix %q", m, res.Message, want)
		}
		if !strings.Contains(res.Message, "tiny.png") {
			t.Errorf("%s failure: message = %q, want the artifact path named", m, res.Message)
		}
		if !strings.Contains(res.Message, "min_bytes") {
			t.Errorf("%s failure: message = %q, want the failing validator named", m, res.Message)
		}
	}

	// record-stop is the third artifact-producing method - same leg, same shape.
	reply, err := landArtifactLeg(ctx, "record", small, artifactOp(small, 1<<20))
	if err != nil {
		t.Fatalf("record failure leg: unexpected error %v", err)
	}
	res := decodeReply(t, reply)
	if res.Status != "fail" || !strings.HasPrefix(res.Message, "spice: record: ") {
		t.Errorf("record failure message = %q, want status fail + prefix spice: record:", res.Message)
	}
}

// TestLandArtifactLegPass validates the happy path: a real PNG satisfying the
// declared validators lands without a fail reply.
func TestLandArtifactLegPass(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	shot := filepath.Join(dir, "shot.png")
	writePNG(t, shot, 320, 240)

	reply, err := landArtifactLeg(ctx, "screenshot", shot, artifactOp(shot, 256))
	if err != nil {
		t.Fatalf("pass leg: unexpected error %v", err)
	}
	if reply != nil {
		t.Fatalf("pass leg: want nil reply (no fail), got %q", reply.ResultJson)
	}
}

// TestLandArtifactLegHostSideNoWrite proves the host-side contract: with a nil
// executor the file on disk is validated IN PLACE - validating must not rewrite or
// pull over the artifact the plugin produced (the R3 point of the LandArtifact
// migration vs a venue pull).
func TestLandArtifactLegHostSideNoWrite(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	shot := filepath.Join(dir, "shot.png")
	writePNG(t, shot, 320, 240)
	before, err := os.ReadFile(shot)
	if err != nil {
		t.Fatal(err)
	}

	if reply, err := landArtifactLeg(ctx, "screenshot", shot, artifactOp(shot, 200)); err != nil || reply != nil {
		t.Fatalf("host-side leg should validate in place: reply=%v err=%v", reply, err)
	}
	after, err := os.ReadFile(shot)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("host-side leg rewrote the artifact - nil executor must not write")
	}
}
