# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2024-XX-XX

### Added
- Initial release
- KubeBao Operator with BaoSecret and BaoPolicy CRDs
- KMS Plugin for etcd encryption via OpenBao Transit
- CSI Provider for direct secret injection into pods
- Helm chart for easy deployment
- Automatic secret synchronization from OpenBao to Kubernetes
- Dynamic secret updates without pod restarts
- Kubernetes authentication support
- Comprehensive E2E test suite
- Full documentation

### Components
- `kubebao-operator` - Kubernetes operator for managing secrets
- `kubebao-kms` - KMS plugin for etcd encryption
- `kubebao-csi` - CSI provider for secret injection

### CRDs
- `BaoSecret` - Declarative secret synchronization
- `BaoPolicy` - Declarative policy management

[Unreleased]: https://github.com/kubebao/kubebao/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/kubebao/kubebao/releases/tag/v0.1.0
