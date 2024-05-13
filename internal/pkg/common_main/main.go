package common_main

import (
	"context"
	"fmt"
	"github.com/GoogleCloudPlatform/k8s-config-connector/pkg/clients/generated/apis/sql/v1beta1"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/nais/cloudsql-migrator/internal/pkg/k8s"
	naisv1alpha1 "github.com/nais/liberator/pkg/apis/nais.io/v1alpha1"
	sql_cnrm_cloud_google_com_v1beta1 "github.com/nais/liberator/pkg/apis/sql.cnrm.cloud.google.com/v1beta1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"log/slog"
)

type Manager struct {
	Logger            *slog.Logger
	Clientset         kubernetes.Interface
	Client            dynamic.Interface
	AppClient         k8s.AppClient
	SqlInstanceClient k8s.SqlInstanceClient
	SqlSslCertClient  k8s.SqlSslCertClient
}

func Main(ctx context.Context, cfg *config.CommonConfig, logger *slog.Logger) (*Manager, error) {
	clientset, dynamicClient, err := newK8sClient()
	if err != nil {
		return nil, err
	}

	appClient := k8s.New[*naisv1alpha1.Application](dynamicClient, cfg.Namespace, naisv1alpha1.GroupVersion.WithResource("applications"))
	sqlInstanceClient := k8s.New[*sql_cnrm_cloud_google_com_v1beta1.SQLInstance](dynamicClient, cfg.Namespace, sql_cnrm_cloud_google_com_v1beta1.GroupVersion.WithResource("sqlinstances"))
	sqlSslCertClient := k8s.New[*v1beta1.SQLSSLCert](dynamicClient, cfg.Namespace, sql_cnrm_cloud_google_com_v1beta1.GroupVersion.WithResource("sqlsslcerts"))

	err = resolveConfiguration(ctx, cfg, clientset, appClient)
	if err != nil {
		return nil, err
	}

	logger = logger.With("appName", cfg.ApplicationName,
		"instanceName", cfg.InstanceName,
		"newInstanceName", cfg.NewInstance.Name,
		"gcpProjectId", cfg.GcpProjectId,
	)

	return &Manager{
		Logger:            logger,
		Clientset:         clientset,
		Client:            dynamicClient,
		AppClient:         appClient,
		SqlInstanceClient: sqlInstanceClient,
		SqlSslCertClient:  sqlSslCertClient,
	}, nil
}

func resolveConfiguration(ctx context.Context, cfg *config.CommonConfig, clientset kubernetes.Interface, client k8s.AppClient) error {
	ns, err := clientset.CoreV1().Namespaces().Get(ctx, cfg.Namespace, v1.GetOptions{})
	if err != nil {
		return err
	}

	if projectId, ok := ns.Annotations["cnrm.cloud.google.com/project-id"]; ok {
		cfg.Resolved.GcpProjectId = projectId
	}

	app, err := client.Get(ctx, cfg.ApplicationName)
	if err != nil {
		return err
	}
	cfg.Resolved.InstanceName, err = resolveInstanceName(app)

	return nil
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

func newK8sClient() (kubernetes.Interface, dynamic.Interface, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	clusterConfig, err := kubeConfig.ClientConfig()
	if err != nil {
		return nil, nil, err
	}

	clientset, err := kubernetes.NewForConfig(clusterConfig)
	if err != nil {
		return nil, nil, err
	}

	dynamicClient, err := dynamic.NewForConfig(clusterConfig)
	if err != nil {
		return nil, nil, err
	}

	return clientset, dynamicClient, nil
}
