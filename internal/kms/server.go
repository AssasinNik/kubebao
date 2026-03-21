// KMS сервер — gRPC API для шифрования/дешифрования секретов Kubernetes.
package kms

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/kubebao/kubebao/internal/openbao"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"k8s.io/kms/apis/v2"
)

const (
	APIVersion    = "v2"   // Версия KMS API (совместима с Kubernetes 1.25+)
	HealthStatusOK = "ok"  // Статус при успешной проверке здоровья
)

// Server — реализация gRPC KeyManagementService v2. Kubernetes вызывает Encrypt/Decrypt
// для секретов с encryptionConfiguration. Работает через Unix socket.
type Server struct {
	v2.UnimplementedKeyManagementServiceServer

	config   *Config
	provider EncryptionProvider
	logger   hclog.Logger
	mu       sync.RWMutex
	keyID    string
	healthy  bool
}

// NewServer — создаёт KMS сервер с провайдером Кузнечик (ГОСТ Р 34.12-2015).
// Transit-провайдер оставлен для обратной совместимости, но по умолчанию используется Кузнечик.
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
	case ProviderTransit:
		logger.Warn("Transit-провайдер — не рекомендуется для production. Используйте Kuznyechik (ГОСТ Р 34.12-2015).")
		transit, transitErr := NewTransitClient(config, logger)
		if transitErr != nil {
			return nil, fmt.Errorf("failed to create transit client: %w", transitErr)
		}
		provider = transit
	case ProviderKuznyechik:
		fallthrough
	default:
		provider, err = newKuznyechikProviderFromConfig(config, logger)
		if err != nil {
			return nil, fmt.Errorf("failed to create kuznyechik provider: %w", err)
		}
		logger.Info("Использование провайдера Kuznyechik (ГОСТ Р 34.12-2015 + ГОСТ Р 34.13-2015)")
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
	s.logger.Info("Инициализация KMS сервера", "keyName", s.config.KeyName, "provider", s.config.EncryptionProvider)

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
				s.logger.Info("KMS сервер инициализирован (Kuznyechik, ключ будет создан при первом использовании)", "keyID", s.keyID)
				return nil
			}
		}

		if !s.config.CreateKeyIfNotExists {
			return fmt.Errorf("key not found and createKeyIfNotExists is false: %w", err)
		}

		// For Transit: create the key
		if transit, ok := s.provider.(*TransitClient); ok {
			s.logger.Info("Создание Transit ключа", "keyName", s.config.KeyName, "keyType", s.config.KeyType)
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
		s.logger.Info("KMS сервер инициализирован успешно", "keyID", s.keyID)
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
	s.logger.Debug("Запрос шифрования", "uid", req.Uid, "plaintextSize", len(req.Plaintext))

	if len(req.Plaintext) == 0 {
		return nil, fmt.Errorf("plaintext cannot be empty")
	}

	// Encrypt using provider
	ciphertext, err := s.provider.Encrypt(ctx, s.config.KeyName, req.Plaintext)
	if err != nil {
		s.logger.Error("Ошибка шифрования", "error", err, "uid", req.Uid)
		return nil, fmt.Errorf("encryption failed: %w", err)
	}

	s.mu.RLock()
	keyID := s.keyID
	s.mu.RUnlock()

	// Create annotations (can be used for additional metadata)
	annotations := map[string][]byte{
		"kubebao.io/key-name": []byte(s.config.KeyName),
	}

	s.logger.Debug("Шифрование выполнено успешно", "uid", req.Uid, "ciphertextSize", len(ciphertext))

	return &v2.EncryptResponse{
		Ciphertext:  []byte(ciphertext),
		KeyId:       keyID,
		Annotations: annotations,
	}, nil
}

// Decrypt decrypts the given ciphertext using the transit secrets engine
func (s *Server) Decrypt(ctx context.Context, req *v2.DecryptRequest) (*v2.DecryptResponse, error) {
	s.logger.Debug("Запрос дешифрования", "uid", req.Uid, "keyId", req.KeyId, "ciphertextSize", len(req.Ciphertext))

	if len(req.Ciphertext) == 0 {
		return nil, fmt.Errorf("ciphertext cannot be empty")
	}

	// Decrypt using provider
	plaintext, err := s.provider.Decrypt(ctx, s.config.KeyName, string(req.Ciphertext))
	if err != nil {
		s.logger.Error("Ошибка дешифрования", "error", err, "uid", req.Uid)
		return nil, fmt.Errorf("decryption failed: %w", err)
	}

	s.logger.Debug("Дешифрование выполнено успешно", "uid", req.Uid, "plaintextSize", len(plaintext))

	return &v2.DecryptResponse{
		Plaintext: plaintext,
	}, nil
}

// Run starts the KMS gRPC server
func (s *Server) Run(ctx context.Context) error {
	socketDir := filepath.Dir(s.config.SocketPath)
	if err := os.MkdirAll(socketDir, 0700); err != nil {
		return fmt.Errorf("failed to create socket directory: %w", err)
	}

	if err := os.Remove(s.config.SocketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove existing socket: %w", err)
	}

	listener, err := net.Listen("unix", s.config.SocketPath)
	if err != nil {
		return fmt.Errorf("failed to create listener: %w", err)
	}
	defer listener.Close()

	if err := os.Chmod(s.config.SocketPath, 0600); err != nil {
		return fmt.Errorf("failed to set socket permissions: %w", err)
	}

	grpcServer := grpc.NewServer(
		grpc.MaxRecvMsgSize(16*1024*1024),
		grpc.MaxSendMsgSize(16*1024*1024),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle: 5 * time.Minute,
			Time:              30 * time.Second,
			Timeout:           10 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	v2.RegisterKeyManagementServiceServer(grpcServer, s)

	s.logger.Info("Запуск KMS сервера", "socket", s.config.SocketPath, "provider", s.config.EncryptionProvider, "keyName", s.config.KeyName)

	// Ожидание отмены контекста для graceful shutdown
	go func() {
		<-ctx.Done()
		s.logger.Info("Остановка KMS сервера")
		grpcServer.GracefulStop()
	}()

	// Периодическая проверка доступности ключа (HealthCheckInterval)
	go s.healthCheckLoop(ctx)

	// Блокирующий вызов до остановки
	if err := grpcServer.Serve(listener); err != nil {
		return fmt.Errorf("gRPC server failed: %w", err)
	}

	return nil
}

// healthCheckLoop — по тикеру вызывает performHealthCheck, при ошибке помечает unhealthy.
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
		s.logger.Warn("Проверка здоровья не пройдена", "error", err)
		s.healthy = false
		return
	}

	// Update key ID if version changed (key rotation)
	newKeyID := fmt.Sprintf("%s:v%d", s.config.KeyName, keyInfo.LatestVersion)
	if newKeyID != s.keyID {
		s.logger.Info("Версия ключа изменилась", "oldKeyID", s.keyID, "newKeyID", newKeyID)
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
