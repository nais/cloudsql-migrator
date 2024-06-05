package common_main

import (
	dms "cloud.google.com/go/clouddms/apiv1"
	"context"
	"fmt"
	"github.com/GoogleCloudPlatform/k8s-config-connector/pkg/clients/generated/apis/sql/v1beta1"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/nais/cloudsql-migrator/internal/pkg/k8s"
	naisv1alpha1 "github.com/nais/liberator/pkg/apis/nais.io/v1alpha1"
	"github.com/nais/liberator/pkg/namegen"
	"google.golang.org/api/datamigration/v1"
	"google.golang.org/api/sqladmin/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"log/slog"
)

type Manager struct {
	Logger *slog.Logger

	AppClient            k8s.AppClient
	SqlInstanceClient    k8s.SqlInstanceClient
	SqlSslCertClient     k8s.SqlSslCertClient
	SqlDatabaseClient    k8s.SqlDatabaseClient
	SqlUserClient        k8s.SqlUserClient
	SqlAdminService      *sqladmin.Service
	DatamigrationService *datamigration.Service
	DBMigrationClient    *dms.DataMigrationClient
	K8sClient            kubernetes.Interface
}

func Main(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*Manager, error) {
	clientset, dynamicClient, err := newK8sClient()
	if err != nil {
		return nil, err
	}

	appClient := k8s.New[*naisv1alpha1.Application](dynamicClient, cfg.Namespace, naisv1alpha1.GroupVersion.WithResource("applications"))
	sqlInstanceClient := k8s.New[*v1beta1.SQLInstance](dynamicClient, cfg.Namespace, v1beta1.SchemeGroupVersion.WithResource("sqlinstances"))
	sqlSslCertClient := k8s.New[*v1beta1.SQLSSLCert](dynamicClient, cfg.Namespace, v1beta1.SchemeGroupVersion.WithResource("sqlsslcerts"))
	sqlDatabaseClient := k8s.New[*v1beta1.SQLDatabase](dynamicClient, cfg.Namespace, v1beta1.SchemeGroupVersion.WithResource("sqldatabases"))
	sqlUserClient := k8s.New[*v1beta1.SQLUser](dynamicClient, cfg.Namespace, v1beta1.SchemeGroupVersion.WithResource("sqlusers"))

	sqlAdminService, err := sqladmin.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create SqlAdminService: %w", err)
	}

	datamigrationService, err := datamigration.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create DataMigrationService: %w", err)
	}

	dbMigrationclient, err := dms.NewDataMigrationClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create dbMigrationclient: %w", err)
	}

	logger = logger.With("app", cfg.ApplicationName,
		"targetInstance", cfg.TargetInstance.Name,
	)

	return &Manager{
		Logger:               logger,
		AppClient:            appClient,
		SqlInstanceClient:    sqlInstanceClient,
		SqlSslCertClient:     sqlSslCertClient,
		SqlDatabaseClient:    sqlDatabaseClient,
		SqlUserClient:        sqlUserClient,
		SqlAdminService:      sqlAdminService,
		DatamigrationService: datamigrationService,
		DBMigrationClient:    dbMigrationclient,
		K8sClient:            clientset,
	}, nil
}

func newK8sClient() (kubernetes.Interface, dynamic.Interface, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	clusterConfig, err := kubeConfig.ClientConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("unable to get cluster config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(clusterConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to create kubernetes client: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(clusterConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to create dynamic kubernetes client: %w", err)
	}

	return clientset, dynamicClient, nil
}

func HelperName(basename string) (string, error) {
	helperName, err := namegen.ShortName(fmt.Sprintf("migrator-%s", basename), 63)
	if err != nil {
		return "", err
	}

	return helperName, nil
}
