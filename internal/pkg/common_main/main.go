package common_main

import (
	"context"
	"fmt"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	naisv1alpha1 "github.com/nais/liberator/pkg/apis/nais.io/v1alpha1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"log/slog"
)

type App struct {
	Logger    *slog.Logger
	Clientset kubernetes.Interface
	Client    dynamic.Interface
}

func Main(ctx context.Context, cfg *config.CommonConfig, logger *slog.Logger) (*App, error) {
	clientset, dynamicClient, err := newK8sClient()
	if err != nil {
		return nil, err
	}

	err = resolveConfiguration(ctx, cfg, clientset, dynamicClient)
	if err != nil {
		return nil, err
	}

	return &App{
		Logger:    logger,
		Clientset: clientset,
		Client:    dynamicClient,
	}, nil
}

func resolveConfiguration(ctx context.Context, cfg *config.CommonConfig, clientset kubernetes.Interface, client dynamic.Interface) error {
	ns, err := clientset.CoreV1().Namespaces().Get(ctx, cfg.Namespace, v1.GetOptions{})
	if err != nil {
		return err
	}

	if projectId, ok := ns.Annotations["cnrm.cloud.google.com/project-id"]; ok {
		cfg.Resolved.GcpProjectId = projectId
	}

	appClient := client.Resource(naisv1alpha1.GroupVersion.WithResource("applications")).Namespace(cfg.Namespace)
	u, err := appClient.Get(ctx, cfg.ApplicationName, v1.GetOptions{})
	if err != nil {
		return err
	}

	app := &naisv1alpha1.Application{}
	if err = runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, app); err != nil {
		return fmt.Errorf("converting to application: %w", err)
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
