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

package csi

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/hashicorp/go-hclog"
	pb "github.com/kubebao/kubebao/internal/csi/proto"
	"google.golang.org/grpc"
	"gopkg.in/yaml.v3"
)

const (
	// ProviderName is the name of the CSI provider
	ProviderName = "kubebao"
)

// Provider implements the CSI secrets store provider interface
type Provider struct {
	pb.UnimplementedCSIDriverProviderServer
	config         *Config
	secretsFetcher *SecretsFetcher
	logger         hclog.Logger
	server         *grpc.Server
}

// NewProvider creates a new CSI provider
func NewProvider(config *Config, logger hclog.Logger) (*Provider, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	if logger == nil {
		logger = hclog.NewNullLogger()
	}

	fetcher, err := NewSecretsFetcher(config, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create secrets fetcher: %w", err)
	}

	return &Provider{
		config:         config,
		secretsFetcher: fetcher,
		logger:         logger,
	}, nil
}

// MountParams holds the parsed mount parameters from SecretProviderClass
type MountParams struct {
	RoleName       string         `yaml:"roleName" json:"roleName"`
	OpenBaoAddress string         `yaml:"openbaoAddr" json:"openbaoAddr"`
	AuthMethod     string         `yaml:"authMethod" json:"authMethod"`
	AuthMountPath  string         `yaml:"authMountPath" json:"authMountPath"`
	Namespace      string         `yaml:"namespace" json:"namespace"`
	Objects        []SecretObject `yaml:"objects" json:"objects"`
	Audience       string         `yaml:"audience" json:"audience"`
}

// SecretObject represents a secret to be fetched
type SecretObject struct {
	ObjectName     string            `yaml:"objectName" json:"objectName"`
	SecretPath     string            `yaml:"secretPath" json:"secretPath"`
	SecretKey      string            `yaml:"secretKey" json:"secretKey"`
	SecretArgs     map[string]string `yaml:"secretArgs" json:"secretArgs"`
	Encoding       string            `yaml:"encoding" json:"encoding"`
	FilePermission string            `yaml:"filePermission" json:"filePermission"`
}

// FetchedSecret represents a fetched secret
type FetchedSecret struct {
	ObjectName string
	Content    []byte
	Version    string
	Mode       int32
}

// Version implements CSIDriverProviderServer
func (p *Provider) Version(ctx context.Context, req *pb.VersionRequest) (*pb.VersionResponse, error) {
	p.logger.Debug("version request received", "clientVersion", req.GetVersion())
	return &pb.VersionResponse{
		Version:        "v1alpha1",
		RuntimeName:    ProviderName,
		RuntimeVersion: "0.1.0",
	}, nil
}

// Mount implements CSIDriverProviderServer
func (p *Provider) Mount(ctx context.Context, req *pb.MountRequest) (*pb.MountResponse, error) {
	p.logger.Info("mount request received", "targetPath", req.GetTargetPath())

	// Parse attributes (SecretProviderClass parameters)
	var attribs map[string]string
	if req.GetAttributes() != "" {
		if err := json.Unmarshal([]byte(req.GetAttributes()), &attribs); err != nil {
			p.logger.Error("failed to parse attributes", "error", err)
			return &pb.MountResponse{
				Error: &pb.Error{Code: "InvalidArgument"},
			}, nil
		}
	}

	// Parse mount parameters
	params, err := p.parseMountParams(attribs)
	if err != nil {
		p.logger.Error("failed to parse mount parameters", "error", err)
		return &pb.MountResponse{
			Error: &pb.Error{Code: "InvalidArgument"},
		}, nil
	}

	// Parse secrets (pod secrets including service account token)
	var secrets map[string]string
	if req.GetSecrets() != "" {
		if err := json.Unmarshal([]byte(req.GetSecrets()), &secrets); err != nil {
			p.logger.Warn("failed to parse secrets", "error", err)
		}
	}

	// Authenticate to OpenBao
	authClient, err := p.authenticate(ctx, params, secrets)
	if err != nil {
		p.logger.Error("authentication failed", "error", err)
		return &pb.MountResponse{
			Error: &pb.Error{Code: "PermissionDenied"},
		}, nil
	}

	// Fetch secrets from OpenBao
	fetchedSecrets, err := p.secretsFetcher.FetchSecrets(ctx, authClient, params.Objects)
	if err != nil {
		p.logger.Error("failed to fetch secrets", "error", err)
		return &pb.MountResponse{
			Error: &pb.Error{Code: "Internal"},
		}, nil
	}

	// Build response
	var files []*pb.File
	var objectVersions []*pb.ObjectVersion

	for _, secret := range fetchedSecrets {
		files = append(files, &pb.File{
			Path:     secret.ObjectName,
			Mode:     secret.Mode,
			Contents: secret.Content,
		})

		objectVersions = append(objectVersions, &pb.ObjectVersion{
			Id:      secret.ObjectName,
			Version: secret.Version,
		})
	}

	p.logger.Info("mount successful", "filesCount", len(files))

	return &pb.MountResponse{
		ObjectVersion: objectVersions,
		Files:         files,
	}, nil
}

// parseMountParams parses the mount request parameters
func (p *Provider) parseMountParams(attribs map[string]string) (*MountParams, error) {
	params := &MountParams{
		AuthMethod:    p.config.DefaultAuthMethod,
		AuthMountPath: "kubernetes",
		RoleName:      p.config.DefaultRole,
	}

	if attribs == nil {
		return nil, fmt.Errorf("attributes are required")
	}

	// Parse standard attributes
	if roleName, ok := attribs["roleName"]; ok {
		params.RoleName = roleName
	}

	if addr, ok := attribs["openbaoAddr"]; ok {
		params.OpenBaoAddress = addr
	}

	if authMethod, ok := attribs["authMethod"]; ok {
		params.AuthMethod = authMethod
	}

	if mountPath, ok := attribs["authMountPath"]; ok {
		params.AuthMountPath = mountPath
	}

	if namespace, ok := attribs["namespace"]; ok {
		params.Namespace = namespace
	}

	if audience, ok := attribs["audience"]; ok {
		params.Audience = audience
	}

	// Parse objects list
	if objectsStr, ok := attribs["objects"]; ok {
		var objects []SecretObject
		// Try YAML first (more common in SecretProviderClass)
		if err := yaml.Unmarshal([]byte(objectsStr), &objects); err != nil {
			// Try JSON
			if err := json.Unmarshal([]byte(objectsStr), &objects); err != nil {
				return nil, fmt.Errorf("failed to parse objects: %w", err)
			}
		}
		params.Objects = objects
	}

	// Validate
	if params.RoleName == "" {
		return nil, fmt.Errorf("roleName is required")
	}

	if len(params.Objects) == 0 {
		return nil, fmt.Errorf("objects list cannot be empty")
	}

	return params, nil
}

// authenticate performs authentication to OpenBao
func (p *Provider) authenticate(ctx context.Context, params *MountParams, secrets map[string]string) (*AuthenticatedClient, error) {
	authConfig := &AuthConfig{
		OpenBaoAddress: params.OpenBaoAddress,
		AuthMethod:     params.AuthMethod,
		AuthMountPath:  params.AuthMountPath,
		Role:           params.RoleName,
		Namespace:      params.Namespace,
		Audience:       params.Audience,
	}

	// If no address specified, use default from config
	if authConfig.OpenBaoAddress == "" {
		authConfig.OpenBaoAddress = p.config.OpenBao.Address
	}

	// Get service account token from secrets
	if secrets != nil {
		if saTokensStr, ok := secrets["csi.storage.k8s.io/serviceAccount.tokens"]; ok {
			authConfig.ServiceAccountToken = saTokensStr
		}
	}

	// Try to read token from default location if not provided
	if authConfig.ServiceAccountToken == "" {
		tokenPath := "/var/run/secrets/kubernetes.io/serviceaccount/token"
		if token, err := os.ReadFile(tokenPath); err == nil {
			authConfig.ServiceAccountToken = string(token)
		}
	}

	return NewAuthenticatedClient(ctx, authConfig, p.logger)
}

// Run starts the CSI provider gRPC server
func (p *Provider) Run(ctx context.Context) error {
	// Ensure socket directory exists
	socketDir := filepath.Dir(p.config.SocketPath)
	if err := os.MkdirAll(socketDir, 0755); err != nil {
		return fmt.Errorf("failed to create socket directory: %w", err)
	}

	// Remove existing socket file
	if err := os.Remove(p.config.SocketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove existing socket: %w", err)
	}

	// Create Unix socket listener
	listener, err := net.Listen("unix", p.config.SocketPath)
	if err != nil {
		return fmt.Errorf("failed to create listener: %w", err)
	}
	defer listener.Close()

	// Set socket permissions (allow CSI driver to connect)
	if err := os.Chmod(p.config.SocketPath, 0660); err != nil {
		return fmt.Errorf("failed to set socket permissions: %w", err)
	}

	// Create gRPC server
	p.server = grpc.NewServer()

	// Register CSI provider service
	pb.RegisterCSIDriverProviderServer(p.server, p)

	p.logger.Info("CSI provider starting", "socket", p.config.SocketPath)

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	errChan := make(chan error, 1)
	go func() {
		if err := p.server.Serve(listener); err != nil {
			errChan <- fmt.Errorf("gRPC server failed: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		p.logger.Info("shutting down CSI provider (context)")
		p.server.GracefulStop()
	case sig := <-sigChan:
		p.logger.Info("shutting down CSI provider", "signal", sig)
		p.server.GracefulStop()
	case err := <-errChan:
		return err
	}

	return nil
}

// parseFilePermission parses file permission string to int32
func parseFilePermission(perm string) int32 {
	if perm == "" {
		return 0644
	}

	// Remove leading zero if present
	perm = strings.TrimPrefix(perm, "0")

	var mode int32
	fmt.Sscanf(perm, "%o", &mode)
	if mode == 0 {
		return 0644
	}
	return mode
}
