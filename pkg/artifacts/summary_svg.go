package artifacts

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/julienschmidt/httprouter"
)

// The runner links a job summary image to this endpoint. GitHub job summaries
// cannot set font sizes, borders or alignment, so the Artifacts card that
// mirrors GitHub's native list is drawn here as an SVG. The card data is passed
// in the "d" query parameter as base64url encoded JSON.

type artifactSummaryRow struct {
	Name   string `json:"n"`
	Size   string `json:"s"`
	Digest string `json:"d"`
}

const (
	summaryWidth     = 760
	summaryHeaderEnd = 138.0 // y of the line below the column headers
	summaryRowHeight = 57.0
	summaryMaxRows   = 50
)

func truncateRunes(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max-1]) + "…"
}

// summaryIconAt nests one of the localArtifactIcons at the given position.
func summaryIconAt(name string, x, y float64) string {
	svg, ok := localArtifactIcons[name]
	if !ok {
		return ""
	}
	return strings.Replace(svg, "<svg ", fmt.Sprintf(`<svg x="%g" y="%g" `, x, y), 1)
}

func renderArtifactSummarySVG(rows []artifactSummaryRow) string {
	height := summaryHeaderEnd + summaryRowHeight*float64(len(rows)) + 4
	esc := template.HTMLEscapeString

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%g" viewBox="0 0 %d %g" font-family="-apple-system,BlinkMacSystemFont,'Segoe UI','Noto Sans',Helvetica,Arial,sans-serif">`,
		summaryWidth, height, summaryWidth, height)
	fmt.Fprintf(&b, `<rect x="0.5" y="0.5" width="%d" height="%g" rx="6" fill="#ffffff" stroke="#d1d9e0"/>`, summaryWidth-1, height-1)
	b.WriteString(`<text x="20" y="40" font-size="20" font-weight="600" fill="#1f2328">Artifacts</text>`)
	b.WriteString(`<text x="20" y="64" font-size="14" fill="#59636e">Produced during runtime</text>`)
	for _, h := range []struct {
		x     int
		label string
	}{{20, "Name"}, {360, "Size"}, {468, "Digest"}} {
		fmt.Fprintf(&b, `<text x="%d" y="111" font-size="14" font-weight="600" fill="#59636e">%s</text>`, h.x, h.label)
	}
	fmt.Fprintf(&b, `<line x1="1" x2="%d" y1="%g" y2="%g" stroke="#d1d9e0"/>`, summaryWidth-1, summaryHeaderEnd+0.5, summaryHeaderEnd+0.5)

	for i, row := range rows {
		cy := summaryHeaderEnd + summaryRowHeight*float64(i) + summaryRowHeight/2
		b.WriteString(summaryIconAt("package", 20, cy-8))
		fmt.Fprintf(&b, `<text x="46" y="%g" font-size="16" font-weight="600" fill="#1f2328">%s</text>`, cy+5.5, esc(truncateRunes(row.Name, 34)))
		fmt.Fprintf(&b, `<text x="360" y="%g" font-size="16" fill="#1f2328">%s</text>`, cy+5.5, esc(truncateRunes(row.Size, 14)))
		if digest := truncateRunes(row.Digest, 24); digest != "" {
			fmt.Fprintf(&b, `<text x="468" y="%g" font-size="14" fill="#1f2328" font-family="ui-monospace,SFMono-Regular,'SF Mono',Menlo,Consolas,monospace">%s</text>`, cy+5, esc(digest))
			b.WriteString(summaryIconAt("copy", 468+float64(len([]rune(digest)))*8.4+10, cy-8))
		} else {
			fmt.Fprintf(&b, `<text x="468" y="%g" font-size="16" fill="#1f2328">—</text>`, cy+5.5)
		}
		b.WriteString(summaryIconAt("download", 672, cy-8))
		b.WriteString(summaryIconAt("trash", 706, cy-8))
		if i < len(rows)-1 {
			y := summaryHeaderEnd + summaryRowHeight*float64(i+1) + 0.5
			fmt.Fprintf(&b, `<line x1="1" x2="%d" y1="%g" y2="%g" stroke="#d1d9e0"/>`, summaryWidth-1, y, y)
		}
	}
	b.WriteString(`</svg>`)
	return b.String()
}

func localArtifactSummary(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(r.URL.Query().Get("d"), "="))
	var rows []artifactSummaryRow
	if err != nil || json.Unmarshal(raw, &rows) != nil || len(rows) == 0 || len(rows) > summaryMaxRows {
		http.Error(w, "invalid summary data", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write([]byte(renderArtifactSummarySVG(rows)))
}
