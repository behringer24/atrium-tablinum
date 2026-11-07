package mcpserver

import (
	_ "embed"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

//go:embed assets/icon.svg
var defaultIcon []byte

// maxIconSize bounds a configured icon; it is served without authentication.
const maxIconSize = 256 << 10

// Icon is the image shown for the server: the built-in one or a file chosen by the operator.
type Icon struct {
	Data []byte
	MIME string
}

// DefaultIcon returns the built-in Atrium icon.
func DefaultIcon() Icon { return Icon{Data: defaultIcon, MIME: "image/svg+xml"} }

// LoadIcon reads an icon file (SVG, PNG, ICO, JPEG or WebP). An empty path gives the default.
func LoadIcon(path string) (Icon, error) {
	if path == "" {
		return DefaultIcon(), nil
	}
	mime := map[string]string{
		".svg": "image/svg+xml", ".png": "image/png", ".ico": "image/x-icon",
		".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp",
	}[strings.ToLower(filepath.Ext(path))]
	if mime == "" {
		return Icon{}, fmt.Errorf("icon %q: use .svg, .png, .ico, .jpg or .webp", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return Icon{}, fmt.Errorf("icon: %w", err)
	}
	if info.Size() > maxIconSize {
		return Icon{}, fmt.Errorf("icon %q is larger than %d KB", path, maxIconSize>>10)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Icon{}, fmt.Errorf("icon: %w", err)
	}
	return Icon{Data: data, MIME: mime}, nil
}

// Handler serves the icon without authentication, as browsers and clients fetch favicons
// without credentials. The sandbox header keeps a script inside an SVG from running.
func (i Icon) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h := w.Header()
		h.Set("Content-Type", i.MIME)
		h.Set("Cache-Control", "public, max-age=86400")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
		_, _ = w.Write(i.Data)
	})
}

// icons lists the server icon for the server info, or nothing if none is configured.
func (s *Server) icons() []mcp.Icon {
	if s.set.IconURL == "" {
		return nil
	}
	return []mcp.Icon{{Source: s.set.IconURL, MIMEType: s.set.IconMIME, Sizes: []string{"any"}}}
}
