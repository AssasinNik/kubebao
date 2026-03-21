package ui

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"time"

	"github.com/hashicorp/go-hclog"
)

//go:embed static/*
var staticFiles embed.FS

// Server is the KubeBao UI HTTP server.
type Server struct {
	cfg    *Config
	logger hclog.Logger
	api    *APIHandler
}

// NewServer creates the UI server.
func NewServer(cfg *Config, logger hclog.Logger) (*Server, error) {
	api, err := NewAPIHandler(cfg, logger)
	if err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, logger: logger, api: api}, nil
}

// Run starts the HTTP server and blocks until context is cancelled.
func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("/api/status", s.api.Status)
	mux.HandleFunc("/api/keys", s.api.Keys)
	mux.HandleFunc("/api/keys/rotate", s.api.RotateKey)
	mux.HandleFunc("/api/secrets", s.api.Secrets)
	mux.HandleFunc("/api/secrets/decrypt", s.api.DecryptSecret)
	mux.HandleFunc("/api/csi/pods", s.api.CSIPods)
	mux.HandleFunc("/api/metrics", s.api.Metrics)

	// Static frontend
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return err
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))

	srv := &http.Server{
		Addr:         s.cfg.ListenAddr,
		Handler:      corsMiddleware(logMiddleware(s.logger, mux)),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func logMiddleware(logger hclog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		logger.Debug("request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start))
	})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
