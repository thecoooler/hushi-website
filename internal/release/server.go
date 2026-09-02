package release

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var ErrNoServerRelease = errors.New("no server release published")

type ServerAsset struct {
	Filename  string `json:"filename"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

type ServerMetadata struct {
	Version     string                 `json:"version"`
	PublishedAt time.Time              `json:"published_at"`
	Assets      map[string]ServerAsset `json:"assets"`
}

type serverEnvelope struct {
	Directory string         `json:"directory"`
	Metadata  ServerMetadata `json:"metadata"`
}

type ServerStore struct {
	dir      string
	maxBytes int64
	mu       sync.RWMutex
}

func NewServerStore(dir string, maxBytes int64) (*ServerStore, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("server release directory must not be empty")
	}
	if maxBytes <= 0 {
		return nil, errors.New("server release max bytes must be positive")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create server release directory: %w", err)
	}
	return &ServerStore{dir: dir, maxBytes: maxBytes}, nil
}

func (s *ServerStore) MaxBytes() int64 { return s.maxBytes }

func (s *ServerStore) Current() (ServerMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	envelope, err := s.currentLocked()
	if err != nil {
		return ServerMetadata{}, err
	}
	return envelope.Metadata, nil
}

func (s *ServerStore) OpenLatest(filename string) (ServerAsset, *os.File, error) {
	if !safeAssetName(filename) {
		return ServerAsset{}, nil, errors.New("invalid server asset name")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	envelope, err := s.currentLocked()
	if err != nil {
		return ServerAsset{}, nil, err
	}
	asset, ok := envelope.Metadata.Assets[filename]
	if !ok {
		return ServerAsset{}, nil, os.ErrNotExist
	}
	file, err := os.Open(filepath.Join(s.dir, envelope.Directory, asset.Filename))
	if err != nil {
		return ServerAsset{}, nil, fmt.Errorf("open server asset: %w", err)
	}
	return asset, file, nil
}

// Publish stores a complete server release from repeated multipart asset
// fields. The current pointer changes only after every asset is present.
func (s *ServerStore) Publish(version string, files []*multipart.FileHeader) (ServerMetadata, error) {
	version = strings.TrimSpace(version)
	if version == "" || len(version) > 128 {
		return ServerMetadata{}, errors.New("version must be between 1 and 128 characters")
	}
	if len(files) == 0 || len(files) > 8 {
		return ServerMetadata{}, errors.New("a release must contain between 1 and 8 assets")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	directory, err := os.MkdirTemp(s.dir, ".server-release-")
	if err != nil {
		return ServerMetadata{}, fmt.Errorf("create server release staging directory: %w", err)
	}
	removeStaging := true
	defer func() {
		if removeStaging {
			_ = os.RemoveAll(directory)
		}
	}()

	metadata := ServerMetadata{
		Version: version, PublishedAt: time.Now().UTC(), Assets: make(map[string]ServerAsset, len(files)),
	}
	var total int64
	for _, header := range files {
		if header == nil || !safeAssetName(header.Filename) || header.Filename == "" {
			return ServerMetadata{}, errors.New("invalid server asset filename")
		}
		if _, exists := metadata.Assets[header.Filename]; exists {
			return ServerMetadata{}, fmt.Errorf("duplicate server asset %q", header.Filename)
		}
		file, err := header.Open()
		if err != nil {
			return ServerMetadata{}, fmt.Errorf("open uploaded server asset: %w", err)
		}
		path := filepath.Join(directory, header.Filename)
		out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
		if err != nil {
			file.Close()
			return ServerMetadata{}, fmt.Errorf("stage server asset: %w", err)
		}
		digest := sha256.New()
		written, copyErr := io.Copy(io.MultiWriter(out, digest), io.LimitReader(file, s.maxBytes-total+1))
		closeErr := out.Close()
		fileCloseErr := file.Close()
		if copyErr != nil {
			return ServerMetadata{}, fmt.Errorf("write server asset: %w", copyErr)
		}
		if closeErr != nil || fileCloseErr != nil {
			return ServerMetadata{}, errors.New("close server asset failed")
		}
		if written == 0 {
			return ServerMetadata{}, fmt.Errorf("server asset %q is empty", header.Filename)
		}
		total += written
		if total > s.maxBytes {
			return ServerMetadata{}, fmt.Errorf("server release exceeds %d byte limit", s.maxBytes)
		}
		metadata.Assets[header.Filename] = ServerAsset{
			Filename: header.Filename, SizeBytes: written, SHA256: hex.EncodeToString(digest.Sum(nil)),
		}
	}

	if err := writeJSONFile(filepath.Join(directory, "metadata.json"), metadata); err != nil {
		return ServerMetadata{}, fmt.Errorf("write server release metadata: %w", err)
	}
	envelope := serverEnvelope{Directory: filepath.Base(directory), Metadata: metadata}
	if err := writeJSONFile(filepath.Join(s.dir, "latest-server.json.tmp"), envelope); err != nil {
		return ServerMetadata{}, fmt.Errorf("stage server release pointer: %w", err)
	}
	if err := os.Rename(filepath.Join(s.dir, "latest-server.json.tmp"), filepath.Join(s.dir, "latest-server.json")); err != nil {
		return ServerMetadata{}, fmt.Errorf("publish server release pointer: %w", err)
	}
	removeStaging = false
	return metadata, nil
}

func (s *ServerStore) currentLocked() (serverEnvelope, error) {
	raw, err := os.ReadFile(filepath.Join(s.dir, "latest-server.json"))
	if errors.Is(err, os.ErrNotExist) {
		return serverEnvelope{}, ErrNoServerRelease
	}
	if err != nil {
		return serverEnvelope{}, fmt.Errorf("read server release pointer: %w", err)
	}
	var envelope serverEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return serverEnvelope{}, fmt.Errorf("decode server release pointer: %w", err)
	}
	if filepath.Base(envelope.Directory) != envelope.Directory || envelope.Directory == "" ||
		envelope.Metadata.Version == "" || len(envelope.Metadata.Assets) == 0 {
		return serverEnvelope{}, errors.New("invalid server release pointer")
	}
	for name, asset := range envelope.Metadata.Assets {
		if name != asset.Filename || !safeAssetName(name) || asset.SizeBytes <= 0 || len(asset.SHA256) != sha256.Size*2 {
			return serverEnvelope{}, errors.New("invalid server release asset metadata")
		}
	}
	return envelope, nil
}

func writeJSONFile(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o640); err != nil {
		return err
	}
	return os.Chmod(path, 0o640)
}

func safeAssetName(name string) bool {
	if name == "" || filepath.Base(name) != name || strings.Contains(name, "..") {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
