package mcpserver

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadIcon(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, size int) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(strings.Repeat("x", size)), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	def, err := LoadIcon("")
	if err != nil || def.MIME != "image/svg+xml" || !strings.Contains(string(def.Data), "<svg") {
		t.Fatalf("default icon: %v %q", err, def.MIME)
	}
	if ic, err := LoadIcon(write("logo.PNG", 10)); err != nil || ic.MIME != "image/png" {
		t.Errorf("png: %v %q", err, ic.MIME)
	}
	if _, err := LoadIcon(write("logo.gif", 10)); err == nil {
		t.Error("an unsupported extension must be refused")
	}
	if _, err := LoadIcon(write("big.png", maxIconSize+1)); err == nil {
		t.Error("an oversized icon must be refused")
	}
	if _, err := LoadIcon(filepath.Join(dir, "missing.png")); err == nil {
		t.Error("a missing file must be refused")
	}
}

func TestIconHandlerAndServerInfo(t *testing.T) {
	rec := httptest.NewRecorder()
	DefaultIcon().Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/favicon.ico", nil))
	h := rec.Result().Header
	if rec.Code != 200 || h.Get("Content-Type") != "image/svg+xml" || h.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("icon response: %d %v", rec.Code, h)
	}

	if got := New(Deps{}, Settings{ProductName: "Atrium"}).icons(); got != nil {
		t.Errorf("no icon url, got %v", got)
	}
	got := New(Deps{}, Settings{ProductName: "Atrium", IconURL: "https://atrium.example/icon", IconMIME: "image/svg+xml"}).icons()
	if len(got) != 1 || got[0].Source != "https://atrium.example/icon" || got[0].MIMEType != "image/svg+xml" {
		t.Errorf("icons: %+v", got)
	}
}
