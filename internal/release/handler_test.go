package release

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUploadLatestAndDownload(t *testing.T) {
	store, handler := testHandler(t, "upload-secret")
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	response := upload(t, server.URL, "upload-secret", "0.1.0-m7", "2", "First public build", []byte("PK\x03\x04test-apk"))
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("upload status = %d, want %d", response.StatusCode, http.StatusCreated)
	}
	var meta Metadata
	decodeJSON(t, response, &meta)
	if meta.Version != "0.1.0-m7" || meta.VersionCode != 2 || meta.SizeBytes != 12 {
		t.Fatalf("unexpected metadata: %+v", meta)
	}
	if meta.SHA256 == "" || meta.DownloadURL != "/api/v1/releases/latest/apk" {
		t.Fatalf("incomplete metadata: %+v", meta)
	}

	latest, err := http.Get(server.URL + "/api/v1/releases/latest")
	if err != nil {
		t.Fatal(err)
	}
	defer latest.Body.Close()
	if latest.StatusCode != http.StatusOK {
		t.Fatalf("latest status = %d", latest.StatusCode)
	}
	var latestMeta Metadata
	if err := json.NewDecoder(latest.Body).Decode(&latestMeta); err != nil {
		t.Fatal(err)
	}
	if latestMeta.SHA256 != meta.SHA256 {
		t.Fatalf("latest sha256 = %q, want %q", latestMeta.SHA256, meta.SHA256)
	}

	download, err := http.Get(server.URL + meta.DownloadURL)
	if err != nil {
		t.Fatal(err)
	}
	defer download.Body.Close()
	if download.StatusCode != http.StatusOK {
		t.Fatalf("download status = %d", download.StatusCode)
	}
	body, err := io.ReadAll(download.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "PK\x03\x04test-apk" {
		t.Fatalf("downloaded body = %q", body)
	}
	if download.Header.Get("Content-Type") != "application/vnd.android.package-archive" {
		t.Fatalf("download content type = %q", download.Header.Get("Content-Type"))
	}
	head, err := http.Head(server.URL + meta.DownloadURL)
	if err != nil {
		t.Fatal(err)
	}
	defer head.Body.Close()
	if head.StatusCode != http.StatusOK {
		t.Fatalf("head status = %d, want %d", head.StatusCode, http.StatusOK)
	}
	if head.ContentLength != meta.SizeBytes {
		t.Fatalf("head content length = %d, want %d", head.ContentLength, meta.SizeBytes)
	}
	if _, err := os.Stat(filepath.Join(store.dir, meta.Filename)); err != nil {
		t.Fatalf("published file missing: %v", err)
	}
}

func TestUploadRequiresBearerToken(t *testing.T) {
	_, handler := testHandler(t, "upload-secret")
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	for _, token := range []string{"", "wrong"} {
		response := upload(t, server.URL, token, "0.1.0", "1", "", []byte("PK\x03\x04apk"))
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Errorf("token %q status = %d, want %d", token, response.StatusCode, http.StatusUnauthorized)
		}
	}
}

func TestUploadRejectsOversizeAndNonAPK(t *testing.T) {
	store, err := NewStore(t.TempDir(), 8)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(store, "secret")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	tooLarge := upload(t, server.URL, "secret", "0.1.0", "1", "", []byte("PK\x03\x04too-large"))
	tooLarge.Body.Close()
	if tooLarge.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversize status = %d, want %d", tooLarge.StatusCode, http.StatusBadRequest)
	}
	notAPK := upload(t, server.URL, "secret", "0.1.0", "1", "", []byte("not-an-apk"))
	notAPK.Body.Close()
	if notAPK.StatusCode != http.StatusBadRequest {
		t.Fatalf("non-APK status = %d, want %d", notAPK.StatusCode, http.StatusBadRequest)
	}
}

func TestLandingPageIsServed(t *testing.T) {
	_, handler := testHandler(t, "secret")
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("landing status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "HUSHI") {
		t.Fatal("landing page does not contain HUSHI")
	}
}

func testHandler(t *testing.T, token string) (*Store, http.Handler) {
	t.Helper()
	store, err := NewStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(store, token)
	if err != nil {
		t.Fatal(err)
	}
	return store, handler
}

func upload(t *testing.T, base, token, version, versionCode, notes string, apk []byte) *http.Response {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range map[string]string{
		"version":      version,
		"version_code": versionCode,
		"notes":        notes,
	} {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("apk", "app-release.apk")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(apk); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, base+"/api/v1/releases", &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeJSON(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}
