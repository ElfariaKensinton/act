package artifacts

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestBuildArtifactURLPreservesHTTPS(t *testing.T) {
	routes := artifactV4Routes{
		prefix: ArtifactV4RouteBase,
		AppURL: "https://artifacts.example.com",
	}

	got := routes.buildArtifactURL("UploadArtifact", "test", 123)
	wantPrefix := "https://artifacts.example.com" + ArtifactV4RouteBase + "/UploadArtifact?"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("buildArtifactURL() = %q, want prefix %q", got, wantPrefix)
	}
}


type interruptedUploadBody struct {
	sent bool
}

func (b *interruptedUploadBody) Read(p []byte) (int, error) {
	if b.sent {
		return 0, io.ErrUnexpectedEOF
	}
	b.sent = true
	return copy(p, []byte("partial")), nil
}

func (b *interruptedUploadBody) Close() error { return nil }

func TestArtifactV4BlockRetryIsIdempotent(t *testing.T) {
	baseDir := t.TempDir()
	storage := readWriteFSImpl{}
	routes := &artifactV4Routes{
		prefix:  ArtifactV4RouteBase,
		fs:      storage,
		rfs:     storage,
		AppURL:  "http://localhost",
		baseDir: baseDir,
	}
	signedURL := routes.buildArtifactURL("UploadArtifact", "sample", 123)

	uploadBlock := func(blockID string, body io.ReadCloser, contentLength int64) *httptest.ResponseRecorder {
		u := signedURL + "&comp=block&blockid=" + url.QueryEscape(blockID)
		req := httptest.NewRequest(http.MethodPut, u, nil)
		req.Body = body
		req.ContentLength = contentLength
		resp := httptest.NewRecorder()
		routes.uploadArtifact(&ArtifactContext{Req: req, Resp: resp})
		return resp
	}

	firstID := base64.StdEncoding.EncodeToString([]byte("block-one"))
	partial := uploadBlock(firstID, &interruptedUploadBody{}, int64(len("complete-first")))
	if partial.Code != http.StatusInternalServerError {
		t.Fatalf("interrupted upload status = %d, want %d", partial.Code, http.StatusInternalServerError)
	}

	// Retrying the same block ID must replace the truncated bytes, not append.
	first := uploadBlock(firstID, io.NopCloser(strings.NewReader("complete-first")), int64(len("complete-first")))
	if first.Code != http.StatusCreated {
		t.Fatalf("retried block status = %d, want %d", first.Code, http.StatusCreated)
	}

	secondID := base64.StdEncoding.EncodeToString([]byte("block-two"))
	second := uploadBlock(secondID, io.NopCloser(strings.NewReader("second")), int64(len("second")))
	if second.Code != http.StatusCreated {
		t.Fatalf("second block status = %d, want %d", second.Code, http.StatusCreated)
	}

	blockList := "<BlockList><Latest>" + firstID + "</Latest><Latest>" + secondID + "</Latest></BlockList>"
	commitURL := signedURL + "&comp=blockList"
	commitReq := httptest.NewRequest(http.MethodPut, commitURL, strings.NewReader(blockList))
	commitResp := httptest.NewRecorder()
	routes.uploadArtifact(&ArtifactContext{Req: commitReq, Resp: commitResp})
	if commitResp.Code != http.StatusCreated {
		t.Fatalf("block list commit status = %d, want %d", commitResp.Code, http.StatusCreated)
	}

	got, err := os.ReadFile(artifactV4ZipPath(baseDir, 123, "sample"))
	if err != nil {
		t.Fatalf("reading committed artifact: %v", err)
	}
	if string(got) != "complete-firstsecond" {
		t.Fatalf("committed content = %q, want %q", got, "complete-firstsecond")
	}
	if _, err := os.Stat(artifactV4BlockDir(baseDir, 123, "sample")); !os.IsNotExist(err) {
		t.Fatalf("staged block directory should be removed after commit; stat error = %v", err)
	}
}
