package main

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The whole point of the back panel decoder: the LOW nibble is the LEFT pixel.
func TestDecodeL4NibbleOrder(t *testing.T) {
	// 0xF0 -> left = 0x0 (black), right = 0xF (white)
	got, err := decodeFrame([]byte{0xF0}, "L4", 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0, 0, 0, 255, 255, 255}
	if !bytes.Equal(got, want) {
		t.Fatalf("nibble order wrong: got %v, want %v", got, want)
	}
}

func TestDecodeL4Levels(t *testing.T) {
	// 0x88 -> both samples 0x8 -> 8*17 = 136
	got, err := decodeFrame([]byte{0x88}, "L4", 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range got {
		if v != 136 {
			t.Fatalf("level 0x8 should expand to 136, got %d", v)
		}
	}
}

func TestDecodeBase64Wrapped(t *testing.T) {
	raw := []byte{0x00, 0x11, 0x22}
	encoded := base64.StdEncoding.EncodeToString(raw)
	got, err := decodeFrame([]byte(encoded), "RGB888", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("got %v, want %v", got, raw)
	}
}

func TestDecodeShortFrame(t *testing.T) {
	if _, err := decodeFrame([]byte{1, 2, 3}, "RGB888", 72, 16); err == nil {
		t.Fatal("expected a short-frame error")
	}
}

func TestFramePNGRoundTrip(t *testing.T) {
	rgb := []byte{255, 0, 0, 0, 255, 0} // 2x1: red, green
	data, err := framePNG(rgb, 2, 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 6 || b.Dy() != 3 {
		t.Fatalf("scale 3 should give 6x3, got %dx%d", b.Dx(), b.Dy())
	}
	// Upscaling must not smear: the left half stays red, the right half green.
	if r, g, _, _ := img.At(0, 2).RGBA(); r>>8 != 255 || g>>8 != 0 {
		t.Fatal("left block is not red")
	}
	if r, g, _, _ := img.At(5, 0).RGBA(); r>>8 != 0 || g>>8 != 255 {
		t.Fatal("right block is not green")
	}
}

// The API takes #RRGGBBAA strings only — never an [r,g,b] array, never bare #rrggbb.
func TestParseColor(t *testing.T) {
	cases := map[string]string{
		"ff0044":       "#FF0044FF",
		"#ff0044":      "#FF0044FF",
		"#AAFF00FF":    "#AAFF00FF",
		"255,0,68":     "#FF0044FF",
		"255,0,68,128": "#FF004480",
	}
	for in, want := range cases {
		got, err := parseColor(in)
		if err != nil || got != want {
			t.Fatalf("parseColor(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"#abc", "1,2", "nope", "255,0,300"} {
		if _, err := parseColor(bad); err == nil {
			t.Fatalf("parseColor(%q) should have failed", bad)
		}
	}
}

// --for is seconds on the wire: 10s must send timeout:10, not 10000.
func TestElementLifetimeIsSeconds(t *testing.T) {
	cmd := displayText()
	if err := cmd.Flags().Set("for", "10s"); err != nil {
		t.Fatal(err)
	}
	var e elemFlags
	e.display = "front"
	e.lifetime = 10 * time.Second
	// Reuse the real command's flag set so Changed("for") is true.
	el, err := e.base(cmd, "text")
	if err != nil {
		t.Fatal(err)
	}
	if el["timeout"] != 10 {
		t.Fatalf("timeout = %v, want 10 (seconds)", el["timeout"])
	}
}

// One transport test: URL, headers and body of a real display draw.
func TestClientDrawRequest(t *testing.T) {
	var gotPath, gotToken, gotVer string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		gotToken = r.Header.Get("X-API-Token")
		gotVer = r.Header.Get(apiVersionHdr)
		gotBody, _ = readAll(r)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := NewClient(strings.TrimPrefix(srv.URL, "http://"), "secret", false, 5*time.Second, false)
	res, err := c.post("/display/draw", opts{
		params: map[string]any{"application_name": "t"},
		json:   map[string]any{"elements": []any{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/display/draw?application_name=t" {
		t.Fatalf("path/prefix wrong: %s", gotPath)
	}
	if gotToken != "secret" {
		t.Fatalf("local transport must use X-API-Token, got %q", gotToken)
	}
	if gotVer != defaultAPIVer {
		t.Fatalf("missing API version header, got %q", gotVer)
	}
	if !bytes.Contains(gotBody, []byte(`"elements"`)) {
		t.Fatalf("body wrong: %s", gotBody)
	}
	if m, ok := res.(map[string]any); !ok || m["ok"] != true {
		t.Fatalf("response not decoded: %#v", res)
	}
}

func TestCloudUsesBearer(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/status" {
			t.Errorf("cloud mode must not add /api, got %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", true, 5*time.Second, false)
	if _, err := c.get("/status", opts{}); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer tok" {
		t.Fatalf("got %q", auth)
	}
}

func readAll(r *http.Request) ([]byte, error) {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(r.Body)
	return buf.Bytes(), err
}
