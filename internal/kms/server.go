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
	"github.com/kubebao/kubebao/internal/openbao"
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
	provider EncryptionProvider
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

	var provider EncryptionProvider
	var err error

	switch config.EncryptionProvider {
	case ProviderKuznyechik:
		provider, err = newKuznyechikProviderFromConfig(config, logger)
		if err != nil {
			return nil, fmt.Errorf("failed to create kuznyechik provider: %w", err)
		}
		logger.Info("using Kuznyechik (GOST R 34.12-2015) encryption provider")
	case ProviderTransit:
		fallthrough
	default:
		transit, transitErr := NewTransitClient(config, logger)
		if transitErr != nil {
			return nil, fmt.Errorf("failed to create transit client: %w", transitErr)
		}
		provider = transit
		logger.Info("using OpenBao Transit encryption provider")
	}

	server := &Server{
		config:   config,
		provider: provider,
		logger:   logger,
		healthy:  false,
	}

	// Initialize the server (check/create key)
	if err := server.initialize(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to initialize server: %w", err)
	}

	return server, nil
}

// newKuznyechikProviderFromConfig creates Kuznyechik provider with KeyManager
func newKuznyechikProviderFromConfig(config *Config, logger hclog.Logger) (*KuznyechikProvider, error) {
	baoClient, err := openbao.NewClient(config.OpenBao, logger)
	if err != nil {
		return nil, fmt.Errorf("openbao client: %w", err)
	}

	keyManager, err := NewKeyManager(baoClient, config.KVPathPrefix, config.KeyName, config.CreateKeyIfNotExists, logger)
	if err != nil {
		return nil, fmt.Errorf("key manager: %w", err)
	}

	return NewKuznyechikProvider(keyManager, logger), nil
}

// initialize initializes the KMS server
func (s *Server) initialize(ctx context.Context) error {
	s.logger.Info("initializing KMS server", "keyName", s.config.KeyName, "provider", s.config.EncryptionProvider)

	keyInfo, err := s.provider.GetKeyInfo(ctx, s.config.KeyName)
	if err != nil {
		if s.config.EncryptionProvider == ProviderKuznyechik {
			// Kuznyechik provider creates key on first Encrypt - GetKeyInfo may fail for new keys
			// Try to trigger key creation via GetOrCreateKey in the provider
			if s.config.CreateKeyIfNotExists {
				// KeyManager will create on first Encrypt; use placeholder keyID for now
				s.mu.Lock()
				s.keyID = fmt.Sprintf("%s:v1", s.config.KeyName)
				s.healthy = true
				s.mu.Unlock()
				s.logger.Info("KMS server initialized (kuznyechik, key will be created on first use)", "keyID", s.keyID)
				return nil
			}
		}

		if !s.config.CreateKeyIfNotExists {
			return fmt.Errorf("key not found and createKeyIfNotExists is false: %w", err)
		}

		// For Transit: create the key
		if transit, ok := s.provider.(*TransitClient); ok {
			s.logger.Info("creating transit key", "keyName", s.config.KeyName, "keyType", s.config.KeyType)
			if err := transit.CreateKey(ctx, s.config.KeyName, s.config.KeyType); err != nil {
				return fmt.Errorf("failed to create transit key: %w", err)
			}

			keyInfo, err = s.provider.GetKeyInfo(ctx, s.config.KeyName)
			if err != nil {
				return fmt.Errorf("failed to get key info after creation: %w", err)
			}
		}
	}

	if keyInfo != nil {
		s.mu.Lock()
		s.keyID = fmt.Sprintf("%s:v%d", s.config.KeyName, keyInfo.LatestVersion)
		s.healthy = true
		s.mu.Unlock()
		s.logger.Info("KMS server initialized", "keyID", s.keyID)
	}

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

	// Encrypt using provider
	ciphertext, err := s.provider.Encrypt(ctx, s.config.KeyName, req.Plaintext)
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

	// Decrypt using provider
	plaintext, err := s.provider.Decrypt(ctx, s.config.KeyName, string(req.Ciphertext))
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
	keyInfo, err := s.provider.GetKeyInfo(ctx, s.config.KeyName)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err != nil {
		// For Kuznyechik, key might not exist until first Encrypt
		if s.config.EncryptionProvider == ProviderKuznyechik && s.healthy {
			// Stay healthy if we were healthy - key might not exist yet
			return
		}
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
