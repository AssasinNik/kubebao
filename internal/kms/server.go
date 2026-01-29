/*
Copyright 2024 KubeBao Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package kms

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/hashicorp/go-hclog"
	"google.golang.org/grpc"
	"k8s.io/kms/apis/v2"
)

const (
	// APIVersion is the KMS API version
	APIVersion = "v2"

	// HealthStatusOK indicates the KMS plugin is healthy
	HealthStatusOK = "ok"
)

// Server implements the KMS v2 KeyManagementServiceServer interface
type Server struct {
	v2.UnimplementedKeyManagementServiceServer

	config   *Config
	transit  *TransitClient
	logger   hclog.Logger
	mu       sync.RWMutex
	keyID    string
	healthy  bool
}

// NewServer creates a new KMS server
func NewServer(config *Config, logger hclog.Logger) (*Server, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	if logger == nil {
		logger = hclog.NewNullLogger()
	}

	// Create transit client
	transit, err := NewTransitClient(config, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create transit client: %w", err)
	}

	server := &Server{
		config:  config,
		transit: transit,
		logger:  logger,
		healthy: false,
	}

	// Initialize the server (check/create key)
	if err := server.initialize(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to initialize server: %w", err)
	}

	return server, nil
}

// initialize initializes the KMS server
func (s *Server) initialize(ctx context.Context) error {
	s.logger.Info("initializing KMS server", "keyName", s.config.KeyName)

	// Check if the key exists
	keyInfo, err := s.transit.GetKeyInfo(ctx, s.config.KeyName)
	if err != nil {
		if !s.config.CreateKeyIfNotExists {
			return fmt.Errorf("transit key not found and createKeyIfNotExists is false: %w", err)
		}

		// Create the key
		s.logger.Info("creating transit key", "keyName", s.config.KeyName, "keyType", s.config.KeyType)
		if err := s.transit.CreateKey(ctx, s.config.KeyName, s.config.KeyType); err != nil {
			return fmt.Errorf("failed to create transit key: %w", err)
		}

		// Get key info again
		keyInfo, err = s.transit.GetKeyInfo(ctx, s.config.KeyName)
		if err != nil {
			return fmt.Errorf("failed to get key info after creation: %w", err)
		}
	}

	// Set key ID (includes version for key rotation detection)
	s.mu.Lock()
	s.keyID = fmt.Sprintf("%s:v%d", s.config.KeyName, keyInfo.LatestVersion)
	s.healthy = true
	s.mu.Unlock()

	s.logger.Info("KMS server initialized", "keyID", s.keyID)
	return nil
}

// Status returns the status of the KMS plugin
func (s *Server) Status(ctx context.Context, req *v2.StatusRequest) (*v2.StatusResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	healthStatus := HealthStatusOK
	if !s.healthy {
		healthStatus = "unhealthy"
	}

	return &v2.StatusResponse{
		Version: APIVersion,
		Healthz: healthStatus,
		KeyId:   s.keyID,
	}, nil
}

// Encrypt encrypts the given plaintext using the transit secrets engine
func (s *Server) Encrypt(ctx context.Context, req *v2.EncryptRequest) (*v2.EncryptResponse, error) {
	s.logger.Debug("encrypt request received", "uid", req.Uid, "plaintextSize", len(req.Plaintext))

	if len(req.Plaintext) == 0 {
		return nil, fmt.Errorf("plaintext cannot be empty")
	}

	// Encrypt using transit
	ciphertext, err := s.transit.Encrypt(ctx, s.config.KeyName, req.Plaintext)
	if err != nil {
		s.logger.Error("encryption failed", "error", err, "uid", req.Uid)
		return nil, fmt.Errorf("encryption failed: %w", err)
	}

	s.mu.RLock()
	keyID := s.keyID
	s.mu.RUnlock()

	// Create annotations (can be used for additional metadata)
	annotations := map[string][]byte{
		"kubebao.io/key-name": []byte(s.config.KeyName),
	}

	s.logger.Debug("encryption successful", "uid", req.Uid, "ciphertextSize", len(ciphertext))

	return &v2.EncryptResponse{
		Ciphertext:  []byte(ciphertext),
		KeyId:       keyID,
		Annotations: annotations,
	}, nil
}

// Decrypt decrypts the given ciphertext using the transit secrets engine
func (s *Server) Decrypt(ctx context.Context, req *v2.DecryptRequest) (*v2.DecryptResponse, error) {
	s.logger.Debug("decrypt request received", "uid", req.Uid, "keyId", req.KeyId, "ciphertextSize", len(req.Ciphertext))

	if len(req.Ciphertext) == 0 {
		return nil, fmt.Errorf("ciphertext cannot be empty")
	}

	// Decrypt using transit
	plaintext, err := s.transit.Decrypt(ctx, s.config.KeyName, string(req.Ciphertext))
	if err != nil {
		s.logger.Error("decryption failed", "error", err, "uid", req.Uid)
		return nil, fmt.Errorf("decryption failed: %w", err)
	}

	s.logger.Debug("decryption successful", "uid", req.Uid, "plaintextSize", len(plaintext))

	return &v2.DecryptResponse{
		Plaintext: plaintext,
	}, nil
}

// Run starts the KMS gRPC server
func (s *Server) Run(ctx context.Context) error {
	// Ensure socket directory exists
	socketDir := filepath.Dir(s.config.SocketPath)
	if err := os.MkdirAll(socketDir, 0755); err != nil {
		return fmt.Errorf("failed to create socket directory: %w", err)
	}

	// Remove existing socket file
	if err := os.Remove(s.config.SocketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove existing socket: %w", err)
	}

	// Create Unix socket listener
	listener, err := net.Listen("unix", s.config.SocketPath)
	if err != nil {
		return fmt.Errorf("failed to create listener: %w", err)
	}
	defer listener.Close()

	// Set socket permissions
	if err := os.Chmod(s.config.SocketPath, 0660); err != nil {
		return fmt.Errorf("failed to set socket permissions: %w", err)
	}

	// Create gRPC server
	grpcServer := grpc.NewServer()
	v2.RegisterKeyManagementServiceServer(grpcServer, s)

	s.logger.Info("KMS server starting", "socket", s.config.SocketPath)

	// Handle graceful shutdown
	go func() {
		<-ctx.Done()
		s.logger.Info("shutting down KMS server")
		grpcServer.GracefulStop()
	}()

	// Start health check routine
	go s.healthCheckLoop(ctx)

	// Start serving
	if err := grpcServer.Serve(listener); err != nil {
		return fmt.Errorf("gRPC server failed: %w", err)
	}

	return nil
}

// healthCheckLoop periodically checks the health of the OpenBao connection
func (s *Server) healthCheckLoop(ctx context.Context) {
	ticker := NewTicker(s.config.HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.performHealthCheck(ctx)
		}
	}
}

// performHealthCheck checks the health of the KMS plugin
func (s *Server) performHealthCheck(ctx context.Context) {
	// Try to get key info to verify connection
	keyInfo, err := s.transit.GetKeyInfo(ctx, s.config.KeyName)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err != nil {
		s.logger.Warn("health check failed", "error", err)
		s.healthy = false
		return
	}

	// Update key ID if version changed (key rotation)
	newKeyID := fmt.Sprintf("%s:v%d", s.config.KeyName, keyInfo.LatestVersion)
	if newKeyID != s.keyID {
		s.logger.Info("key version changed", "oldKeyID", s.keyID, "newKeyID", newKeyID)
		s.keyID = newKeyID
	}

	s.healthy = true
}

// GetKeyID returns the current key ID
func (s *Server) GetKeyID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.keyID
}

// IsHealthy returns whether the server is healthy
func (s *Server) IsHealthy() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.healthy
}
