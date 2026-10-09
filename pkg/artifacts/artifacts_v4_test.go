package artifacts

import (
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
