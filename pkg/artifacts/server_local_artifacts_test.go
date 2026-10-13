package artifacts

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/julienschmidt/httprouter"
)

func TestLocalArtifactsPageListsZipFiles(t *testing.T) {
	root := t.TempDir()
	artifact := filepath.Join(root, "run-123", "GboardiOS-ipa", "GboardiOS-ipa.zip")
	if err := os.MkdirAll(filepath.Dir(artifact), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, []byte("ipa archive"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	localArtifactsPage(root, rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "GboardiOS-ipa") || !strings.Contains(body, "local-artifacts/download?path=") {
		t.Fatalf("artifact name or download link missing from page: %s", body)
	}
}

func TestDownloadLocalArtifact(t *testing.T) {
	root := t.TempDir()
	artifact := filepath.Join(root, "run-123", "GboardiOS-ipa", "GboardiOS-ipa.zip")
	if err := os.MkdirAll(filepath.Dir(artifact), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, []byte("zip payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/local-artifacts/download?path=run-123%2FGboardiOS-ipa%2FGboardiOS-ipa.zip", nil)
	rec := httptest.NewRecorder()
	downloadLocalArtifact(root, rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "zip payload" {
		t.Fatalf("downloaded body = %q, want zip payload", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/zip" {
		t.Fatalf("Content-Type = %q, want application/zip", got)
	}
}

func TestDownloadLocalArtifactRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	req := httptest.NewRequest("GET", "/local-artifacts/download?path=..%2F..%2Fsecret.txt", nil)
	rec := httptest.NewRecorder()
	downloadLocalArtifact(root, rec, req)

	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestDownloadLocalArtifactMapsOpaqueRunID(t *testing.T) {
	root := t.TempDir()
	const backendID = "62f0f060-0000-4000-8000-123456789abc"
	artifact := filepath.Join(root, fmt.Sprint(runKeyFromBackendID(backendID)), "app", "app.zip")
	if err := os.MkdirAll(filepath.Dir(artifact), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, []byte("zip payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/local-artifacts/download?path="+backendID+"%2Fapp%2Fapp.zip", nil)
	rec := httptest.NewRecorder()
	downloadLocalArtifact(root, rec, req)

	if rec.Code != 200 || rec.Body.String() != "zip payload" {
		t.Fatalf("status = %d body = %q, want 200 zip payload", rec.Code, rec.Body.String())
	}
}

func TestLocalArtifactIconServesSVG(t *testing.T) {
	rec := httptest.NewRecorder()
	localArtifactIcon(rec, httptest.NewRequest("GET", "/local-artifacts/icon/download.svg", nil), httprouter.Params{{Key: "name", Value: "download.svg"}})
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/svg+xml" || !strings.HasPrefix(rec.Body.String(), "<svg") {
		t.Fatalf("status = %d type = %q body = %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
}
