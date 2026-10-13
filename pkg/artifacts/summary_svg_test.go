package artifacts

import (
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRenderArtifactSummarySVG(t *testing.T) {
	svg := renderArtifactSummarySVG([]artifactSummaryRow{{Name: "app <ipa>", Size: "1.02 MB", Digest: "sha256:73846d3d…"}})
	for _, want := range []string{"<svg", "Artifacts", "Produced during runtime", "app &lt;ipa&gt;", "1.02 MB", "sha256:73846d3d…"} {
		if !strings.Contains(svg, want) {
			t.Fatalf("summary svg is missing %q: %s", want, svg)
		}
	}
}

func TestLocalArtifactSummaryHandler(t *testing.T) {
	data, err := json.Marshal([]artifactSummaryRow{{Name: "a", Size: "1 MB"}})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	localArtifactSummary(rec, httptest.NewRequest("GET", "/local-artifacts/summary.svg?d="+base64.RawURLEncoding.EncodeToString(data), nil), nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/svg+xml" || !strings.HasPrefix(rec.Body.String(), "<svg") {
		t.Fatalf("status = %d type = %q body = %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}

	bad := httptest.NewRecorder()
	localArtifactSummary(bad, httptest.NewRequest("GET", "/local-artifacts/summary.svg?d=!!!", nil), nil)
	if bad.Code != 400 {
		t.Fatalf("status = %d, want 400", bad.Code)
	}
}
