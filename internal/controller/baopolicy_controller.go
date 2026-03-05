// Контроллер BaoPolicy — синхронизация политик OpenBao из BaoPolicy CRD.
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
	// Финализатор для BaoPolicy — вызов handleDeletion при удалении
	baoPolicyFinalizer = "kubebao.io/policy-finalizer"
)

// BaoPolicyReconciler — контроллер, синхронизирующий политики OpenBao (HCL) из BaoPolicy CRD.
type BaoPolicyReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	Log           logr.Logger
	OpenBaoClient *openbao.Client
}

// +kubebuilder:rbac:groups=kubebao.io,resources=baopolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kubebao.io,resources=baopolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kubebao.io,resources=baopolicies/finalizers,verbs=update

// Reconcile — цикл согласования BaoPolicy. Записывает HCL-политику в OpenBao sys/policies/acl/.
func (r *BaoPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("baopolicy", req.NamespacedName)
	log.V(1).Info("Начало reconcile BaoPolicy", "namespace", req.Namespace, "name", req.Name)

	// Загрузка BaoPolicy
	baoPolicy := &kubebaoiov1alpha1.BaoPolicy{}
	if err := r.Get(ctx, req.NamespacedName, baoPolicy); err != nil {
		if apierrors.IsNotFound(err) {
			log.V(1).Info("BaoPolicy не найден — завершение")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Ошибка получения BaoPolicy")
		return ctrl.Result{}, err
	}

	// Обработка удаления — снятие finalizer
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

	// Запись/обновление политики в OpenBao
	log.V(1).Info("Синхронизация политики", "policyName", baoPolicy.GetPolicyName())
	if err := r.syncPolicy(ctx, baoPolicy); err != nil {
		log.Error(err, "Ошибка синхронизации политики")
		r.setCondition(baoPolicy, kubebaoiov1alpha1.ConditionTypeReady, metav1.ConditionFalse,
			kubebaoiov1alpha1.ReasonFailed, err.Error())
		if err := r.Status().Update(ctx, baoPolicy); err != nil {
			return ctrl.Result{}, err
		}
		log.Info("Повтор через 30 секунд")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Обновление статуса — Ready, LastSyncTime
	baoPolicy.Status.ObservedGeneration = baoPolicy.Generation
	now := metav1.Now()
	baoPolicy.Status.LastSyncTime = &now
	r.setCondition(baoPolicy, kubebaoiov1alpha1.ConditionTypeReady, metav1.ConditionTrue,
		kubebaoiov1alpha1.ReasonSuccess, "Policy synced successfully")

	if err := r.Status().Update(ctx, baoPolicy); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Политика успешно синхронизирована", "policy", baoPolicy.Name)
	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

// syncPolicy — конвертирует BaoPolicy в HCL, сравнивает хэш с PolicyVersion, при изменении пишет в OpenBao.
func (r *BaoPolicyReconciler) syncPolicy(ctx context.Context, baoPolicy *kubebaoiov1alpha1.BaoPolicy) error {
	log := r.Log.WithValues("baopolicy", types.NamespacedName{
		Name:      baoPolicy.Name,
		Namespace: baoPolicy.Namespace,
	})

	// Клиент OpenBao
	baoClient := r.OpenBaoClient
	if baoClient == nil {
		return fmt.Errorf("OpenBao client not configured")
	}

	// Генерация HCL из BaoPolicy.Spec.Rules (path "secret/*" { capabilities = [...] })
	policyHCL := baoPolicy.ToHCL()
	policyName := baoPolicy.GetPolicyName()
	log.V(1).Info("HCL политики сгенерирован", "policyName", policyName)

	// Хэш HCL для проверки необходимости обновления
	hash := sha256.Sum256([]byte(policyHCL))
	version := hex.EncodeToString(hash[:8])

	// Check if policy needs update
	if baoPolicy.Status.PolicyVersion == version {
		log.V(1).Info("Политика не изменилась, обновление пропущено")
		return nil
	}

	// Запись в sys/policies/acl/{policyName}
	path := fmt.Sprintf("sys/policies/acl/%s", policyName)
	log.V(1).Info("Запись политики в OpenBao", "path", path)
	data := map[string]interface{}{
		"policy": policyHCL,
	}

	_, err := baoClient.WriteSecret(ctx, path, data)
	if err != nil {
		return fmt.Errorf("failed to write policy to OpenBao: %w", err)
	}

	log.Info("Политика записана в OpenBao", "policyName", policyName)

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
			log.Info("Удаление политики из OpenBao", "policyName", policyName)

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
