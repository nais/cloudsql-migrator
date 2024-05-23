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
	"google.golang.org/api/datamigration/v1"
	"google.golang.org/api/sqladmin/v1"
	core_v1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"log/slog"
	"strings"
)

type Manager struct {
	Logger *slog.Logger

	Resolved *resolved.Resolved

	AppClient            k8s.AppClient
	SqlInstanceClient    k8s.SqlInstanceClient
	SqlSslCertClient     k8s.SqlSslCertClient
	SqlAdminService      *sqladmin.Service
	DatamigrationService *datamigration.Service
	DBMigrationClient    *dms.DataMigrationClient
}

func Main(ctx context.Context, cfg *config.CommonConfig, logger *slog.Logger) (*Manager, error) {
	clientset, dynamicClient, err := newK8sClient()
	if err != nil {
		return nil, err
	}

	appClient := k8s.New[*naisv1alpha1.Application](dynamicClient, cfg.Namespace, naisv1alpha1.GroupVersion.WithResource("applications"))
	sqlInstanceClient := k8s.New[*v1beta1.SQLInstance](dynamicClient, cfg.Namespace, v1beta1.SchemeGroupVersion.WithResource("sqlinstances"))
	sqlSslCertClient := k8s.New[*v1beta1.SQLSSLCert](dynamicClient, cfg.Namespace, v1beta1.SchemeGroupVersion.WithResource("sqlsslcerts"))

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
		SqlAdminService:      sqlAdminService,
		DatamigrationService: datamigrationService,
		DBMigrationClient:    dbMigrationclient,
	}, nil
}

func resolveClusterInformation(ctx context.Context, cfg *config.CommonConfig, clientset kubernetes.Interface, client k8s.AppClient, sqlInstanceClient k8s.SqlInstanceClient, resolved *resolved.Resolved) error {
	ns, err := clientset.CoreV1().Namespaces().Get(ctx, cfg.Namespace, v1.GetOptions{})
	if err != nil {
		return err
	}

	if projectId, ok := ns.Annotations["cnrm.cloud.google.com/project-id"]; ok {
		resolved.GcpProjectId = projectId
	} else {
		return fmt.Errorf("unable to determine google project id for namespace %s", cfg.Namespace)
	}

	app, err := client.Get(ctx, cfg.ApplicationName)
	if err != nil {
		return fmt.Errorf("unable to get existing application: %w", err)
	}

	resolved.Source.Name, err = resolveInstanceName(app)
	if err != nil {
		return err
	}

	resolved.DatabaseName, err = resolveDatabaseName(app)
	if err != nil {
		return err
	}

	resolved.Source.AppUsername = resolveAppUsername(app)

	secret, err := clientset.CoreV1().Secrets(cfg.Namespace).Get(ctx, "google-sql-"+cfg.ApplicationName, v1.GetOptions{})
	if err != nil {
		return err
	}
	resolved.Source.AppPassword, err = resolveAppPassword(secret)
	if err != nil {
		return err
	}

	sqlInstance, err := sqlInstanceClient.Get(ctx, resolved.Source.Name)
	if err != nil {
		return fmt.Errorf("unable to get existing sql instance: %w", err)
	}
	if sqlInstance.Status.PublicIpAddress == nil {
		return fmt.Errorf("sql instance %s does not have public ip address", resolved.Source.Name)
	}
	resolved.Source.Ip = *sqlInstance.Status.PublicIpAddress

	return nil
}

func resolveAppPassword(secret *core_v1.Secret) (string, error) {
	for key, bytes := range secret.Data {
		if strings.HasSuffix(key, "_PASSWORD") {
			return string(bytes), nil
		}
	}
	return "", fmt.Errorf("unable to find password in secret %s", secret.Name)
}

func resolveAppUsername(app *naisv1alpha1.Application) string {
	return app.ObjectMeta.Name
}

func resolveInstanceName(app *naisv1alpha1.Application) (string, error) {
	spec := app.Spec
	if spec.GCP != nil {
		gcp := spec.GCP
		if gcp.SqlInstances != nil && len(gcp.SqlInstances) == 1 {
			instance := gcp.SqlInstances[0]
			if len(instance.Name) > 0 {
				return instance.Name, nil
			}
			return app.ObjectMeta.Name, nil
		}
	}
	return "", fmt.Errorf("application does not have sql instance")
}

func resolveDatabaseName(app *naisv1alpha1.Application) (string, error) {
	spec := app.Spec
	if spec.GCP != nil {
		gcp := spec.GCP
		if gcp.SqlInstances != nil && len(gcp.SqlInstances) == 1 {
			database := gcp.SqlInstances[0].Databases[0]
			if len(database.Name) > 0 {
				return database.Name, nil
			}
			return app.ObjectMeta.Name, nil
		}
	}
	return "", fmt.Errorf("application does not have sql database")
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
