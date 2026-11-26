package main

import (
	"bufio"
	"encoding/binary"
	"image/jpeg"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// fakeVNC speaks enough RFB 3.8 to serve a 4x2 red desktop: the first
// (non-incremental) request gets the full frame, later ones an empty update.
func fakeVNC(t *testing.T) (string, func() int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	inputs := 0
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				r := bufio.NewReader(conn)
				conn.Write([]byte("RFB 003.008\n"))
				io.ReadFull(r, make([]byte, 12))
				conn.Write([]byte{1, 1})
				r.ReadByte()
				binary.Write(conn, binary.BigEndian, uint32(0))
				r.ReadByte()
				binary.Write(conn, binary.BigEndian, struct {
					W, H uint16
					F    [16]byte
					N    uint32
				}{4, 2, [16]byte{}, 4})
				conn.Write([]byte("test"))
				for {
					kind, err := r.ReadByte()
					if err != nil {
						return
					}
					switch kind {
					case 0:
						io.ReadFull(r, make([]byte, 19))
					case 2:
						head := make([]byte, 3)
						io.ReadFull(r, head)
						n := int(binary.BigEndian.Uint16(head[1:]))
						io.ReadFull(r, make([]byte, 4*n))
					case 3:
						req := make([]byte, 9)
						io.ReadFull(r, req)
						if req[0] == 0 {
							conn.Write([]byte{0, 0, 0, 1})
							binary.Write(conn, binary.BigEndian, struct {
								X, Y, W, H uint16
								E          int32
							}{0, 0, 4, 2, 0})
							for i := 0; i < 8; i++ {
								conn.Write([]byte{255, 0, 0, 0})
							}
						} else {
							conn.Write([]byte{0, 0, 0, 0})
						}
					default:
						inputs++ // key or pointer input: must never happen
						return
					}
				}
			}(conn)
		}
	}()
	return ln.Addr().String(), func() int { return inputs }
}

func TestFrameServesTheDesktopViewOnly(t *testing.T) {
	addr, inputs := fakeVNC(t)
	t.Setenv("VECTRABOT_VNC_ADDR", addr)
	root := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(newServer(testTokens(), root))
	defer srv.Close()
	get := func(path, token string) *http.Response {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := get("/v1/computer/frame", ""); res.StatusCode != 401 {
		t.Fatalf("frame without session %d", res.StatusCode)
	}
	res := get("/v1/computer/frame", "secret")
	defer res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/jpeg" || res.Header.Get("X-Frame-Size") != "4x2" {
		t.Fatalf("frame %d %v", res.StatusCode, res.Header)
	}
	img, err := jpeg.Decode(res.Body)
	if err != nil || img.Bounds().Dx() != 4 {
		t.Fatalf("decode %v", err)
	}
	if red, _, blue, _ := img.At(1, 1).RGBA(); red < 0xc000 || blue > 0x4000 {
		t.Fatalf("pixel colour wrong: r=%x b=%x", red, blue)
	}
	version := res.Header.Get("X-Frame-Version")
	same := get("/v1/computer/frame?after="+version, "secret")
	same.Body.Close()
	if same.StatusCode != 204 {
		t.Fatalf("unchanged frame %d", same.StatusCode)
	}
	if inputs() != 0 {
		t.Fatal("viewer sent input to the desktop")
	}
}

func TestListeningPortsSkipsInfraAndNonListeners(t *testing.T) {
	dir := t.TempDir()
	v4 := filepath.Join(dir, "tcp")
	body := "  sl  local_address rem_address   st\n" +
		"   0: 00000000:0BB8 00000000:0000 0A 0\n" + // 3000 listening
		"   1: 0100007F:2256 00000000:0000 0A 0\n" + // 8790 host: hidden
		"   2: 0100007F:1F90 0100007F:9C40 01 0\n" + // 8080 established: not a server
		"   3: 00000000:0016 00000000:0000 0A 0\n" + // 22 ssh: hidden
		"   4: 00000000:0050 00000000:0000 0A 0\n" // 80 web app
	v6 := filepath.Join(dir, "tcp6")
	body6 := "  sl  local_address rem_address st\n   0: 00000000000000000000000000000000:1435 00000000000000000000000000000000:0000 0A 0\n" // 5173
	os.WriteFile(v4, []byte(body), 0o644)
	os.WriteFile(v6, []byte(body6), 0o644)
	got := listeningPorts(v4, v6)
	if len(got) != 3 || got[0] != 80 || got[1] != 3000 || got[2] != 5173 {
		t.Fatalf("ports %v", got)
	}
}
