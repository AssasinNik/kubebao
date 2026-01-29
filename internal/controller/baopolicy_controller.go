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
	"fmt"
	"time"

	"github.com/go-logr/logr"
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
	baoPolicyFinalizer = "kubebao.io/policy-finalizer"
)

// BaoPolicyReconciler reconciles a BaoPolicy object
type BaoPolicyReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	Log           logr.Logger
	OpenBaoClient *openbao.Client
}

// +kubebuilder:rbac:groups=kubebao.io,resources=baopolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kubebao.io,resources=baopolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kubebao.io,resources=baopolicies/finalizers,verbs=update

// Reconcile handles the reconciliation loop for BaoPolicy
func (r *BaoPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("baopolicy", req.NamespacedName)

	// Fetch the BaoPolicy
	baoPolicy := &kubebaoiov1alpha1.BaoPolicy{}
	if err := r.Get(ctx, req.NamespacedName, baoPolicy); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "unable to fetch BaoPolicy")
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !baoPolicy.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, baoPolicy)
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(baoPolicy, baoPolicyFinalizer) {
		controllerutil.AddFinalizer(baoPolicy, baoPolicyFinalizer)
		if err := r.Update(ctx, baoPolicy); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Sync the policy
	if err := r.syncPolicy(ctx, baoPolicy); err != nil {
		log.Error(err, "failed to sync policy")
		r.setCondition(baoPolicy, kubebaoiov1alpha1.ConditionTypeReady, metav1.ConditionFalse,
			kubebaoiov1alpha1.ReasonFailed, err.Error())
		if err := r.Status().Update(ctx, baoPolicy); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Update status
	baoPolicy.Status.ObservedGeneration = baoPolicy.Generation
	now := metav1.Now()
	baoPolicy.Status.LastSyncTime = &now
	r.setCondition(baoPolicy, kubebaoiov1alpha1.ConditionTypeReady, metav1.ConditionTrue,
		kubebaoiov1alpha1.ReasonSuccess, "Policy synced successfully")

	if err := r.Status().Update(ctx, baoPolicy); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("policy synced successfully")
	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

// syncPolicy synchronizes the policy to OpenBao
func (r *BaoPolicyReconciler) syncPolicy(ctx context.Context, baoPolicy *kubebaoiov1alpha1.BaoPolicy) error {
	log := r.Log.WithValues("baopolicy", types.NamespacedName{
		Name:      baoPolicy.Name,
		Namespace: baoPolicy.Namespace,
	})

	// Get OpenBao client
	baoClient := r.OpenBaoClient
	if baoClient == nil {
		return fmt.Errorf("OpenBao client not configured")
	}

	// Generate policy HCL
	policyHCL := baoPolicy.ToHCL()
	policyName := baoPolicy.GetPolicyName()

	// Calculate policy version (hash)
	hash := sha256.Sum256([]byte(policyHCL))
	version := hex.EncodeToString(hash[:8])

	// Check if policy needs update
	if baoPolicy.Status.PolicyVersion == version {
		log.V(1).Info("policy unchanged, skipping update")
		return nil
	}

	// Write policy to OpenBao
	path := fmt.Sprintf("sys/policies/acl/%s", policyName)
	data := map[string]interface{}{
		"policy": policyHCL,
	}

	_, err := baoClient.WriteSecret(ctx, path, data)
	if err != nil {
		return fmt.Errorf("failed to write policy to OpenBao: %w", err)
	}

	log.Info("policy written to OpenBao", "policyName", policyName)

	// Update status
	baoPolicy.Status.PolicyVersion = version
	baoPolicy.Status.AppliedPolicyName = policyName

	return nil
}

// handleDeletion handles the deletion of a BaoPolicy
func (r *BaoPolicyReconciler) handleDeletion(ctx context.Context, baoPolicy *kubebaoiov1alpha1.BaoPolicy) (ctrl.Result, error) {
	log := r.Log.WithValues("baopolicy", types.NamespacedName{
		Name:      baoPolicy.Name,
		Namespace: baoPolicy.Namespace,
	})

	if controllerutil.ContainsFinalizer(baoPolicy, baoPolicyFinalizer) {
		// Delete policy from OpenBao
		if r.OpenBaoClient != nil && baoPolicy.Status.AppliedPolicyName != "" {
			policyName := baoPolicy.Status.AppliedPolicyName
			path := fmt.Sprintf("sys/policies/acl/%s", policyName)

			// Note: We use ReadSecret here to simulate DELETE - in production
			// you'd want a proper delete method
			log.Info("deleting policy from OpenBao", "policyName", policyName)

			// For now, we'll just log the deletion intent
			// The actual deletion would require the DELETE HTTP method
			_ = path
		}

		// Remove finalizer
		controllerutil.RemoveFinalizer(baoPolicy, baoPolicyFinalizer)
		if err := r.Update(ctx, baoPolicy); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// setCondition sets a condition on the BaoPolicy status
func (r *BaoPolicyReconciler) setCondition(baoPolicy *kubebaoiov1alpha1.BaoPolicy, condType string, status metav1.ConditionStatus, reason, message string) {
	now := metav1.Now()

	var existingCondition *metav1.Condition
	for i := range baoPolicy.Status.Conditions {
		if baoPolicy.Status.Conditions[i].Type == condType {
			existingCondition = &baoPolicy.Status.Conditions[i]
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
		baoPolicy.Status.Conditions = append(baoPolicy.Status.Conditions, metav1.Condition{
			Type:               condType,
			Status:             status,
			LastTransitionTime: now,
			Reason:             reason,
			Message:            message,
		})
	}
}

// SetupWithManager sets up the controller with the Manager
func (r *BaoPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kubebaoiov1alpha1.BaoPolicy{}).
		Complete(r)
}
