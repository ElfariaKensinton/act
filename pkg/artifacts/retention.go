package artifacts

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const defaultRetentionDays = 90
const retentionFile = ".retention.json"

type artifactRetention struct {
	ExpiresAt time.Time `json:"expires_at"`
}

// Keep the deadline on disk so a restart cannot extend an artifact's lifetime.
func writeArtifactRetention(fsys WriteFS, archive string, expiresAt time.Time) error {
	data, err := json.Marshal(artifactRetention{ExpiresAt: expiresAt.UTC()})
	if err != nil {
		return err
	}
	file, err := fsys.OpenWritable(filepath.Join(filepath.Dir(archive), retentionFile))
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	return errors.Join(writeErr, file.Close())
}

// Remove only artifact directories inside run directories; never follow symlinks.
// Older artifacts without metadata use their newest file's modification time.
func cleanupExpiredArtifacts(root string, retentionDays int, now time.Time) error {
	runs, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var cleanupErr error
	for _, run := range runs {
		if !run.IsDir() {
			continue
		}
		runPath := filepath.Join(root, run.Name())
		artifacts, err := os.ReadDir(runPath)
		if err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
			continue
		}
		for _, artifact := range artifacts {
			if !artifact.IsDir() {
				continue
			}
			artifactPath := filepath.Join(runPath, artifact.Name())
			expiresAt, err := artifactExpiration(artifactPath, retentionDays)
			if err != nil {
				cleanupErr = errors.Join(cleanupErr, err)
				continue
			}
			if !expiresAt.IsZero() && !now.Before(expiresAt) {
				cleanupErr = errors.Join(cleanupErr, os.RemoveAll(artifactPath))
			}
		}
	}
	return cleanupErr
}

func artifactExpiration(dir string, retentionDays int) (time.Time, error) {
	data, err := os.ReadFile(filepath.Join(dir, retentionFile))
	if err == nil {
		var retention artifactRetention
		if err := json.Unmarshal(data, &retention); err != nil {
			return time.Time{}, err
		}
		if retention.ExpiresAt.IsZero() {
			return time.Time{}, errors.New("artifact retention deadline is missing: " + dir)
		}
		return retention.ExpiresAt, nil
	}
	if !os.IsNotExist(err) {
		return time.Time{}, err
	}
	var newest time.Time
	err = filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return err
	})
	if err != nil || newest.IsZero() {
		return time.Time{}, err
	}
	return newest.Add(time.Duration(retentionDays) * 24 * time.Hour), nil
}
