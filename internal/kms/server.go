// Package kms реализует плагин Kubernetes KMS v2 для kube-apiserver.
//
// Поток данных (упрощённо):
//   1) apiserver подключается по Unix-сокету к gRPC-сервису KeyManagementService.
//   2) Status — частые проверки здоровья и согласования keyID; при смене версии ключа
//      apiserver сбрасывает кеш DEK и снова вызывает Encrypt.
//   3) Encrypt — оборачивает (wrap) новый DEK мастер-ключом провайдера; вызывается редко.
//   4) Decrypt — разворачивает (unwrap) DEK для старых записей в etcd.
//
// Зависимость google.golang.org/grpc должна быть не ниже v1.79.3 (исправление GO-2026-4762:
// обход авторизации при некорректном :path без ведущего слэша).
package kms

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
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
//
// KMS v2 контракт (из kubernetes/kms reference):
//   - Status вызывается часто (apiserver проверяет здоровье + keyID)
//   - Encrypt вызывается при генерации/ротации DEK (редко: при старте + при смене keyID)
//   - Decrypt вызывается при чтении секрета из etcd с устаревшим DEK
//
// apiserver кеширует DEK, пока keyID в Status не изменится.
type Server struct {
	v2.UnimplementedKeyManagementServiceServer

	config   *Config   // Нормализованная конфигурация (сокет, ключ, провайдер, OpenBao).
	provider EncryptionProvider // Реализация шифрования: TransitClient или KuznyechikProvider.
	logger   hclog.Logger       // Структурированные логи (hashicorp go-hclog).
	mu       sync.RWMutex       // Защита keyID и флага healthy от гонок с healthCheckLoop.
	keyID    string             // Строка вида name:vN, отдаётся apiserver в Status/Encrypt.
	healthy  bool               // Итог последней проверки провайдера; влияет на поле Healthz в Status.

	encryptCount atomic.Int64 // Счётчик вызовов Encrypt (для сводки и диагностики).
	decryptCount atomic.Int64 // Счётчик вызовов Decrypt.
	statusCount  atomic.Int64 // Счётчик вызовов Status (ожидаемо большой).
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

	// Инициализация: проверка доступности OpenBao, при необходимости создание ключа (Transit)
	// или установка placeholder keyID для Kuznyechik до первого Encrypt.
	if err := server.initialize(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to initialize server: %w", err)
	}

	return server, nil
}

// newKuznyechikProviderFromConfig собирает цепочку OpenBao-клиент → KeyManager → KuznyechikProvider.
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

// initialize выполняет однократную подготовку: узнаёт или создаёт ключ, выставляет keyID и healthy.
func (s *Server) initialize(ctx context.Context) error {
	s.logger.Info("Инициализация KMS сервера", "keyName", s.config.KeyName, "provider", s.config.EncryptionProvider)

	keyInfo, err := s.provider.GetKeyInfo(ctx, s.config.KeyName)
	if err != nil {
		if s.config.EncryptionProvider == ProviderKuznyechik {
			// Для Kuznyechik запись в KV появляется при первом Encrypt — GetKeyInfo до этого пустой.
			// Разрешаем «мягкий» старт: помечаем здоровым и задаём предварительный keyID.
			if s.config.CreateKeyIfNotExists {
				// Фактическая версия появится после первого GetOrCreateKey в Encrypt.
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

		// Для Transit явно создаём ключ в движке transit, затем перечитываем метаданные.
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

// Status — обязательный метод KMS v2: версия API, health и текущий keyID.
// apiserver опрашивает часто; смена keyID заставляет сбросить кеш DEK и вызвать Encrypt.
func (s *Server) Status(ctx context.Context, req *v2.StatusRequest) (*v2.StatusResponse, error) {
	s.statusCount.Add(1)

	s.mu.RLock()
	defer s.mu.RUnlock()

	healthStatus := HealthStatusOK
	if !s.healthy {
		healthStatus = "unhealthy"
	}

	s.logger.Debug("KMS Status",
		"keyID", s.keyID,
		"healthy", s.healthy,
		"totalEncrypt", s.encryptCount.Load(),
		"totalDecrypt", s.decryptCount.Load(),
	)

	return &v2.StatusResponse{
		Version: APIVersion,
		Healthz: healthStatus,
		KeyId:   s.keyID,
	}, nil
}

// Encrypt шифрует plaintext (обычно это DEK apiserver) выбранным провайдером.
// В KMS v2 вызовы редкие: после Encrypt apiserver кеширует DEK и шифрует Secret локально.
func (s *Server) Encrypt(ctx context.Context, req *v2.EncryptRequest) (*v2.EncryptResponse, error) {
	n := s.encryptCount.Add(1)
	s.logger.Info("KMS Encrypt запрос (обёртка DEK для apiserver)",
		"uid", req.Uid,
		"plaintextSize", len(req.Plaintext),
		"encryptCallN", n,
		"algorithm", "Кузнечик (ГОСТ Р 34.12-2015)",
		"mode", "CTR+CMAC (ГОСТ Р 34.13-2015)",
	)

	if len(req.Plaintext) == 0 {
		return nil, fmt.Errorf("plaintext cannot be empty")
	}

	start := time.Now()
	ciphertext, err := s.provider.Encrypt(ctx, s.config.KeyName, req.Plaintext)
	elapsed := time.Since(start)
	if err != nil {
		s.logger.Error("Ошибка шифрования", "error", err, "uid", req.Uid)
		return nil, fmt.Errorf("encryption failed: %w", err)
	}

	s.mu.RLock()
	keyID := s.keyID
	s.mu.RUnlock()

	annotations := map[string][]byte{
		"kms-key.kubebao.io": []byte(s.config.KeyName),
	}

	s.logger.Info("KMS Encrypt выполнен",
		"uid", req.Uid,
		"keyID", keyID,
		"plaintextSize", len(req.Plaintext),
		"ciphertextSize", len(ciphertext),
		"duration", elapsed,
		"totalEncryptCalls", n,
	)

	return &v2.EncryptResponse{
		Ciphertext:  []byte(ciphertext),
		KeyId:       keyID,
		Annotations: annotations,
	}, nil
}

// Decrypt восстанавливает plaintext (DEK) из ciphertext для записей с устаревшим или иным keyID.
func (s *Server) Decrypt(ctx context.Context, req *v2.DecryptRequest) (*v2.DecryptResponse, error) {
	n := s.decryptCount.Add(1)
	s.logger.Info("KMS Decrypt запрос",
		"uid", req.Uid,
		"keyId", req.KeyId,
		"ciphertextSize", len(req.Ciphertext),
		"decryptCallN", n,
	)

	if len(req.Ciphertext) == 0 {
		return nil, fmt.Errorf("ciphertext cannot be empty")
	}

	start := time.Now()
	plaintext, err := s.provider.Decrypt(ctx, s.config.KeyName, string(req.Ciphertext))
	elapsed := time.Since(start)
	if err != nil {
		s.logger.Error("Ошибка дешифрования", "error", err, "uid", req.Uid)
		return nil, fmt.Errorf("decryption failed: %w", err)
	}

	s.logger.Info("KMS Decrypt выполнен",
		"uid", req.Uid,
		"keyId", req.KeyId,
		"plaintextSize", len(plaintext),
		"duration", elapsed,
		"totalDecryptCalls", n,
	)

	return &v2.DecryptResponse{
		Plaintext: plaintext,
	}, nil
}

// Run поднимает Unix-listener, регистрирует gRPC-сервис и блокируется на Serve до остановки.
// Контекст ctx отменяется при shutdown: graceful stop gRPC, закрытие слушателя через defer.
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
	defer func() { _ = listener.Close() }()

	if err := os.Chmod(s.config.SocketPath, 0666); err != nil {
		return fmt.Errorf("failed to set socket permissions: %w", err)
	}

	// Параметры сообщений: apiserver может передавать крупные DEK-пакеты при пиковых нагрузках.
	grpcServer := grpc.NewServer(
		grpc.MaxRecvMsgSize(16*1024*1024),
		grpc.MaxSendMsgSize(16*1024*1024),
		grpc.UnaryInterceptor(s.unaryInterceptor),
		// Keepalive согласует разрыв «зависших» соединений и совместимость с клиентом apiserver.
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

	go func() {
		<-ctx.Done()
		s.logger.Info("Остановка KMS сервера")
		grpcServer.GracefulStop()
	}()

	go s.healthCheckLoop(ctx)
	go s.statsReportLoop(ctx)

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

// performHealthCheck синхронизирует keyID с OpenBao и сбрасывает healthy при ошибках (кроме Kuznyechik до первого ключа).
func (s *Server) performHealthCheck(ctx context.Context) {
	keyInfo, err := s.provider.GetKeyInfo(ctx, s.config.KeyName)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err != nil {
		// Пока ключа нет в KV, GetKeyInfo даёт ошибку — для уже «зелёного» Kuznyechik не деградируем.
		if s.config.EncryptionProvider == ProviderKuznyechik && s.healthy {
			return
		}
		s.logger.Warn("Проверка здоровья не пройдена", "error", err)
		s.healthy = false
		return
	}

	// Ротация в OpenBao/Transit увеличивает LatestVersion — apiserver увидит новый keyID через Status.
	newKeyID := fmt.Sprintf("%s:v%d", s.config.KeyName, keyInfo.LatestVersion)
	if newKeyID != s.keyID {
		s.logger.Info("Версия ключа изменилась", "oldKeyID", s.keyID, "newKeyID", newKeyID)
		s.keyID = newKeyID
	}

	s.healthy = true
}

// GetKeyID возвращает строковый идентификатор ключа для внешних наблюдателей (тесты, метрики).
func (s *Server) GetKeyID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.keyID
}

// IsHealthy отражает результат последних проверок провайдера и инициализации.
func (s *Server) IsHealthy() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.healthy
}

// unaryInterceptor логирует все gRPC вызовы (паттерн из Trousseau).
// Для Status — только debug (вызывается часто), для Encrypt/Decrypt — info.
func (s *Server) unaryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	start := time.Now()
	resp, err := handler(ctx, req)
	elapsed := time.Since(start)

	if err != nil {
		s.logger.Error("gRPC ошибка", "method", info.FullMethod, "duration", elapsed, "error", err)
	}

	return resp, err
}

// statsReportLoop выводит сводку операций каждые 60 секунд.
func (s *Server) statsReportLoop(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.RLock()
			keyID := s.keyID
			healthy := s.healthy
			s.mu.RUnlock()

			s.logger.Info("KMS сводка операций",
				"keyID", keyID,
				"healthy", healthy,
				"totalEncrypt", s.encryptCount.Load(),
				"totalDecrypt", s.decryptCount.Load(),
				"totalStatus", s.statusCount.Load(),
			)
		}
	}
}
