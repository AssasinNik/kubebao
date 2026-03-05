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

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	kubebaoiov1alpha1 "github.com/kubebao/kubebao/api/v1alpha1"
	"github.com/kubebao/kubebao/internal/openbao"
)

const (
	baoSecretFinalizer = "kubebao.io/finalizer"
	defaultRefreshInterval = time.Hour
)

// BaoSecretReconciler reconciles a BaoSecret object
type BaoSecretReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	Log          logr.Logger
	OpenBaoClient *openbao.Client
}

// +kubebuilder:rbac:groups=kubebao.io,resources=baosecrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kubebao.io,resources=baosecrets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kubebao.io,resources=baosecrets/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=serviceaccounts/token,verbs=create

// Reconcile handles the reconciliation loop for BaoSecret
func (r *BaoSecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("baosecret", req.NamespacedName)

	// Fetch the BaoSecret
	baoSecret := &kubebaoiov1alpha1.BaoSecret{}
	if err := r.Get(ctx, req.NamespacedName, baoSecret); err != nil {
		if apierrors.IsNotFound(err) {
			// Object not found, likely deleted
			return ctrl.Result{}, nil
		}
		log.Error(err, "unable to fetch BaoSecret")
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !baoSecret.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, baoSecret)
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(baoSecret, baoSecretFinalizer) {
		controllerutil.AddFinalizer(baoSecret, baoSecretFinalizer)
		if err := r.Update(ctx, baoSecret); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Check if sync is suspended
	if baoSecret.Spec.SuspendSync {
		log.Info("sync is suspended")
		r.setCondition(baoSecret, kubebaoiov1alpha1.ConditionTypeReady, metav1.ConditionFalse, 
			kubebaoiov1alpha1.ReasonSyncSuspended, "Sync is suspended")
		if err := r.Status().Update(ctx, baoSecret); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Sync the secret
	if err := r.syncSecret(ctx, baoSecret); err != nil {
		log.Error(err, "failed to sync secret")
		r.setCondition(baoSecret, kubebaoiov1alpha1.ConditionTypeSynced, metav1.ConditionFalse,
			kubebaoiov1alpha1.ReasonFailed, err.Error())
		r.setCondition(baoSecret, kubebaoiov1alpha1.ConditionTypeReady, metav1.ConditionFalse,
			kubebaoiov1alpha1.ReasonFailed, "Failed to sync secret")
		if err := r.Status().Update(ctx, baoSecret); err != nil {
			return ctrl.Result{}, err
		}
		// Retry after some time
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Update status
	baoSecret.Status.ObservedGeneration = baoSecret.Generation
	now := metav1.Now()
	baoSecret.Status.LastSyncTime = &now
	r.setCondition(baoSecret, kubebaoiov1alpha1.ConditionTypeSynced, metav1.ConditionTrue,
		kubebaoiov1alpha1.ReasonSuccess, "Secret synced successfully")
	r.setCondition(baoSecret, kubebaoiov1alpha1.ConditionTypeReady, metav1.ConditionTrue,
		kubebaoiov1alpha1.ReasonSuccess, "Secret is ready")

	if err := r.Status().Update(ctx, baoSecret); err != nil {
		return ctrl.Result{}, err
	}

	// Schedule next sync
	refreshInterval := r.parseRefreshInterval(baoSecret.Spec.RefreshInterval)
	log.Info("secret synced successfully", "nextSync", refreshInterval)
	
	return ctrl.Result{RequeueAfter: refreshInterval}, nil
}

// syncSecret synchronizes the secret from OpenBao to Kubernetes
func (r *BaoSecretReconciler) syncSecret(ctx context.Context, baoSecret *kubebaoiov1alpha1.BaoSecret) error {
	log := r.Log.WithValues("baosecret", types.NamespacedName{
		Name:      baoSecret.Name,
		Namespace: baoSecret.Namespace,
	})

	// Get OpenBao client (use default or create based on spec)
	baoClient := r.OpenBaoClient
	if baoClient == nil {
		return fmt.Errorf("OpenBao client not configured")
	}

	// Read secret from OpenBao
	secretData, err := baoClient.KVRead(ctx, baoSecret.Spec.SecretPath)
	if err != nil {
		return fmt.Errorf("failed to read secret from OpenBao: %w", err)
	}

	// Extract specific key if specified
	var data map[string][]byte
	if baoSecret.Spec.SecretKey != "" {
		value, ok := secretData[baoSecret.Spec.SecretKey]
		if !ok {
			return fmt.Errorf("key %s not found in secret", baoSecret.Spec.SecretKey)
		}
		data = map[string][]byte{
			baoSecret.Spec.SecretKey: []byte(fmt.Sprintf("%v", value)),
		}
	} else {
		data = make(map[string][]byte)
		for k, v := range secretData {
			data[k] = []byte(fmt.Sprintf("%v", v))
		}
	}

	// Apply template if specified
	if baoSecret.Spec.Template != nil {
		data, err = r.applyTemplate(data, baoSecret.Spec.Template, secretData)
		if err != nil {
			return fmt.Errorf("failed to apply template: %w", err)
		}
	}

	// Determine target namespace
	targetNamespace := baoSecret.Spec.Target.Namespace
	if targetNamespace == "" {
		targetNamespace = baoSecret.Namespace
	}

	// Calculate secret version (hash of data)
	version := r.calculateVersion(data)

	// Create or update the Kubernetes secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      baoSecret.Spec.Target.Name,
			Namespace: targetNamespace,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		// Set labels
		if secret.Labels == nil {
			secret.Labels = make(map[string]string)
		}
		secret.Labels["kubebao.io/managed-by"] = "kubebao-operator"
		secret.Labels["kubebao.io/baosecret"] = baoSecret.Name
		for k, v := range baoSecret.Spec.Target.Labels {
			secret.Labels[k] = v
		}

		// Set annotations
		if secret.Annotations == nil {
			secret.Annotations = make(map[string]string)
		}
		secret.Annotations["kubebao.io/source-path"] = baoSecret.Spec.SecretPath
		secret.Annotations["kubebao.io/version"] = version
		for k, v := range baoSecret.Spec.Target.Annotations {
			secret.Annotations[k] = v
		}

		// Set secret type
		if baoSecret.Spec.Target.Type != "" {
			secret.Type = corev1.SecretType(baoSecret.Spec.Target.Type)
		} else {
			secret.Type = corev1.SecretTypeOpaque
		}

		// Set data
		secret.Data = data

		// Set owner reference based on creation policy
		if baoSecret.Spec.Target.CreationPolicy == "Owner" || baoSecret.Spec.Target.CreationPolicy == "" {
			if targetNamespace == baoSecret.Namespace {
				return controllerutil.SetControllerReference(baoSecret, secret, r.Scheme)
			}
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to create/update secret: %w", err)
	}

	log.Info("secret operation completed", "operation", op, "secret", secret.Name)

	// Update status
	baoSecret.Status.SecretVersion = version
	baoSecret.Status.SyncedSecretName = secret.Name
	baoSecret.Status.SyncedSecretNamespace = secret.Namespace

	return nil
}

// handleDeletion handles the deletion of a BaoSecret
func (r *BaoSecretReconciler) handleDeletion(ctx context.Context, baoSecret *kubebaoiov1alpha1.BaoSecret) (ctrl.Result, error) {
	log := r.Log.WithValues("baosecret", types.NamespacedName{
		Name:      baoSecret.Name,
		Namespace: baoSecret.Namespace,
	})

	if controllerutil.ContainsFinalizer(baoSecret, baoSecretFinalizer) {
		// Clean up owned resources if needed
		// The secret will be garbage collected if owner reference is set

		// If creation policy is Orphan, don't delete the secret
		if baoSecret.Spec.Target.CreationPolicy == "Orphan" {
			log.Info("orphaning managed secret")
		}

		// Remove finalizer
		controllerutil.RemoveFinalizer(baoSecret, baoSecretFinalizer)
		if err := r.Update(ctx, baoSecret); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// applyTemplate applies the template to the secret data
func (r *BaoSecretReconciler) applyTemplate(data map[string][]byte, template *kubebaoiov1alpha1.SecretTemplate, sourceData map[string]interface{}) (map[string][]byte, error) {
	result := make(map[string][]byte)

	// Copy existing data
	for k, v := range data {
		result[k] = v
	}

	// Apply string data templates
	if template.StringData != nil {
		for key, tmpl := range template.StringData {
			// Simple template replacement - in production, use text/template
			value := tmpl
			for k, v := range sourceData {
				placeholder := fmt.Sprintf("{{ .Data.%s }}", k)
				value = replaceAll(value, placeholder, fmt.Sprintf("%v", v))
			}
			result[key] = []byte(value)
		}
	}

	// Apply data templates
	if template.Data != nil {
		for key, tmpl := range template.Data {
			value := tmpl
			for k, v := range sourceData {
				placeholder := fmt.Sprintf("{{ .Data.%s }}", k)
				value = replaceAll(value, placeholder, fmt.Sprintf("%v", v))
			}
			result[key] = []byte(value)
		}
	}

	return result, nil
}

// replaceAll replaces all occurrences of old with new in s
func replaceAll(s, old, new string) string {
	for {
		newS := s
		if idx := indexOf(newS, old); idx >= 0 {
			newS = newS[:idx] + new + newS[idx+len(old):]
		}
		if newS == s {
			break
		}
		s = newS
	}
	return s
}

// indexOf returns the index of substr in s, or -1 if not found
func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// calculateVersion calculates a version hash for the secret data
func (r *BaoSecretReconciler) calculateVersion(data map[string][]byte) string {
	jsonData, _ := json.Marshal(data)
	hash := sha256.Sum256(jsonData)
	return hex.EncodeToString(hash[:8])
}

// parseRefreshInterval parses the refresh interval string
func (r *BaoSecretReconciler) parseRefreshInterval(interval string) time.Duration {
	if interval == "" {
		return defaultRefreshInterval
	}

	d, err := time.ParseDuration(interval)
	if err != nil {
		return defaultRefreshInterval
	}

	// Minimum refresh interval of 1 minute
	if d < time.Minute {
		return time.Minute
	}

	return d
}

// setCondition sets a condition on the BaoSecret status
func (r *BaoSecretReconciler) setCondition(baoSecret *kubebaoiov1alpha1.BaoSecret, condType string, status metav1.ConditionStatus, reason, message string) {
	now := metav1.Now()

	// Find existing condition
	var existingCondition *metav1.Condition
	for i := range baoSecret.Status.Conditions {
		if baoSecret.Status.Conditions[i].Type == condType {
			existingCondition = &baoSecret.Status.Conditions[i]
			break
		}
	}

	if existingCondition != nil {
		if existingCondition.Status != status {
			existingCondition.LastTransitionTime = now
		}
		existingCondition.Status = status
		existingCondition.Reason = reason
		existingCondition.Message = message
	} else {
		baoSecret.Status.Conditions = append(baoSecret.Status.Conditions, metav1.Condition{
			Type:               condType,
			Status:             status,
			LastTransitionTime: now,
			Reason:             reason,
			Message:            message,
		})
	}
}

// SetupWithManager sets up the controller with the Manager
func (r *BaoSecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kubebaoiov1alpha1.BaoSecret{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}
