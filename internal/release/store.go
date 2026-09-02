package release

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var ErrNoRelease = errors.New("no release published")

// Metadata is the public description of the latest Android release.
// Filename is kept internal to the API contract and is never accepted from a
// client; it lets the store publish a new file before switching latest.json.
type Metadata struct {
	Version     string    `json:"version"`
	VersionCode int       `json:"version_code"`
	Notes       string    `json:"notes,omitempty"`
	Mandatory   bool      `json:"mandatory"`
	PublishedAt time.Time `json:"published_at"`
	SizeBytes   int64     `json:"size_bytes"`
	SHA256      string    `json:"sha256"`
	DownloadURL string    `json:"download_url"`
	Filename    string    `json:"filename"`
}

type Store struct {
	dir      string
	maxBytes int64
	mu       sync.RWMutex
}

func NewStore(dir string, maxBytes int64) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("release directory must not be empty")
	}
	if maxBytes <= 0 {
		return nil, errors.New("release max bytes must be positive")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create release directory: %w", err)
	}
	return &Store{dir: dir, maxBytes: maxBytes}, nil
}

func (s *Store) MaxBytes() int64 { return s.maxBytes }

func (s *Store) Current() (Metadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentLocked()
}

func (s *Store) OpenLatest() (Metadata, *os.File, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	meta, err := s.currentLocked()
	if err != nil {
		return Metadata{}, nil, err
	}
	file, err := os.Open(filepath.Join(s.dir, meta.Filename))
	if err != nil {
		return Metadata{}, nil, fmt.Errorf("open latest APK: %w", err)
	}
	return meta, file, nil
}

// Publish stores an APK under an immutable generated name, then atomically
// replaces latest.json. Readers therefore see either the previous complete
// release or the new complete release, never a partial upload.
func (s *Store) Publish(meta Metadata, src io.Reader) (Metadata, error) {
	if err := validateMetadata(meta); err != nil {
		return Metadata{}, err
	}
	if src == nil {
		return Metadata{}, errors.New("APK upload is empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tmp, err := os.CreateTemp(s.dir, ".upload-*.apk")
	if err != nil {
		return Metadata{}, fmt.Errorf("create upload staging file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o640); err != nil {
		tmp.Close()
		return Metadata{}, fmt.Errorf("protect upload staging file: %w", err)
	}

	digest := sha256.New()
	written, err := io.Copy(io.MultiWriter(tmp, digest), io.LimitReader(src, s.maxBytes+1))
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return Metadata{}, fmt.Errorf("write APK upload: %w", err)
	}
	if written == 0 {
		return Metadata{}, errors.New("APK upload is empty")
	}
	if written > s.maxBytes {
		return Metadata{}, fmt.Errorf("APK exceeds %d byte limit", s.maxBytes)
	}
	if err := looksLikeAPK(tmpName); err != nil {
		return Metadata{}, err
	}

	sum := hex.EncodeToString(digest.Sum(nil))
	meta.SizeBytes = written
	meta.SHA256 = sum
	meta.PublishedAt = time.Now().UTC()
	meta.DownloadURL = "/api/v1/releases/latest/apk"
	meta.Filename = fmt.Sprintf("hushi-%s-%d-%s.apk", safePart(meta.Version), meta.VersionCode, sum[:12])

	finalPath := filepath.Join(s.dir, meta.Filename)
	if err := os.Rename(tmpName, finalPath); err != nil {
		return Metadata{}, fmt.Errorf("publish APK: %w", err)
	}
	if err := os.Chmod(finalPath, 0o640); err != nil {
		return Metadata{}, fmt.Errorf("protect published APK: %w", err)
	}

	encoded, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return Metadata{}, fmt.Errorf("encode release metadata: %w", err)
	}
	metadataFile, err := os.CreateTemp(s.dir, ".latest-*.json")
	if err != nil {
		return Metadata{}, fmt.Errorf("create metadata staging file: %w", err)
	}
	metadataName := metadataFile.Name()
	defer os.Remove(metadataName)
	if err := metadataFile.Chmod(0o640); err == nil {
		_, err = metadataFile.Write(append(encoded, '\n'))
	}
	if closeErr := metadataFile.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return Metadata{}, fmt.Errorf("write release metadata: %w", err)
	}
	if err := os.Rename(metadataName, filepath.Join(s.dir, "latest.json")); err != nil {
		return Metadata{}, fmt.Errorf("publish release metadata: %w", err)
	}
	return meta, nil
}

func (s *Store) currentLocked() (Metadata, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, "latest.json"))
	if errors.Is(err, os.ErrNotExist) {
		return Metadata{}, ErrNoRelease
	}
	if err != nil {
		return Metadata{}, fmt.Errorf("read release metadata: %w", err)
	}
	var meta Metadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return Metadata{}, fmt.Errorf("decode release metadata: %w", err)
	}
	if err := validateMetadata(meta); err != nil {
		return Metadata{}, fmt.Errorf("invalid release metadata: %w", err)
	}
	if meta.Filename == "" || filepath.Base(meta.Filename) != meta.Filename {
		return Metadata{}, errors.New("invalid release filename")
	}
	return meta, nil
}

func validateMetadata(meta Metadata) error {
	version := strings.TrimSpace(meta.Version)
	if version == "" || len(version) > 128 {
		return errors.New("version must be between 1 and 128 characters")
	}
	if meta.VersionCode <= 0 {
		return errors.New("version_code must be positive")
	}
	if len(meta.Notes) > 8<<10 {
		return errors.New("notes are too long")
	}
	return nil
}

func looksLikeAPK(name string) error {
	file, err := os.Open(name)
	if err != nil {
		return fmt.Errorf("inspect APK upload: %w", err)
	}
	defer file.Close()
	header := make([]byte, 4)
	if _, err := io.ReadFull(file, header); err != nil {
		return errors.New("APK upload is not a valid ZIP/APK file")
	}
	if !bytes.Equal(header[:2], []byte("PK")) {
		return errors.New("APK upload is not a ZIP/APK file")
	}
	return nil
}

func safePart(value string) string {
	var out strings.Builder
	for _, r := range strings.TrimSpace(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			out.WriteRune(r)
		default:
			out.WriteByte('-')
		}
	}
	part := strings.Trim(out.String(), ".-")
	if part == "" {
		return "release"
	}
	return part
}
