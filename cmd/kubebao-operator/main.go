// Kubebao Operator — контроллер для синхронизации BaoSecret и BaoPolicy с OpenBao.
package main

import (
	"flag"
	"os"

	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"

	kubebaoiov1alpha1 "github.com/kubebao/kubebao/api/v1alpha1"
	"github.com/kubebao/kubebao/internal/controller"
	"github.com/kubebao/kubebao/internal/openbao"

	"github.com/hashicorp/go-hclog"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")

	// Version is set at build time
	Version = "dev"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kubebaoiov1alpha1.AddToScheme(scheme))
}

func main() {
	var (
		metricsAddr          string
		enableLeaderElection bool
		probeAddr            string
		logLevel             string
		configFile           string
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	flag.StringVar(&configFile, "config", "", "Path to OpenBao configuration file")
	flag.Parse()

	// Setup logger
	zapConfig := zap.NewProductionConfig()
	switch logLevel {
	case "debug":
		zapConfig.Level = zap.NewAtomicLevelAt(zapcore.DebugLevel)
	case "info":
		zapConfig.Level = zap.NewAtomicLevelAt(zapcore.InfoLevel)
	case "warn":
		zapConfig.Level = zap.NewAtomicLevelAt(zapcore.WarnLevel)
	case "error":
		zapConfig.Level = zap.NewAtomicLevelAt(zapcore.ErrorLevel)
	}

	zapLog, err := zapConfig.Build()
	if err != nil {
		os.Exit(1)
	}
	ctrl.SetLogger(zapr.NewLogger(zapLog))

	setupLog.Info("Запуск kubebao-operator", "version", Version)

	// Загрузка конфигурации OpenBao
	var baoConfig *openbao.Config
	if configFile != "" {
		baoConfig, err = openbao.LoadConfig(configFile)
		if err != nil {
			setupLog.Error(err, "Ошибка загрузки конфигурации OpenBao из файла")
			os.Exit(1)
		}
		setupLog.Info("Конфигурация OpenBao загружена из файла", "path", configFile)
	} else {
		baoConfig = openbao.LoadConfigFromEnv()
		setupLog.Info("Конфигурация OpenBao загружена из переменных окружения")
	}

	// Создание клиента OpenBao
	hcLogger := hclog.New(&hclog.LoggerOptions{
		Name:  "openbao",
		Level: hclog.LevelFromString(logLevel),
	})

	var baoClient *openbao.Client
	if baoConfig.Address != "" {
		baoClient, err = openbao.NewClient(baoConfig, hcLogger)
		if err != nil {
			setupLog.Error(err, "Ошибка создания клиента OpenBao (контроллеры будут работать без подключения)")
		} else {
			setupLog.Info("Успешное подключение к OpenBao", "address", baoConfig.Address)
		}
	} else {
		setupLog.Info("Адрес OpenBao не задан, инициализация клиента пропущена")
	}

	// Создание менеджера контроллеров
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "kubebao-operator.kubebao.io",
	})
	if err != nil {
		setupLog.Error(err, "Ошибка создания менеджера контроллеров")
		os.Exit(1)
	}
	setupLog.Info("Менеджер контроллеров создан")

	// Регистрация контроллера BaoSecret
	if err := (&controller.BaoSecretReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		Log:           ctrl.Log.WithName("controllers").WithName("BaoSecret"),
		OpenBaoClient: baoClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Ошибка регистрации контроллера BaoSecret")
		os.Exit(1)
	}
	setupLog.Info("Контроллер BaoSecret зарегистрирован")

	// Регистрация контроллера BaoPolicy
	if err := (&controller.BaoPolicyReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		Log:           ctrl.Log.WithName("controllers").WithName("BaoPolicy"),
		OpenBaoClient: baoClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Ошибка регистрации контроллера BaoPolicy")
		os.Exit(1)
	}
	setupLog.Info("Контроллер BaoPolicy зарегистрирован")

	// Настройка проверок здоровья
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Ошибка настройки health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "Ошибка настройки readyz check")
		os.Exit(1)
	}
	setupLog.Info("Проверки здоровья настроены")

	setupLog.Info("Запуск менеджера контроллеров")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "Ошибка при работе менеджера")
		os.Exit(1)
	}
	setupLog.Info("Менеджер остановлен")
}
