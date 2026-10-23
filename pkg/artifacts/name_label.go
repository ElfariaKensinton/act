package artifacts

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/julienschmidt/httprouter"
)

// A linked image keeps its own text color when GitHub sanitizes summary CSS.
// Leave room for the widest glyphs so the label is never clipped or stretched.
func localArtifactNameLabel(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	name := req.URL.Query().Get("name")
	if name == "" || utf8.RuneCountInString(name) > 256 {
		http.Error(w, "name must contain 1-256 characters", http.StatusBadRequest)
		return
	}
	width := 2
	for _, char := range name {
		switch {
		case char == ' ':
			width += 4
		case strings.ContainsRune("ilI!.,:;|'", char):
			width += 5
		case strings.ContainsRune("frt()[]{}-", char):
			width += 6
		case strings.ContainsRune("mwMW@%&", char):
			width += 14
		case char >= 'A' && char <= 'Z':
			width += 11
		case char < 128:
			width += 9
		default:
			width += 16
		}
	}
	var text strings.Builder
	_ = xml.EscapeText(&text, []byte(name))
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="20" viewBox="0 0 %d 20"><text x="0" y="15" fill="#1f2328" font-family="Arial,Helvetica,sans-serif" font-size="14" font-weight="600">%s</text></svg>`, width, width, text.String())
}
