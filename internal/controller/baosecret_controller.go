// Контроллер BaoSecret — синхронизация секретов из OpenBao в Kubernetes Secrets.
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
	// Финализатор для корректного удаления: при удалении BaoSecret сначала вызывается handleDeletion
	baoSecretFinalizer = "kubebao.io/finalizer"
	// Интервал по умолчанию между проверками актуальности секрета (1 минута)
	defaultRefreshInterval = time.Minute
)

// BaoSecretReconciler — контроллер, который отслеживает BaoSecret CRD и синхронизирует
// данные из OpenBao KV в Kubernetes Secret. При изменении или удалении BaoSecret
// автоматически обновляет или удаляет соответствующий Secret.
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

// Reconcile — основной цикл согласования. Вызывается при каждом изменении BaoSecret или
// связанного Secret. Возвращает ctrl.Result с RequeueAfter для периодической пересинхронизации.
func (r *BaoSecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("baosecret", req.NamespacedName)
	log.Info("Reconcile начат", "namespace", req.Namespace, "name", req.Name)

	// Загрузка BaoSecret из API-сервера
	baoSecret := &kubebaoiov1alpha1.BaoSecret{}
	if err := r.Get(ctx, req.NamespacedName, baoSecret); err != nil {
		if apierrors.IsNotFound(err) {
			log.V(1).Info("BaoSecret не найден, возможно удалён — завершение reconcile")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Ошибка получения BaoSecret")
		return ctrl.Result{}, err
	}
	log.Info("BaoSecret загружен",
		"generation", baoSecret.Generation,
		"secretPath", baoSecret.Spec.SecretPath,
		"target", baoSecret.Spec.Target.Name,
		"refreshInterval", baoSecret.Spec.RefreshInterval,
	)

	// Обработка удаления — удаление finalizer после очистки
	if !baoSecret.DeletionTimestamp.IsZero() {
		log.Info("Обработка удаления BaoSecret")
		return r.handleDeletion(ctx, baoSecret)
	}

	// Добавление finalizer при первом reconcile — гарантирует вызов handleDeletion перед удалением
	if !controllerutil.ContainsFinalizer(baoSecret, baoSecretFinalizer) {
		log.V(1).Info("Добавление finalizer")
		controllerutil.AddFinalizer(baoSecret, baoSecretFinalizer)
		if err := r.Update(ctx, baoSecret); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Check if sync is suspended
	if baoSecret.Spec.SuspendSync {
		log.Info("Синхронизация приостановлена")
		r.setCondition(baoSecret, kubebaoiov1alpha1.ConditionTypeReady, metav1.ConditionFalse, 
			kubebaoiov1alpha1.ReasonSyncSuspended, "Sync is suspended")
		if err := r.Status().Update(ctx, baoSecret); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Синхронизация секрета из OpenBao в Kubernetes Secret
	log.Info("Синхронизация секрета из OpenBao → Kubernetes",
		"sourcePath", baoSecret.Spec.SecretPath,
		"targetSecret", baoSecret.Spec.Target.Name,
		"targetNamespace", baoSecret.Spec.Target.Namespace,
	)
	if err := r.syncSecret(ctx, baoSecret); err != nil {
		log.Error(err, "Ошибка синхронизации секрета")
		r.setCondition(baoSecret, kubebaoiov1alpha1.ConditionTypeSynced, metav1.ConditionFalse,
			kubebaoiov1alpha1.ReasonFailed, err.Error())
		r.setCondition(baoSecret, kubebaoiov1alpha1.ConditionTypeReady, metav1.ConditionFalse,
			kubebaoiov1alpha1.ReasonFailed, "Failed to sync secret")
		if err := r.Status().Update(ctx, baoSecret); err != nil {
			return ctrl.Result{}, err
		}
		log.Info("Повторная попытка через 30 секунд")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Обновление статуса BaoSecret — условия Ready/Synced, время последней синхронизации
	baoSecret.Status.ObservedGeneration = baoSecret.Generation
	now := metav1.Now()
	baoSecret.Status.LastSyncTime = &now
	r.setCondition(baoSecret, kubebaoiov1alpha1.ConditionTypeSynced, metav1.ConditionTrue,
		kubebaoiov1alpha1.ReasonSuccess, "Secret synced successfully")
	r.setCondition(baoSecret, kubebaoiov1alpha1.ConditionTypeReady, metav1.ConditionTrue,
		kubebaoiov1alpha1.ReasonSuccess, "Secret is ready")

	if err := r.Status().Update(ctx, baoSecret); err != nil {
		log.Error(err, "Ошибка обновления статуса BaoSecret")
		return ctrl.Result{}, err
	}

	// Планирование следующей синхронизации по refreshInterval (по умолчанию 1 час)
	refreshInterval := r.parseRefreshInterval(baoSecret.Spec.RefreshInterval)
	log.Info("Секрет успешно синхронизирован", "secret", baoSecret.Spec.Target.Name, "nextSync", refreshInterval)
	
	return ctrl.Result{RequeueAfter: refreshInterval}, nil
}

// syncSecret — читает секрет из OpenBao KV по указанному пути, применяет шаблон (если задан)
// и создаёт/обновляет Kubernetes Secret в целевом namespace.
func (r *BaoSecretReconciler) syncSecret(ctx context.Context, baoSecret *kubebaoiov1alpha1.BaoSecret) error {
	log := r.Log.WithValues("baosecret", types.NamespacedName{
		Name:      baoSecret.Name,
		Namespace: baoSecret.Namespace,
	})

	// Клиент OpenBao — общий для всех BaoSecret (настраивается в main через env/config)
	baoClient := r.OpenBaoClient
	if baoClient == nil {
		return fmt.Errorf("OpenBao client not configured")
	}

	// Чтение секрета из OpenBao KV v2 (путь вида secret/data/myapp/database)
	log.Info("Чтение секрета из OpenBao KV", "path", baoSecret.Spec.SecretPath)
	secretData, err := baoClient.KVRead(ctx, baoSecret.Spec.SecretPath)
	if err != nil {
		log.Error(err, "Ошибка чтения секрета из OpenBao", "path", baoSecret.Spec.SecretPath)
		return fmt.Errorf("failed to read secret from OpenBao: %w", err)
	}
	log.Info("Секрет прочитан из OpenBao", "keysCount", len(secretData), "path", baoSecret.Spec.SecretPath)

	// Извлечение конкретного ключа (SecretKey) или всех ключей
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

	// Применение шаблона StringData/Data — подстановка {{ .Data.key }} из sourceData
	if baoSecret.Spec.Template != nil {
		data, err = r.applyTemplate(data, baoSecret.Spec.Template, secretData)
		if err != nil {
			log.Error(err, "Ошибка применения шаблона")
			return fmt.Errorf("failed to apply template: %w", err)
		}
		log.V(1).Info("Шаблон применён")
	}

	// Целевой namespace — из spec или совпадает с namespace BaoSecret
	targetNamespace := baoSecret.Spec.Target.Namespace
	if targetNamespace == "" {
		targetNamespace = baoSecret.Namespace
	}

	// Хэш данных для отслеживания изменений (версионирование)
	version := r.calculateVersion(data)

	// CreateOrUpdate — идемпотентное создание/обновление Secret (операция: "created" или "updated")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      baoSecret.Spec.Target.Name,
			Namespace: targetNamespace,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		// Метки для идентификации управляемого секрета
		if secret.Labels == nil {
			secret.Labels = make(map[string]string)
		}
		secret.Labels["kubebao.io/managed-by"] = "kubebao-operator"
		secret.Labels["kubebao.io/baosecret"] = baoSecret.Name
		for k, v := range baoSecret.Spec.Target.Labels {
			secret.Labels[k] = v
		}

		// Аннотации: путь в OpenBao, версия (хэш данных)
		if secret.Annotations == nil {
			secret.Annotations = make(map[string]string)
		}
		secret.Annotations["kubebao.io/source-path"] = baoSecret.Spec.SecretPath
		secret.Annotations["kubebao.io/version"] = version
		for k, v := range baoSecret.Spec.Target.Annotations {
			secret.Annotations[k] = v
		}

		// Тип Secret (Opaque, kubernetes.io/tls, kubernetes.io/dockerconfigjson и т.д.)
		if baoSecret.Spec.Target.Type != "" {
			secret.Type = corev1.SecretType(baoSecret.Spec.Target.Type)
		} else {
			secret.Type = corev1.SecretTypeOpaque
		}

		// Данные секрета (все ключи → []byte)
		secret.Data = data

		// Owner reference: при "Owner" Secret удаляется вместе с BaoSecret; при "Orphan" — остаётся
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

	log.Info("Операция с Kubernetes Secret выполнена",
		"operation", op,
		"secret", secret.Name,
		"namespace", secret.Namespace,
		"version", version,
		"keysCount", len(data),
	)

	// Update status
	baoSecret.Status.SecretVersion = version
	baoSecret.Status.SyncedSecretName = secret.Name
	baoSecret.Status.SyncedSecretNamespace = secret.Namespace

	return nil
}

// handleDeletion — вызывается при удалении BaoSecret. При Orphan policy Secret не удаляется.
func (r *BaoSecretReconciler) handleDeletion(ctx context.Context, baoSecret *kubebaoiov1alpha1.BaoSecret) (ctrl.Result, error) {
	log := r.Log.WithValues("baosecret", types.NamespacedName{
		Name:      baoSecret.Name,
		Namespace: baoSecret.Namespace,
	})

	if controllerutil.ContainsFinalizer(baoSecret, baoSecretFinalizer) {
		// Secret с owner reference удалится сборщиком мусора автоматически.
		// При Orphan policy — намеренно оставляем Secret (пользователь удалит вручную при необходимости)
		if baoSecret.Spec.Target.CreationPolicy == "Orphan" {
			log.Info("Секрет оставляется при удалении (Orphan policy)")
		}

		// Remove finalizer
		controllerutil.RemoveFinalizer(baoSecret, baoSecretFinalizer)
		if err := r.Update(ctx, baoSecret); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// applyTemplate — подстановка шаблонов {{ .Data.key }} в StringData и Data из sourceData
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

// replaceAll — замена всех вхождений old на new в строке s (без regex)
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

// indexOf — возвращает индекс первого вхождения substr в s или -1
func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// calculateVersion — SHA256-хэш данных (первые 8 байт в hex) для версионирования
func (r *BaoSecretReconciler) calculateVersion(data map[string][]byte) string {
	jsonData, _ := json.Marshal(data)
	hash := sha256.Sum256(jsonData)
	return hex.EncodeToString(hash[:8])
}

// parseRefreshInterval — разбор интервала ("1h", "30m"). Минимум 1 минута.
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

// setCondition — обновление условия в status.conditions (Ready, Synced)
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
