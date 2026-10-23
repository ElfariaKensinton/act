package artifacts

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/julienschmidt/httprouter"
)

func TestArtifactRetentionSurvivesRestartAndDeletesExpiredFiles(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	router := httprouter.New()
	storage := readWriteFSImpl{}
	RoutesV4(router, root, storage, storage, 7)
	for _, name := range []string{"short", "long", "fallback"} {
		expiration := ""
		if name != "fallback" {
			days := 2
			if name == "long" {
				days = 10
			}
			expiration = fmt.Sprintf(`,"expiresAt":%q`, now.Add(time.Duration(days)*24*time.Hour).Format(time.RFC3339))
		}
		body := fmt.Sprintf(`{"workflowRunBackendId":"123","name":%q,"version":4%s}`, name, expiration)
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, ArtifactV4RouteBase+"/CreateArtifact", strings.NewReader(body)))
		if resp.Code != http.StatusOK {
			t.Fatalf("create %s: status %d: %s", name, resp.Code, resp.Body.String())
		}
	}
	// Staged blocks must be removed along with the expired ZIP and metadata.
	blocks := artifactV4BlockPath(root, 123, "short", "block-id")
	if err := os.MkdirAll(filepath.Dir(blocks), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocks, []byte("staged"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Cleanup has no reference to the router: all deadlines come from disk,
	// even after a restart with a different fallback setting.
	if err := cleanupExpiredArtifacts(root, 1, now.Add(3*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "123", "short")); !os.IsNotExist(err) {
		t.Fatalf("expired artifact directory still exists: %v", err)
	}
	for _, name := range []string{"long", "fallback"} {
		if _, err := os.Stat(artifactV4ZipPath(root, 123, name)); err != nil {
			t.Fatalf("live artifact %s removed: %v", name, err)
		}
	}
	if err := cleanupExpiredArtifacts(root, 1, now.Add(8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(artifactV4ZipPath(root, 123, "fallback")); !os.IsNotExist(err) {
		t.Fatalf("CLI fallback deadline was not enforced: %v", err)
	}
	if _, err := os.Stat(artifactV4ZipPath(root, 123, "long")); err != nil {
		t.Fatalf("explicit upload expiration was shortened by fallback: %v", err)
	}
	if err := cleanupExpiredArtifacts(root, 1, now.Add(11*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, ArtifactV4RouteBase+"/ListArtifacts", strings.NewReader(`{"workflowRunBackendId":"123"}`)))
	if resp.Code != http.StatusOK || strings.Contains(resp.Body.String(), `"name"`) {
		t.Fatalf("expired artifacts still listed: %d %s", resp.Code, resp.Body.String())
	}
	// Previously issued direct links must also stop serving deleted artifacts.
	download := httptest.NewRecorder()
	downloadLocalArtifact(root, download, httptest.NewRequest(http.MethodGet, "/local-artifacts/download?path=123/short/short.zip", nil))
	if download.Code != http.StatusNotFound {
		t.Fatalf("expired direct download status = %d", download.Code)
	}
	routes := artifactV4Routes{baseDir: root, rfs: storage, prefix: ArtifactV4RouteBase, AppURL: "http://localhost"}
	download = httptest.NewRecorder()
	router.ServeHTTP(download, httptest.NewRequest(http.MethodGet, routes.buildArtifactURL("DownloadArtifact", "short", 123), nil))
	if download.Code != http.StatusNotFound {
		t.Fatalf("expired signed download status = %d", download.Code)
	}
}

func TestLegacyRetentionUsesNewestFileAndDoesNotFollowSymlinks(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	for _, name := range []string{"expired", "active"} {
		archive := artifactV4ZipPath(root, 123, name)
		if err := os.MkdirAll(filepath.Dir(archive), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(archive, []byte("zip"), 0o644); err != nil {
			t.Fatal(err)
		}
		old := now.Add(-8 * 24 * time.Hour)
		if err := os.Chtimes(archive, old, old); err != nil {
			t.Fatal(err)
		}
	}
	block := artifactV4BlockPath(root, 123, "active", "block")
	if err := os.MkdirAll(filepath.Dir(block), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(block, []byte("upload in progress"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "123", "linked")); err != nil {
		t.Fatal(err)
	}
	if err := cleanupExpiredArtifacts(root, 7, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(artifactV4ZipPath(root, 123, "expired")); !os.IsNotExist(err) {
		t.Fatalf("legacy expired artifact still exists: %v", err)
	}
	if _, err := os.Stat(block); err != nil {
		t.Fatalf("recent staged upload removed: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "123", "linked")); err != nil {
		t.Fatalf("symlink should not be traversed or removed: %v", err)
	}
}

func TestMalformedRetentionIsPreservedAndReported(t *testing.T) {
	root := t.TempDir()
	archive := artifactV4ZipPath(root, 123, "build")
	if err := os.MkdirAll(filepath.Dir(archive), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(archive), retentionFile), []byte(`{invalid`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cleanupExpiredArtifacts(root, 1, time.Now().Add(100*24*time.Hour)); err == nil {
		t.Fatal("malformed metadata should report an error")
	}
	if _, err := os.Stat(filepath.Dir(archive)); err != nil {
		t.Fatalf("artifact with malformed metadata removed: %v", err)
	}
}

func TestCreateRejectsExpiredDeadline(t *testing.T) {
	storage := readWriteFSImpl{}
	router := httprouter.New()
	RoutesV4(router, t.TempDir(), storage, storage)
	body := `{"workflowRunBackendId":"123","name":"expired","expiresAt":"2000-01-01T00:00:00Z"}`
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, ArtifactV4RouteBase+"/CreateArtifact", strings.NewReader(body)))
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expired create status = %d", resp.Code)
	}
}
