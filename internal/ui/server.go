package ui

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
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

	// Public endpoints (no auth)
	mux.HandleFunc("/api/auth/login", s.api.Login)
	mux.HandleFunc("/api/status", s.api.Status)

	// Protected API routes
	mux.Handle("/api/keys", s.authMiddleware(http.HandlerFunc(s.api.Keys)))
	mux.Handle("/api/keys/rotate", s.authMiddleware(http.HandlerFunc(s.api.RotateKey)))
	mux.Handle("/api/keys/current", s.authMiddleware(http.HandlerFunc(s.api.KeyValue)))
	mux.Handle("/api/secrets", s.authMiddleware(http.HandlerFunc(s.api.Secrets)))
	mux.Handle("/api/secrets/", s.authMiddleware(http.HandlerFunc(s.api.SecretDetail)))
	mux.Handle("/api/secrets/decrypt", s.authMiddleware(http.HandlerFunc(s.api.DecryptSecret)))
	mux.Handle("/api/csi/pods", s.authMiddleware(http.HandlerFunc(s.api.CSIPods)))
	mux.Handle("/api/csi/classes", s.authMiddleware(http.HandlerFunc(s.api.CSIClasses)))
	mux.Handle("/api/csi/all-pods", s.authMiddleware(http.HandlerFunc(s.api.AllPods)))
	mux.Handle("/api/csi/attach", s.authMiddleware(http.HandlerFunc(s.api.CSIAttachSecret)))
	mux.Handle("/api/metrics", s.authMiddleware(http.HandlerFunc(s.api.Metrics)))
	mux.Handle("/api/openbao", s.authMiddleware(http.HandlerFunc(s.api.OpenBaoInfo)))
	mux.Handle("/api/cluster", s.authMiddleware(http.HandlerFunc(s.api.ClusterInfo)))
	mux.Handle("/api/namespaces", s.authMiddleware(http.HandlerFunc(s.api.Namespaces)))

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

// authMiddleware checks the X-Token header matches the configured OpenBao token.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Token")
		if token == "" {
			token = r.URL.Query().Get("token")
		}
		if token == "" || token != s.cfg.OpenBaoToken {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func logMiddleware(logger hclog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			return
		}
		logger.Debug("request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start))
	})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Token")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
