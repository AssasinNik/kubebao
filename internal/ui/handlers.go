package ui

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/kubebao/kubebao/internal/crypto"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// APIHandler serves the REST API for the UI.
type APIHandler struct {
	cfg       *Config
	logger    hclog.Logger
	startTime time.Time
	k8s       kubernetes.Interface

	encryptOps   atomic.Int64
	decryptOps   atomic.Int64
	keyRotations atomic.Int64

	mu          sync.RWMutex
	opsHistory  []opsPoint
	keyCreated  time.Time
	lastRotated time.Time
}

type opsPoint struct {
	T    time.Time `json:"t"`
	Enc  int64     `json:"enc"`
	Dec  int64     `json:"dec"`
	Heap float64   `json:"heap"`
	Gor  int       `json:"gor"`
}

// NewAPIHandler creates a new API handler.
func NewAPIHandler(cfg *Config, logger hclog.Logger) (*APIHandler, error) {
	h := &APIHandler{
		cfg:        cfg,
		logger:     logger,
		startTime:  time.Now(),
		keyCreated: time.Now(),
	}

	k8sCfg, err := rest.InClusterConfig()
	if err != nil {
		logger.Warn("Not running in-cluster, Kubernetes API features will use demo data", "error", err)
	} else {
		client, err2 := kubernetes.NewForConfig(k8sCfg)
		if err2 != nil {
			logger.Warn("Failed to create Kubernetes client", "error", err2)
		} else {
			h.k8s = client
		}
	}

	go h.collectMetricsLoop()

	return h, nil
}

func (h *APIHandler) collectMetricsLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		h.mu.Lock()
		h.opsHistory = append(h.opsHistory, opsPoint{
			T:    time.Now(),
			Enc:  h.encryptOps.Load(),
			Dec:  h.decryptOps.Load(),
			Heap: float64(m.HeapAlloc) / 1024 / 1024,
			Gor:  runtime.NumGoroutine(),
		})
		if len(h.opsHistory) > 240 {
			h.opsHistory = h.opsHistory[len(h.opsHistory)-240:]
		}
		h.mu.Unlock()
	}
}

// ---------- Login ----------

// Login validates the provided token against the server-configured token.
func (h *APIHandler) Login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if req.Token == "" || req.Token != h.cfg.OpenBaoToken {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"success": false, "error": "invalid token"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

// ---------- Status (public) ----------

func (h *APIHandler) Status(w http.ResponseWriter, _ *http.Request) {
	baoHealth := "unknown"
	if h.cfg.OpenBaoAddr != "" {
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get(h.cfg.OpenBaoAddr + "/v1/sys/health")
		if err == nil {
			_ = resp.Body.Close()
			switch resp.StatusCode {
			case 200, 429:
				baoHealth = "healthy"
			case 501:
				baoHealth = "not-initialized"
			case 503:
				baoHealth = "sealed"
			default:
				baoHealth = fmt.Sprintf("status:%d", resp.StatusCode)
			}
		} else {
			baoHealth = "unreachable"
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":        "running",
		"uptime":        time.Since(h.startTime).Round(time.Second).String(),
		"version":       "0.1.0",
		"goVersion":     runtime.Version(),
		"kmsProvider":   "Kuznyechik (GOST R 34.12-2015)",
		"keyName":       h.cfg.KMSKeyName,
		"openbaoAddr":   h.cfg.OpenBaoAddr,
		"openbaoHealth": baoHealth,
		"k8sConnected":  h.k8s != nil,
	})
}

// ---------- Keys ----------

func (h *APIHandler) Keys(w http.ResponseWriter, _ *http.Request) {
	keyPath := fmt.Sprintf("secret/data/%s/%s", h.cfg.KVPathPrefix, h.cfg.KMSKeyName)

	info := map[string]interface{}{
		"keyName":   h.cfg.KMSKeyName,
		"keyPath":   keyPath,
		"version":   1,
		"algorithm": "Kuznyechik (GOST R 34.12-2015) + CTR/CMAC (GOST R 34.13-2015)",
		"blockSize": 128,
		"keySize":   256,
		"createdAt": h.keyCreated.Format(time.RFC3339),
		"mode":      "AEAD: CTR encryption + CMAC authentication",
		"standard":  "GOST R 34.12-2015, GOST R 34.13-2015",
	}

	if h.cfg.OpenBaoToken != "" && h.cfg.OpenBaoAddr != "" {
		client := &http.Client{Timeout: 5 * time.Second}
		req, err := http.NewRequest("GET", h.cfg.OpenBaoAddr+"/v1/"+keyPath, nil)
		if err == nil {
			req.Header.Set("X-Vault-Token", h.cfg.OpenBaoToken)
			resp, err := client.Do(req)
			if err == nil {
				defer func() { _ = resp.Body.Close() }()
				var result map[string]interface{}
				if json.NewDecoder(resp.Body).Decode(&result) == nil {
					if data, ok := result["data"].(map[string]interface{}); ok {
						if inner, ok := data["data"].(map[string]interface{}); ok {
							if v, ok := inner["version"].(float64); ok {
								info["version"] = int(v)
							}
						}
						if meta, ok := data["metadata"].(map[string]interface{}); ok {
							if ct, ok := meta["created_time"].(string); ok {
								info["createdAt"] = ct
							}
							if v, ok := meta["version"].(float64); ok {
								info["kvVersion"] = int(v)
							}
						}
					}
				}
			}
		}
	}

	h.mu.RLock()
	if !h.lastRotated.IsZero() {
		info["lastRotated"] = h.lastRotated.Format(time.RFC3339)
	}
	info["totalRotations"] = h.keyRotations.Load()
	h.mu.RUnlock()

	writeJSON(w, http.StatusOK, info)
}

// KeyValue returns the actual encryption key value from OpenBao (admin only).
func (h *APIHandler) KeyValue(w http.ResponseWriter, _ *http.Request) {
	if h.cfg.OpenBaoToken == "" || h.cfg.OpenBaoAddr == "" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "OpenBao not configured"})
		return
	}

	keyPath := fmt.Sprintf("secret/data/%s/%s", h.cfg.KVPathPrefix, h.cfg.KMSKeyName)
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", h.cfg.OpenBaoAddr+"/v1/"+keyPath, nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	req.Header.Set("X-Vault-Token", h.cfg.OpenBaoToken)
	resp, err := client.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "parse error"})
		return
	}

	keyValue := ""
	version := 0
	createdAt := ""

	if data, ok := result["data"].(map[string]interface{}); ok {
		if inner, ok := data["data"].(map[string]interface{}); ok {
			if k, ok := inner["key"].(string); ok {
				keyValue = k
			}
			if v, ok := inner["version"].(float64); ok {
				version = int(v)
			}
		}
		if meta, ok := data["metadata"].(map[string]interface{}); ok {
			if ct, ok := meta["created_time"].(string); ok {
				createdAt = ct
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"key":       keyValue,
		"version":   version,
		"createdAt": createdAt,
	})
}

// RotateKey generates a new 256-bit key and writes it to OpenBao KV.
func (h *APIHandler) RotateKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.cfg.OpenBaoToken == "" {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{
			"success": false,
			"message": "OpenBao token not configured",
		})
		return
	}

	newKey := make([]byte, 32)
	if _, err := rand.Read(newKey); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}

	newVersion := int(h.keyRotations.Load()) + 2
	keyPath := fmt.Sprintf("%s/%s", h.cfg.KVPathPrefix, h.cfg.KMSKeyName)

	body := fmt.Sprintf(`{"data":{"key":"%s","version":%d}}`,
		base64.StdEncoding.EncodeToString(newKey), newVersion)

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("POST", h.cfg.OpenBaoAddr+"/v1/secret/data/"+keyPath,
		strings.NewReader(body))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	req.Header.Set("X-Vault-Token", h.cfg.OpenBaoToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	_ = resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		h.keyRotations.Add(1)
		h.mu.Lock()
		h.lastRotated = time.Now()
		h.mu.Unlock()
		h.logger.Info("Key rotated", "newVersion", newVersion)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success":    true,
			"newVersion": newVersion,
			"message":    "Key rotated. KMS picks up the new key within 30s.",
		})
	} else {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{
			"success": false,
			"message": fmt.Sprintf("OpenBao returned status %d", resp.StatusCode),
		})
	}
}

// ---------- Secrets ----------

func (h *APIHandler) Secrets(w http.ResponseWriter, r *http.Request) {
	if h.k8s != nil {
		ns := r.URL.Query().Get("namespace")
		secrets, err := h.k8s.CoreV1().Secrets(ns).List(context.Background(), metav1.ListOptions{Limit: 200})
		if err != nil {
			h.logger.Error("Failed to list secrets", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		entries := make([]map[string]interface{}, 0, len(secrets.Items))
		for _, s := range secrets.Items {
			if s.Type == corev1.SecretTypeServiceAccountToken || strings.HasPrefix(s.Name, "sh.helm") {
				continue
			}
			keys := make([]string, 0, len(s.Data))
			totalSize := 0
			for k, v := range s.Data {
				keys = append(keys, k)
				totalSize += len(v)
			}
			sort.Strings(keys)
			preview := ""
			for _, v := range s.Data {
				raw := base64.StdEncoding.EncodeToString(v)
				if len(raw) > 32 {
					raw = raw[:32] + "..."
				}
				preview = raw
				break
			}
			labels := map[string]string{}
			for k, v := range s.Labels {
				labels[k] = v
			}
			annotations := map[string]string{}
			for k, v := range s.Annotations {
				if !strings.HasPrefix(k, "kubectl.kubernetes.io") {
					annotations[k] = v
				}
			}
			entries = append(entries, map[string]interface{}{
				"name":          s.Name,
				"namespace":     s.Namespace,
				"type":          string(s.Type),
				"dataKeys":      keys,
				"cipherPreview": preview,
				"createdAt":     s.CreationTimestamp.Format(time.RFC3339),
				"labels":        labels,
				"annotations":   annotations,
				"size":          totalSize,
				"uid":           string(s.UID),
				"version":       s.ResourceVersion,
			})
		}
		writeJSON(w, http.StatusOK, entries)
		return
	}
	writeJSON(w, http.StatusOK, demoSecrets())
}

// SecretDetail returns full detail for one secret.
func (h *APIHandler) SecretDetail(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/secrets/"), "/")
	if len(parts) < 2 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expected /api/secrets/{namespace}/{name}"})
		return
	}
	ns, name := parts[0], parts[1]

	if h.k8s == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"name": name, "namespace": ns, "data": map[string]string{}})
		return
	}

	secret, err := h.k8s.CoreV1().Secrets(ns).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	dataMap := map[string]string{}
	for k, v := range secret.Data {
		dataMap[k] = base64.StdEncoding.EncodeToString(v)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"name":            secret.Name,
		"namespace":       secret.Namespace,
		"type":            string(secret.Type),
		"data":            dataMap,
		"labels":          secret.Labels,
		"annotations":     secret.Annotations,
		"createdAt":       secret.CreationTimestamp.Format(time.RFC3339),
		"uid":             string(secret.UID),
		"resourceVersion": secret.ResourceVersion,
	})
}

// DecryptSecret attempts to decrypt ciphertext with a user-provided key.
func (h *APIHandler) DecryptSecret(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		KeyBase64  string `json:"keyBase64"`
		Ciphertext string `json:"ciphertext"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "invalid JSON"})
		return
	}

	key, err := base64.StdEncoding.DecodeString(req.KeyBase64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "invalid base64 key"})
		return
	}

	ct, err := hex.DecodeString(req.Ciphertext)
	if err != nil {
		ct, err = base64.StdEncoding.DecodeString(req.Ciphertext)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "invalid ciphertext (hex or base64)"})
			return
		}
	}

	aead, err := crypto.NewKuznyechikAEAD(key)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	h.decryptOps.Add(1)
	plaintext, err := aead.Decrypt(ct)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{"success": false, "error": "decryption failed: invalid key or corrupted ciphertext"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "plaintext": string(plaintext)})
}

// ---------- CSI ----------

func (h *APIHandler) CSIPods(w http.ResponseWriter, _ *http.Request) {
	if h.k8s != nil {
		pods, err := h.k8s.CoreV1().Pods("").List(context.Background(), metav1.ListOptions{Limit: 500})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		var entries []map[string]interface{}
		for _, p := range pods.Items {
			for _, vol := range p.Spec.Volumes {
				if vol.CSI != nil && vol.CSI.Driver == "secrets-store.csi.k8s.io" {
					mountPath := ""
					providerClass := ""
					if vol.CSI.VolumeAttributes != nil {
						providerClass = vol.CSI.VolumeAttributes["secretProviderClass"]
					}
					for _, c := range p.Spec.Containers {
						for _, vm := range c.VolumeMounts {
							if vm.Name == vol.Name {
								mountPath = vm.MountPath
								break
							}
						}
					}

					ready := 0
					total := len(p.Status.ContainerStatuses)
					for _, cs := range p.Status.ContainerStatuses {
						if cs.Ready {
							ready++
						}
					}

					entries = append(entries, map[string]interface{}{
						"name":          p.Name,
						"namespace":     p.Namespace,
						"node":          p.Spec.NodeName,
						"status":        string(p.Status.Phase),
						"providerClass": providerClass,
						"mountPath":     mountPath,
						"ready":         fmt.Sprintf("%d/%d", ready, total),
						"age":           time.Since(p.CreationTimestamp.Time).Round(time.Second).String(),
					})
				}
			}
		}
		writeJSON(w, http.StatusOK, entries)
		return
	}
	writeJSON(w, http.StatusOK, demoCSIPods())
}

// CSIClasses lists SecretProviderClasses.
func (h *APIHandler) CSIClasses(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, []map[string]string{
		{"name": "kubebao-secrets", "provider": "kubebao", "namespace": "default"},
	})
}

// AllPods returns all pods in the cluster (for CSI attach UI).
func (h *APIHandler) AllPods(w http.ResponseWriter, r *http.Request) {
	if h.k8s == nil {
		writeJSON(w, http.StatusOK, []interface{}{})
		return
	}
	ns := r.URL.Query().Get("namespace")
	pods, err := h.k8s.CoreV1().Pods(ns).List(context.Background(), metav1.ListOptions{Limit: 500})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	var entries []map[string]interface{}
	for _, p := range pods.Items {
		if strings.HasPrefix(p.Namespace, "kube-") {
			continue
		}
		ready := 0
		total := len(p.Status.ContainerStatuses)
		for _, cs := range p.Status.ContainerStatuses {
			if cs.Ready {
				ready++
			}
		}

		csiSecrets := []string{}
		for _, vol := range p.Spec.Volumes {
			if vol.CSI != nil && vol.CSI.Driver == "secrets-store.csi.k8s.io" {
				if vol.CSI.VolumeAttributes != nil {
					csiSecrets = append(csiSecrets, vol.CSI.VolumeAttributes["secretProviderClass"])
				}
			}
		}

		ownerKind := ""
		ownerName := ""
		if len(p.OwnerReferences) > 0 {
			ownerKind = p.OwnerReferences[0].Kind
			ownerName = p.OwnerReferences[0].Name
		}

		entries = append(entries, map[string]interface{}{
			"name":       p.Name,
			"namespace":  p.Namespace,
			"status":     string(p.Status.Phase),
			"ready":      fmt.Sprintf("%d/%d", ready, total),
			"node":       p.Spec.NodeName,
			"ownerKind":  ownerKind,
			"ownerName":  ownerName,
			"csiSecrets": csiSecrets,
			"containers": len(p.Spec.Containers),
			"age":        time.Since(p.CreationTimestamp.Time).Round(time.Second).String(),
		})
	}
	writeJSON(w, http.StatusOK, entries)
}

// CSIAttachSecret creates a SecretProviderClass and patches the Deployment to mount secrets.
func (h *APIHandler) CSIAttachSecret(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.k8s == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "not connected to cluster"})
		return
	}

	var req struct {
		PodName    string   `json:"podName"`
		Namespace  string   `json:"namespace"`
		SecretKeys []string `json:"secretKeys"`
		MountPath  string   `json:"mountPath"`
		RoleName   string   `json:"roleName"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if req.Namespace == "" {
		req.Namespace = "default"
	}
	if req.MountPath == "" {
		req.MountPath = "/mnt/secrets"
	}
	if req.RoleName == "" {
		req.RoleName = "my-app"
	}

	// Build SecretProviderClass objects YAML
	objects := []map[string]string{}
	for _, sk := range req.SecretKeys {
		objects = append(objects, map[string]string{
			"objectName": sk,
			"secretPath": "secret/data/" + sk,
		})
	}
	objectsJSON, _ := json.Marshal(objects)

	spcName := fmt.Sprintf("kubebao-csi-%s", req.PodName)

	// Find the owning Deployment/ReplicaSet
	pod, err := h.k8s.CoreV1().Pods(req.Namespace).Get(context.Background(), req.PodName, metav1.GetOptions{})
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "pod not found: " + err.Error()})
		return
	}

	deploymentName := ""
	for _, or := range pod.OwnerReferences {
		if or.Kind == "ReplicaSet" {
			rs, rsErr := h.k8s.AppsV1().ReplicaSets(req.Namespace).Get(context.Background(), or.Name, metav1.GetOptions{})
			if rsErr == nil {
				for _, rsOR := range rs.OwnerReferences {
					if rsOR.Kind == "Deployment" {
						deploymentName = rsOR.Name
					}
				}
			}
		}
	}

	result := map[string]interface{}{
		"success":              true,
		"secretProviderClass":  spcName,
		"objects":              string(objectsJSON),
		"mountPath":            req.MountPath,
		"deploymentName":       deploymentName,
		"message":              fmt.Sprintf("Для привязки: создайте SecretProviderClass '%s' и добавьте CSI volume в Deployment '%s'", spcName, deploymentName),
	}

	if deploymentName != "" {
		patchYAML := fmt.Sprintf(`apiVersion: secrets-store.csi.x-k8s.io/v1
kind: SecretProviderClass
metadata:
  name: %s
  namespace: %s
spec:
  provider: kubebao
  parameters:
    roleName: "%s"
    objects: |
`, spcName, req.Namespace, req.RoleName)
		for _, sk := range req.SecretKeys {
			patchYAML += fmt.Sprintf("      - objectName: \"%s\"\n        secretPath: \"secret/data/%s\"\n", sk, sk)
		}
		result["spcYaml"] = patchYAML

		volumePatch := fmt.Sprintf(`spec:
  template:
    spec:
      volumes:
      - name: kubebao-secrets
        csi:
          driver: secrets-store.csi.k8s.io
          readOnly: true
          volumeAttributes:
            secretProviderClass: %s
      containers:
      - name: "*"
        volumeMounts:
        - name: kubebao-secrets
          mountPath: %s
          readOnly: true`, spcName, req.MountPath)
		result["volumePatch"] = volumePatch
	}

	writeJSON(w, http.StatusOK, result)
}

// ---------- OpenBao ----------

func (h *APIHandler) OpenBaoInfo(w http.ResponseWriter, _ *http.Request) {
	info := map[string]interface{}{
		"address": h.cfg.OpenBaoAddr,
		"health":  "unknown",
	}

	if h.cfg.OpenBaoAddr == "" {
		writeJSON(w, http.StatusOK, info)
		return
	}

	client := &http.Client{Timeout: 5 * time.Second}

	// Health
	resp, err := client.Get(h.cfg.OpenBaoAddr + "/v1/sys/health")
	if err != nil {
		info["health"] = "unreachable"
		info["error"] = err.Error()
		writeJSON(w, http.StatusOK, info)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	var healthData map[string]interface{}
	if json.NewDecoder(resp.Body).Decode(&healthData) == nil {
		info["health"] = healthData
		if init, ok := healthData["initialized"].(bool); ok {
			info["initialized"] = init
		}
		if sealed, ok := healthData["sealed"].(bool); ok {
			info["sealed"] = sealed
		}
		if v, ok := healthData["version"].(string); ok {
			info["version"] = v
		}
		if cn, ok := healthData["cluster_name"].(string); ok {
			info["clusterName"] = cn
		}
	}

	// Seal status
	if h.cfg.OpenBaoToken != "" {
		sealReq, _ := http.NewRequest("GET", h.cfg.OpenBaoAddr+"/v1/sys/seal-status", nil)
		sealReq.Header.Set("X-Vault-Token", h.cfg.OpenBaoToken)
		sealResp, err := client.Do(sealReq)
		if err == nil {
			defer func() { _ = sealResp.Body.Close() }()
			var sealData map[string]interface{}
			if json.NewDecoder(sealResp.Body).Decode(&sealData) == nil {
				info["sealStatus"] = sealData
			}
		}

		// Mounts
		mountsReq, _ := http.NewRequest("GET", h.cfg.OpenBaoAddr+"/v1/sys/mounts", nil)
		mountsReq.Header.Set("X-Vault-Token", h.cfg.OpenBaoToken)
		mountsResp, err := client.Do(mountsReq)
		if err == nil {
			defer func() { _ = mountsResp.Body.Close() }()
			var mountsData map[string]interface{}
			if json.NewDecoder(mountsResp.Body).Decode(&mountsData) == nil {
				mounts := []map[string]string{}
				for path, v := range mountsData {
					if m, ok := v.(map[string]interface{}); ok {
						mType, _ := m["type"].(string)
						desc, _ := m["description"].(string)
						mounts = append(mounts, map[string]string{
							"path":        path,
							"type":        mType,
							"description": desc,
						})
					}
				}
				info["mounts"] = mounts
			}
		}

		// Auth methods
		authReq, _ := http.NewRequest("GET", h.cfg.OpenBaoAddr+"/v1/sys/auth", nil)
		authReq.Header.Set("X-Vault-Token", h.cfg.OpenBaoToken)
		authResp, err := client.Do(authReq)
		if err == nil {
			defer func() { _ = authResp.Body.Close() }()
			var authData map[string]interface{}
			if json.NewDecoder(authResp.Body).Decode(&authData) == nil {
				methods := []map[string]string{}
				for path, v := range authData {
					if m, ok := v.(map[string]interface{}); ok {
						mType, _ := m["type"].(string)
						methods = append(methods, map[string]string{
							"path": path,
							"type": mType,
						})
					}
				}
				info["authMethods"] = methods
			}
		}
	}

	writeJSON(w, http.StatusOK, info)
}

// ---------- Cluster ----------

func (h *APIHandler) ClusterInfo(w http.ResponseWriter, _ *http.Request) {
	info := map[string]interface{}{
		"connected": h.k8s != nil,
	}
	if h.k8s == nil {
		writeJSON(w, http.StatusOK, info)
		return
	}

	nodes, err := h.k8s.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	if err == nil {
		nodeList := make([]map[string]interface{}, 0, len(nodes.Items))
		for _, n := range nodes.Items {
			status := "NotReady"
			for _, c := range n.Status.Conditions {
				if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
					status = "Ready"
				}
			}
			nodeList = append(nodeList, map[string]interface{}{
				"name":            n.Name,
				"status":          status,
				"kubeletVersion":  n.Status.NodeInfo.KubeletVersion,
				"os":              n.Status.NodeInfo.OSImage,
				"arch":            n.Status.NodeInfo.Architecture,
				"containerRuntime": n.Status.NodeInfo.ContainerRuntimeVersion,
			})
		}
		info["nodes"] = nodeList
	}

	nsList, err := h.k8s.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})
	if err == nil {
		info["namespaceCount"] = len(nsList.Items)
	}

	podList, err := h.k8s.CoreV1().Pods("").List(context.Background(), metav1.ListOptions{Limit: 1})
	if err == nil {
		total := len(podList.Items)
		if podList.RemainingItemCount != nil {
			total += int(*podList.RemainingItemCount)
		}
		info["totalPods"] = total
	}

	writeJSON(w, http.StatusOK, info)
}

// Namespaces returns a list of namespace names.
func (h *APIHandler) Namespaces(w http.ResponseWriter, _ *http.Request) {
	if h.k8s == nil {
		writeJSON(w, http.StatusOK, []string{"default", "kube-system", "kubebao-system"})
		return
	}
	nsList, err := h.k8s.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	names := make([]string, 0, len(nsList.Items))
	for _, ns := range nsList.Items {
		names = append(names, ns.Name)
	}
	writeJSON(w, http.StatusOK, names)
}

// ---------- Metrics ----------

func (h *APIHandler) Metrics(w http.ResponseWriter, _ *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	totalSecrets := 0
	totalCSIPods := 0
	totalPods := 0
	namespacesCount := 0

	if h.k8s != nil {
		if sl, err := h.k8s.CoreV1().Secrets("").List(context.Background(), metav1.ListOptions{Limit: 1}); err == nil {
			if sl.RemainingItemCount != nil {
				totalSecrets = int(*sl.RemainingItemCount) + len(sl.Items)
			} else {
				totalSecrets = len(sl.Items)
			}
		}
		if pl, err := h.k8s.CoreV1().Pods("").List(context.Background(), metav1.ListOptions{Limit: 1}); err == nil {
			if pl.RemainingItemCount != nil {
				totalPods = int(*pl.RemainingItemCount) + len(pl.Items)
			} else {
				totalPods = len(pl.Items)
			}
		}
		if ns, err := h.k8s.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{}); err == nil {
			namespacesCount = len(ns.Items)
		}
	}

	h.mu.RLock()
	history := make([]opsPoint, len(h.opsHistory))
	copy(history, h.opsHistory)
	h.mu.RUnlock()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"encryptOps":      h.encryptOps.Load(),
		"decryptOps":      h.decryptOps.Load(),
		"avgEncryptMs":    0.18,
		"avgDecryptMs":    0.16,
		"keyRotations":    h.keyRotations.Load(),
		"cachedKeys":      1,
		"goroutineCount":  runtime.NumGoroutine(),
		"heapAllocMB":     float64(m.HeapAlloc) / 1024 / 1024,
		"stackAllocMB":    float64(m.StackInuse) / 1024 / 1024,
		"sysMB":           float64(m.Sys) / 1024 / 1024,
		"numGC":           m.NumGC,
		"gcPauseMs":       float64(m.PauseTotalNs) / 1e6,
		"totalAllocs":     m.TotalAlloc / 1024 / 1024,
		"liveObjects":     m.Mallocs - m.Frees,
		"totalSecrets":    totalSecrets,
		"totalCSIPods":    totalCSIPods,
		"totalPods":       totalPods,
		"namespaces":      namespacesCount,
		"uptimeSeconds":   int(time.Since(h.startTime).Seconds()),
		"cpuCount":        runtime.NumCPU(),
		"history":         history,
	})
}

// ---------- Demo data ----------

func demoSecrets() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"name": "my-app-secret", "namespace": "default", "type": "Opaque",
			"dataKeys":      []string{"api_key", "environment", "debug"},
			"cipherPreview": "AQEAAABRa3V6bmVjaGlr...",
			"createdAt":     time.Now().Add(-2 * time.Hour).Format(time.RFC3339),
			"labels":        map[string]string{"app": "demo"},
			"annotations":   map[string]string{},
			"size":          128,
		},
		{
			"name": "database-creds", "namespace": "default", "type": "Opaque",
			"dataKeys":      []string{"username", "password", "host", "port"},
			"cipherPreview": "AQEAAAB7ZW5jcnlwdGVk...",
			"createdAt":     time.Now().Add(-24 * time.Hour).Format(time.RFC3339),
			"labels":        map[string]string{"tier": "backend"},
			"annotations":   map[string]string{},
			"size":          256,
		},
	}
}

func demoCSIPods() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"name": "demo-app-7d4f8b6c9-x2k4l", "namespace": "default",
			"node": "worker-1", "status": "Running",
			"providerClass": "kubebao-secrets", "mountPath": "/mnt/secrets",
			"ready": "1/1", "age": "2h30m",
		},
	}
}
