package ui

// Config holds the UI server configuration.
type Config struct {
	ListenAddr    string
	OpenBaoAddr   string
	OpenBaoToken  string
	KMSKeyName    string
	KVPathPrefix  string
	KubeNamespace string
}
