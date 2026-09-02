// Command hushi-website serves the public Hushi landing page and Android
// release API. Keep it behind a normal HTTPS reverse proxy in production.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"hushi-website/internal/release"
)

const defaultMaxAPKBytes = int64(512 << 20)

const (
	serverReadTimeout  = 10 * time.Minute
	serverWriteTimeout = 10 * time.Minute
)

func main() {
	fs := flag.NewFlagSet("hushi-website", flag.ExitOnError)
	addr := fs.String("addr", envOr("HUSHI_WEBSITE_ADDR", ":8080"), "listen address")
	dir := fs.String("dir", envOr("HUSHI_WEBSITE_RELEASE_DIR", "data/releases"), "directory for published APKs")
	maxBytes := fs.Int64("max-apk-bytes", envInt64("HUSHI_WEBSITE_MAX_APK_BYTES", defaultMaxAPKBytes), "maximum APK upload size")
	fs.Parse(os.Args[1:])

	store, err := release.NewStore(*dir, *maxBytes)
	if err != nil {
		log.Fatalf("release store: %v", err)
	}
	uploadToken := os.Getenv("HUSHI_WEBSITE_UPLOAD_TOKEN")
	if strings.TrimSpace(uploadToken) == "" {
		log.Print("WARNING: HUSHI_WEBSITE_UPLOAD_TOKEN is empty; release uploads are disabled")
	}
	handler, err := release.NewHandler(store, uploadToken)
	if err != nil {
		log.Fatalf("handler: %v", err)
	}

	server := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       serverReadTimeout,
		WriteTimeout:      serverWriteTimeout,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}()

	log.Printf("hushi website listening on http://%s", *addr)
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envInt64(name string, fallback int64) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(name)), 10, 64)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}
