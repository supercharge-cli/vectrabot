package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// screenGrabber is a view-only RFB client of the sandbox's local VNC server.
// It keeps the latest desktop frame so devices can watch the agent work
// without being given control: only JPEG frames leave the host, and no input
// path exists. It connects on the first viewer and disconnects when nobody
// has asked for a frame for a while.
type screenGrabber struct {
	addr     string
	interval time.Duration
	idle     time.Duration

	mu       sync.Mutex
	changed  *sync.Cond
	running  bool
	lastAsk  time.Time
	version  uint64
	frame    *image.RGBA
	jpegVer  uint64
	jpegData []byte
	err      error
}

func newScreenGrabber(addr string) *screenGrabber {
	g := &screenGrabber{addr: addr, interval: 250 * time.Millisecond, idle: 20 * time.Second}
	g.changed = sync.NewCond(&g.mu)
	return g
}

// ensure starts the capture loop if it is not running. Callers hold g.mu.
func (g *screenGrabber) ensure() {
	g.lastAsk = time.Now()
	if g.running {
		return
	}
	g.running = true
	go g.run()
}

func (g *screenGrabber) run() {
	defer func() {
		g.mu.Lock()
		g.running = false
		g.changed.Broadcast()
		g.mu.Unlock()
	}()
	for attempt := 0; attempt < 3; attempt++ {
		err := g.capture()
		g.mu.Lock()
		idle := time.Since(g.lastAsk) > g.idle
		if err != nil && !errors.Is(err, errIdle) {
			g.err = err
		}
		g.mu.Unlock()
		if idle || errors.Is(err, errIdle) {
			return
		}
		time.Sleep(time.Second)
	}
}

var errIdle = errors.New("no viewers")

func (g *screenGrabber) capture() error {
	conn, err := net.DialTimeout("tcp", g.addr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("desktop is not reachable: %w", err)
	}
	defer conn.Close()
	r := bufio.NewReaderSize(conn, 1<<20)
	width, height, err := rfbHandshake(conn, r)
	if err != nil {
		return err
	}
	fb := image.NewRGBA(image.Rect(0, 0, width, height))
	incremental := byte(0)
	for {
		g.mu.Lock()
		idle := time.Since(g.lastAsk) > g.idle
		g.mu.Unlock()
		if idle {
			return errIdle
		}
		w, h := fb.Rect.Dx(), fb.Rect.Dy()
		request := []byte{3, incremental, 0, 0, 0, 0, byte(w >> 8), byte(w), byte(h >> 8), byte(h)}
		if _, err := conn.Write(request); err != nil {
			return err
		}
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		updated, resized, err := readUpdate(r, &fb)
		if err != nil {
			return err
		}
		incremental = 1
		if resized {
			incremental = 0
		}
		if updated {
			snapshot := image.NewRGBA(fb.Rect)
			copy(snapshot.Pix, fb.Pix)
			g.mu.Lock()
			g.frame = snapshot
			g.version++
			g.err = nil
			g.changed.Broadcast()
			g.mu.Unlock()
		}
		time.Sleep(g.interval)
	}
}

func rfbHandshake(conn net.Conn, r *bufio.Reader) (int, int, error) {
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	defer conn.SetDeadline(time.Time{})
	version := make([]byte, 12)
	if _, err := io.ReadFull(r, version); err != nil {
		return 0, 0, err
	}
	if !bytes.HasPrefix(version, []byte("RFB 003.")) {
		return 0, 0, errors.New("not a VNC server")
	}
	if _, err := conn.Write([]byte("RFB 003.008\n")); err != nil {
		return 0, 0, err
	}
	count, err := r.ReadByte()
	if err != nil {
		return 0, 0, err
	}
	if count == 0 {
		return 0, 0, errors.New("VNC server refused the connection")
	}
	types := make([]byte, count)
	if _, err := io.ReadFull(r, types); err != nil {
		return 0, 0, err
	}
	if !bytes.Contains(types, []byte{1}) {
		return 0, 0, errors.New("VNC server needs a password")
	}
	if _, err := conn.Write([]byte{1}); err != nil {
		return 0, 0, err
	}
	var result uint32
	if err := binary.Read(r, binary.BigEndian, &result); err != nil || result != 0 {
		return 0, 0, errors.New("VNC security handshake failed")
	}
	if _, err := conn.Write([]byte{1}); err != nil { // ClientInit: shared
		return 0, 0, err
	}
	var init struct {
		Width, Height uint16
		Format        [16]byte
		NameLength    uint32
	}
	if err := binary.Read(r, binary.BigEndian, &init); err != nil {
		return 0, 0, err
	}
	if init.NameLength > 1<<16 {
		return 0, 0, errors.New("bad VNC server name")
	}
	if _, err := r.Discard(int(init.NameLength)); err != nil {
		return 0, 0, err
	}
	// 32-bit little-endian true colour: bytes arrive as R, G, B, unused.
	format := []byte{0, 0, 0, 0, 32, 24, 0, 1, 0, 255, 0, 255, 0, 255, 0, 8, 16, 0, 0, 0}
	// Raw pixels, plus the DesktopSize pseudo-encoding for resolution changes.
	encodings := []byte{2, 0, 0, 2, 0, 0, 0, 0, 0xff, 0xff, 0xff, 0x21}
	if _, err := conn.Write(append(format, encodings...)); err != nil {
		return 0, 0, err
	}
	if init.Width == 0 || init.Height == 0 || int(init.Width)*int(init.Height) > 8192*8192 {
		return 0, 0, errors.New("bad desktop size")
	}
	return int(init.Width), int(init.Height), nil
}

// readUpdate reads server messages until one framebuffer update is applied.
func readUpdate(r *bufio.Reader, fb **image.RGBA) (updated, resized bool, err error) {
	for {
		kind, err := r.ReadByte()
		if err != nil {
			return false, false, err
		}
		switch kind {
		case 0: // FramebufferUpdate
			var head struct {
				Pad   byte
				Rects uint16
			}
			if err := binary.Read(r, binary.BigEndian, &head); err != nil {
				return false, false, err
			}
			for i := 0; i < int(head.Rects); i++ {
				var rect struct {
					X, Y, W, H uint16
					Encoding   int32
				}
				if err := binary.Read(r, binary.BigEndian, &rect); err != nil {
					return false, false, err
				}
				switch rect.Encoding {
				case 0:
					if err := readRaw(r, *fb, int(rect.X), int(rect.Y), int(rect.W), int(rect.H)); err != nil {
						return false, false, err
					}
					updated = true
				case -223:
					if rect.W == 0 || rect.H == 0 || int(rect.W)*int(rect.H) > 8192*8192 {
						return false, false, errors.New("bad desktop size")
					}
					*fb = image.NewRGBA(image.Rect(0, 0, int(rect.W), int(rect.H)))
					resized = true
				default:
					return false, false, fmt.Errorf("unexpected VNC encoding %d", rect.Encoding)
				}
			}
			return updated, resized, nil
		case 1: // SetColourMapEntries
			var head struct {
				Pad          byte
				First, Count uint16
			}
			if err := binary.Read(r, binary.BigEndian, &head); err != nil {
				return false, false, err
			}
			if _, err := r.Discard(int(head.Count) * 6); err != nil {
				return false, false, err
			}
		case 2: // Bell
		case 3: // ServerCutText: the clipboard is not part of the view.
			var head struct {
				Pad    [3]byte
				Length uint32
			}
			if err := binary.Read(r, binary.BigEndian, &head); err != nil {
				return false, false, err
			}
			if head.Length > 1<<24 {
				return false, false, errors.New("clipboard too large")
			}
			if _, err := r.Discard(int(head.Length)); err != nil {
				return false, false, err
			}
		default:
			return false, false, fmt.Errorf("unexpected VNC message %d", kind)
		}
	}
}

func readRaw(r *bufio.Reader, fb *image.RGBA, x, y, w, h int) error {
	bounds := fb.Rect
	row := make([]byte, w*4)
	for line := 0; line < h; line++ {
		if _, err := io.ReadFull(r, row); err != nil {
			return err
		}
		py := y + line
		if py >= bounds.Dy() {
			continue
		}
		for col := 0; col < w; col++ {
			px := x + col
			if px >= bounds.Dx() {
				break
			}
			o := fb.PixOffset(px, py)
			fb.Pix[o], fb.Pix[o+1], fb.Pix[o+2], fb.Pix[o+3] = row[col*4], row[col*4+1], row[col*4+2], 255
		}
	}
	return nil
}

// serveFrame returns the newest frame as JPEG. With ?after=<version> it waits
// up to wait for a newer one and answers 204 if the screen did not change.
func (g *screenGrabber) serveFrame(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
	g.mu.Lock()
	deadline := time.Now().Add(3 * time.Second)
	if g.frame == nil {
		deadline = time.Now().Add(8 * time.Second)
	}
	g.ensure()
	timer := time.AfterFunc(time.Until(deadline), func() {
		g.mu.Lock()
		g.changed.Broadcast()
		g.mu.Unlock()
	})
	defer timer.Stop()
	for (g.frame == nil || g.version <= after) && time.Now().Before(deadline) && r.Context().Err() == nil {
		if !g.running {
			g.ensure()
		}
		g.changed.Wait()
	}
	frame, version, lastErr := g.frame, g.version, g.err
	if frame != nil && g.jpegVer != version {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, frame, &jpeg.Options{Quality: 72}); err == nil {
			g.jpegData, g.jpegVer = buf.Bytes(), version
		}
	}
	data := g.jpegData
	g.mu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case frame == nil:
		message := "the desktop has not sent a frame yet"
		if lastErr != nil {
			message = lastErr.Error()
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": message})
	case version <= after:
		w.Header().Set("X-Frame-Version", strconv.FormatUint(version, 10))
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("X-Frame-Version", strconv.FormatUint(version, 10))
		w.Header().Set("X-Frame-Size", fmt.Sprintf("%dx%d", frame.Rect.Dx(), frame.Rect.Dy()))
		_, _ = w.Write(data)
	}
}
