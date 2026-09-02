package spice

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"github.com/opencharly/plugin-spice/candy/plugin-spice/params"
)

// record_test.go covers the deterministic core of `spice: record`: the change
// detection, the MJPEG frame encoding/counting, and the session registry naming.
// The venue-driving path (live SPICE wire capture) is exercised by the R10 bed.

// solidRGBA builds an RGBA image filled with one color (for change/encode tests).
func solidRGBA(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func TestRecordName(t *testing.T) {
	if got := recordName(&params.SpiceInput{}); got != "default" {
		t.Errorf("recordName(empty) = %q, want default", got)
	}
	if got := recordName(&params.SpiceInput{RecordName: "vm-walk"}); got != "vm-walk" {
		t.Errorf("recordName(vm-walk) = %q, want vm-walk", got)
	}
}

func TestRecordFps(t *testing.T) {
	if got := recordFps(&params.SpiceInput{}); got != 200*1000*1000 {
		t.Errorf("recordFps(unset) = %v, want 200ms", got)
	}
	if got := recordFps(&params.SpiceInput{Fps: 10}); got != 100*1000*1000 {
		t.Errorf("recordFps(10) = %v, want 100ms", got)
	}
	// 200 fps caps at 10ms interval (100 fps)
	if got := recordFps(&params.SpiceInput{Fps: 200}); got != 10*1000*1000 {
		t.Errorf("recordFps(200) = %v, want 10ms cap", got)
	}
}

func TestAppendLockedMJPEG(t *testing.T) {
	rs := &recordSession{}
	img := solidRGBA(32, 32, color.RGBA{R: 90, G: 140, B: 200, A: 255})
	rs.appendLocked(img)
	rs.appendLocked(solidRGBA(32, 32, color.RGBA{R: 90, G: 140, B: 201, A: 255}))
	if rs.count != 2 {
		t.Fatalf("count = %d, want 2", rs.count)
	}
	data := rs.buf.Bytes()
	if len(data) == 0 {
		t.Fatal("empty MJPEG stream")
	}
	// every JPEG frame in the stream must decode on its own (SOI..EOI slices)
	frames := splitMJpeg(data)
	if len(frames) != 2 {
		t.Fatalf("splitMJpeg = %d frames, want 2", len(frames))
	}
	for i, fr := range frames {
		if _, err := jpeg.Decode(bytes.NewReader(fr)); err != nil {
			t.Errorf("frame %d does not decode: %v", i, err)
		}
	}
}

// TestTickAppendsIdenticalFrames is the B12 coverage for the multi-frame
// semantics: two consecutive ticks with the SAME framebuffer must BOTH append a
// frame (a camera-accurate video of a still scene). It FAILS on the pre-change
// code (the change-only capture skipped the identical second frame).
func TestTickAppendsIdenticalFrames(t *testing.T) {
	fake := &fakeSpiceSession{img: solidRGBA(32, 32, color.RGBA{R: 10, G: 200, B: 30, A: 255})}
	rs := &recordSession{s: fake}
	rs.tick()
	rs.tick()
	if rs.count != 2 {
		t.Fatalf("two identical ticks = %d frames, want 2 (every poll is one frame)", rs.count)
	}
	if frames := splitMJpeg(rs.buf.Bytes()); len(frames) != 2 {
		t.Fatalf("splitMJpeg = %d frames, want 2", len(frames))
	}
}

// fakeSpiceSession satisfies the small part of SpiceSession the poller uses.
type fakeSpiceSession struct {
	img  *image.RGBA
	sess SpiceSession
}

func (f *fakeSpiceSession) Display() image.Image { return f.img }

// splitMJpeg splits a concatenated-JPEG stream at SOI/EOI boundaries.
func splitMJpeg(data []byte) [][]byte {
	var frames [][]byte
	i := 0
	for i < len(data)-1 {
		if data[i] == 0xFF && data[i+1] == 0xD8 { // SOI
			j := i + 2
			for j < len(data)-1 {
				if data[j] == 0xFF && data[j+1] == 0xD9 { // EOI
					frames = append(frames, data[i:j+2])
					i = j + 2
					break
				}
				j++
			}
			if j >= len(data)-1 {
				break
			}
			continue
		}
		i++
	}
	return frames
}
