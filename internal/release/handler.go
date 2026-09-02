package release

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

//go:embed web/*
var webFiles embed.FS

type Handler struct {
	store       *Store
	serverStore *ServerStore
	uploadToken string
	site        http.Handler
}

func NewHandler(store *Store, uploadToken string) (*Handler, error) {
	if store == nil {
		return nil, errors.New("release store must not be nil")
	}
	site, err := fsHandler()
	if err != nil {
		return nil, err
	}
	serverStore, err := NewServerStore(filepath.Join(store.dir, "server"), 128<<20)
	if err != nil {
		return nil, err
	}
	return &Handler{store: store, serverStore: serverStore, uploadToken: strings.TrimSpace(uploadToken), site: site}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/healthz":
		h.health(w, r)
	case "/api/v1/releases/latest":
		h.latest(w, r)
	case "/api/v1/releases/latest/apk":
		h.download(w, r)
	case "/api/v1/releases":
		h.upload(w, r)
	case "/install.sh":
		h.installScript(w, r)
	case "/api/v1/server/releases/latest":
		h.latestServer(w, r)
	case "/api/v1/server/releases":
		h.uploadServer(w, r)
	default:
		const serverAssetPrefix = "/api/v1/server/releases/latest/"
		if strings.HasPrefix(r.URL.Path, serverAssetPrefix) {
			h.downloadServer(w, r, strings.TrimPrefix(r.URL.Path, serverAssetPrefix))
			return
		}
		h.site.ServeHTTP(w, r)
	}
}

func (h *Handler) installScript(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	h.site.ServeHTTP(w, r)
}

func (h *Handler) latestServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	meta, err := h.serverStore.Current()
	if errors.Is(err, ErrNoServerRelease) {
		writeError(w, http.StatusNotFound, "no_server_release", "No server release has been published")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_release_unavailable", "Latest server release is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

func (h *Handler) downloadServer(w http.ResponseWriter, r *http.Request, filename string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD")
		return
	}
	asset, file, err := h.serverStore.OpenLatest(filename)
	if errors.Is(err, ErrNoServerRelease) || errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "server_asset_not_found", "Server asset is unavailable")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_release_unavailable", "Server asset is unavailable")
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", asset.Filename))
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, asset.Filename, time.Time{}, file)
}

func (h *Handler) uploadServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if h.uploadToken == "" {
		writeError(w, http.StatusServiceUnavailable, "upload_disabled", "Release uploads are not configured")
		return
	}
	if !hasBearerToken(r, h.uploadToken) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "A valid release upload token is required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, h.serverStore.MaxBytes()+2<<20)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "invalid_upload", "The server release is too large or malformed")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	version := strings.TrimSpace(r.FormValue("version"))
	files := r.MultipartForm.File["asset"]
	if len(files) == 0 {
		writeError(w, http.StatusBadRequest, "missing_assets", "Repeated multipart field asset is required")
		return
	}
	meta, err := h.serverStore.Publish(version, files)
	if err != nil {
		writeError(w, http.StatusBadRequest, "publish_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, meta)
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) latest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	meta, err := h.store.Current()
	if errors.Is(err, ErrNoRelease) {
		writeError(w, http.StatusNotFound, "no_release", "No APK release has been published")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "release_unavailable", "Latest release is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

func (h *Handler) download(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD")
		return
	}
	meta, file, err := h.store.OpenLatest()
	if errors.Is(err, ErrNoRelease) {
		writeError(w, http.StatusNotFound, "no_release", "No APK release has been published")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "release_unavailable", "Latest APK is unavailable")
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", meta.Filename))
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, meta.Filename, meta.PublishedAt, file)
}

func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if h.uploadToken == "" {
		writeError(w, http.StatusServiceUnavailable, "upload_disabled", "Release uploads are not configured")
		return
	}
	if !hasBearerToken(r, h.uploadToken) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "A valid release upload token is required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, h.store.MaxBytes()+2<<20)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "invalid_upload", "The APK upload is too large or malformed")
		return
	}
	file, _, err := r.FormFile("apk")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing_apk", "Multipart field apk is required")
		return
	}
	defer file.Close()
	version := strings.TrimSpace(r.FormValue("version"))
	versionCode, err := strconv.Atoi(strings.TrimSpace(r.FormValue("version_code")))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_version_code", "version_code must be a positive integer")
		return
	}
	mandatory, err := parseOptionalBool(r.FormValue("mandatory"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_mandatory", "mandatory must be true or false")
		return
	}
	meta, err := h.store.Publish(Metadata{
		Version:     version,
		VersionCode: versionCode,
		Notes:       strings.TrimSpace(r.FormValue("notes")),
		Mandatory:   mandatory,
	}, file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "publish_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, meta)
}

func hasBearerToken(r *http.Request, expected string) bool {
	value := r.Header.Get("Authorization")
	prefix := "Bearer "
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	provided := strings.TrimSpace(strings.TrimPrefix(value, prefix))
	if provided == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func parseOptionalBool(value string) (bool, error) {
	if strings.TrimSpace(value) == "" {
		return false, nil
	}
	return strconv.ParseBool(value)
}

func fsHandler() (http.Handler, error) {
	root, err := fs.Sub(webFiles, "web")
	if err != nil {
		return nil, fmt.Errorf("prepare landing page: %w", err)
	}
	return http.FileServer(http.FS(root)), nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": message, "code": code})
}

func methodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method is not allowed")
}
