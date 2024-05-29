package common_main

import (
	dms "cloud.google.com/go/clouddms/apiv1"
	"context"
	"fmt"
	"github.com/GoogleCloudPlatform/k8s-config-connector/pkg/clients/generated/apis/sql/v1beta1"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/nais/cloudsql-migrator/internal/pkg/k8s"
	"github.com/nais/cloudsql-migrator/internal/pkg/resolved"
	naisv1alpha1 "github.com/nais/liberator/pkg/apis/nais.io/v1alpha1"
	"github.com/nais/liberator/pkg/namegen"
	"google.golang.org/api/datamigration/v1"
	"google.golang.org/api/sqladmin/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"log/slog"
)

type Manager struct {
	Logger *slog.Logger

	Resolved *resolved.Resolved

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

	r := &resolved.Resolved{}
	err = resolveClusterInformation(ctx, cfg, clientset, appClient, sqlInstanceClient, r)
	if err != nil {
		return nil, err
	}

	logger = logger.With("app", cfg.ApplicationName,
		"sourceInstance", r.Source.Name,
		"targetInstance", cfg.TargetInstance.Name,
		"projectId", r.GcpProjectId,
	)

	return &Manager{
		Logger:               logger,
		Resolved:             r,
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

func resolveClusterInformation(ctx context.Context, cfg *config.Config, clientset kubernetes.Interface, client k8s.AppClient, sqlInstanceClient k8s.SqlInstanceClient, r *resolved.Resolved) error {
	ns, err := clientset.CoreV1().Namespaces().Get(ctx, cfg.Namespace, v1.GetOptions{})
	if err != nil {
		return err
	}

	if projectId, ok := ns.Annotations["cnrm.cloud.google.com/project-id"]; ok {
		r.GcpProjectId = projectId
	} else {
		return fmt.Errorf("unable to determine google project id for namespace %s", cfg.Namespace)
	}

	app, err := client.Get(ctx, cfg.ApplicationName)
	if err != nil {
		return fmt.Errorf("unable to get existing application: %w", err)
	}

	err = r.ResolveSourceInstanceName(app)
	if err != nil {
		return err
	}
	r.Target.Name = cfg.TargetInstance.Name

	err = r.ResolveDatabaseName(app)
	if err != nil {
		return err
	}

	r.ResolveSourceAppUsername(app)

	secret, err := clientset.CoreV1().Secrets(cfg.Namespace).Get(ctx, "google-sql-"+cfg.ApplicationName, v1.GetOptions{})
	if err != nil {
		return err
	}

	err = r.Source.ResolveAppPassword(secret)
	if err != nil {
		return err
	}

	sqlInstance, err := sqlInstanceClient.Get(ctx, r.Source.Name)
	if err != nil {
		return fmt.Errorf("unable to get existing sql instance: %w", err)
	}
	if sqlInstance.Status.PublicIpAddress == nil {
		return fmt.Errorf("sql instance %s does not have public ip address", r.Source.Name)
	}
	r.Source.Ip = *sqlInstance.Status.PublicIpAddress

	return nil
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

func HelperAppName(applicationName string) (string, error) {
	helperName, err := namegen.ShortName(fmt.Sprintf("migrator-%s", applicationName), 63)
	if err != nil {
		return "", err
	}

	return helperName, nil
}
