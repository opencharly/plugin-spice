package spice

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"sync"
	"time"

	"github.com/opencharly/plugin-spice/candy/plugin-spice/params"
)

// record.go implements the `spice: record` method — host-side MJPEG video capture
// of a VM's SPICE display channel. It is the display-channel analogue of
// plugin-record's terminal/desktop sessions: record: start polls the SPICE
// session's stashed framebuffer (updated by the driver at every
// SPICE_MSG_DISPLAY_MARK, session.go) at the configured fps, encoding each
// CHANGED frame as a JPEG into a single MJPEG byte stream; record: stop flushes
// the stream to the host artifact path where the shared artifact validators run.
//
// A record session is package-global (keyed by record_name) and survives across
// check steps: the plugin process serves the whole check-live run, and holding
// the *SpiceSession keeps the SPICE connection + its framebuffer updates alive
// between invokes (the session has no Close — the plugin never tears the wire
// down; the VM's death ends it). Multiple concurrent sessions are supported.
//
// Design notes:
//   - PURE-GO: frames are encoded with the stdlib image/jpeg encoder (the vendored
//     spice lib already imports it), so the plugin stays cgo-free. WebM/MP4
//     containerization is a host-side ffmpeg post-pass (layer-ffmpeg), not plugin
//     scope (R3: no decoder/container duplication in the plugin).
//   - CHANGE-ONLY capture: identical framebuffers are skipped, so a dead/static
//     display yields an empty (or near-empty) stream and the artifact validators
//     (artifact_min_bytes / artifact_not_uniform) honestly fail. Real captures
//     contain real motion.
//   - LIMITS (documented): the vendored SPICE client cannot decode server video
//     streams (SPICE_MSG_DISPLAY_STREAM_* unhandled) or GLZ/PLT images, so
//     motion-heavy guests may freeze where the server switches to streams; audio
//     is impossible (playback/record channels are stubs, no cgo opus/portaudio).

// recordSessions is the package-global record-session registry (name -> session).
var recordSessions sync.Map

// recordSession captures display frames of one named recording.
type recordSession struct {
	s     *SpiceSession
	fps   time.Duration
	done  chan struct{}
	mu    sync.Mutex
	buf   bytes.Buffer
	count int
	last  *image.RGBA
}

// recordName returns the authored record_name, defaulting to "default".
func recordName(in *params.SpiceInput) string {
	if in.RecordName != "" {
		return in.RecordName
	}
	return "default"
}

// recordFps returns the authored capture interval, defaulting to 5 frames/second.
func recordFps(in *params.SpiceInput) time.Duration {
	if in.Fps > 0 {
		d := time.Second / time.Duration(in.Fps)
		if d < 10*time.Millisecond {
			d = 10 * time.Millisecond // cap at 100 fps
		}
		return d
	}
	return 200 * time.Millisecond // 5 fps
}

// runRecord dispatches record start/stop. start requires action=start and keeps
// the session alive in the registry; stop requires action=stop + artifact and
// returns the MJPEG byte-count confirmation for the provider's matchers.
func runRecord(s *SpiceSession, in *params.SpiceInput) (string, error) {
	name := recordName(in)
	switch in.Action {
	case "start":
		if _, exists := recordSessions.Load(name); exists {
			return "", fmt.Errorf("recording %q already active; stop it first", name)
		}
		if err := s.WaitForDisplay(5 * time.Second); err != nil {
			return "", fmt.Errorf("record %q: no display framebuffer yet: %v", name, err)
		}
		rs := &recordSession{
			s:    s,
			fps:  recordFps(in),
			done: make(chan struct{}),
		}
		recordSessions.Store(name, rs)
		go rs.run()
		return fmt.Sprintf("Recording started (name: %s, fps: %d)", name, rs.pollerFps()), nil
	case "stop":
		v, exists := recordSessions.Load(name)
		if !exists {
			return "", fmt.Errorf("no active recording %q", name)
		}
		rs := v.(*recordSession)
		close(rs.done)
		recordSessions.Delete(name)
		rs.mu.Lock()
		data := rs.buf.Bytes()
		count := rs.count
		rs.mu.Unlock()
		if err := writeArtifact(in.Artifact, data); err != nil {
			return "", err
		}
		return fmt.Sprintf("Recording stopped (name: %s, frames: %d, bytes: %d)", name, count, len(data)), nil
	}
	return "", fmt.Errorf("record requires action: start|stop")
}

// pollerFps returns the poll interval in a friendly form (frames/second).
func (rs *recordSession) pollerFps() int {
	if rs.fps <= 0 {
		return 5
	}
	return int(time.Second / rs.fps)
}

// run is the frame-capture loop: poll the session display at the configured fps,
// skip unchanged surfaces, encode changed ones as JPEG into the MJPEG buffer.
func (rs *recordSession) run() {
	tick := time.NewTicker(rs.fps)
	defer tick.Stop()
	for {
		select {
		case <-rs.done:
			return
		case <-tick.C:
			img := rs.s.Display()
			if img == nil {
				continue
			}
			rs.mu.Lock()
			changed := framesChanged(rs.last, img)
			if !changed {
				rs.mu.Unlock()
				continue
			}
			rs.appendLocked(img)
			rs.mu.Unlock()
		}
	}
}

// appendLocked encodes one frame into the MJPEG stream (caller holds rs.mu).
func (rs *recordSession) appendLocked(img image.Image) {
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 80}); err != nil {
		return
	}
	rs.last = normalizeRGBA(img)
	rs.buf.Write(b.Bytes())
	rs.count++
}

// framesChanged reports whether two framebuffers differ (a sparse comparison:
// identical frames are skipped; only the 64-pixel grid is compared).
func framesChanged(a *image.RGBA, b image.Image) bool {
	if a == nil {
		return true // first frame always captured
	}
	bb := normalizeRGBA(b)
	if bb == nil {
		return false
	}
	if a.Bounds() != bb.Bounds() {
		return true
	}
	w, h := bb.Bounds().Dx(), bb.Bounds().Dy()
	for y := 0; y < h; y += 64 {
		for x := 0; x < w; x += 64 {
			i := bb.PixOffset(x, y)
			if a.Pix[i] != bb.Pix[i] || a.Pix[i+1] != bb.Pix[i+1] || a.Pix[i+2] != bb.Pix[i+2] {
				return true
			}
		}
	}
	return false
}

// normalizeRGBA converts any image to *image.RGBA for stable comparison.
func normalizeRGBA(img image.Image) *image.RGBA {
	switch v := img.(type) {
	case *image.RGBA:
		return v
	default:
		rgba := image.NewRGBA(img.Bounds())
		for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
			for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
				rgba.Set(x, y, img.At(x, y))
			}
		}
		return rgba
	}
}

// writeArtifact writes the MJPEG stream to the host artifact path.
func writeArtifact(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}
