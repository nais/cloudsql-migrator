package k8s

import (
	"context"
	nais_io_v1alpha1 "github.com/nais/liberator/pkg/apis/nais.io/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
)

type appClient struct {
	client dynamic.ResourceInterface
}

type AppClient interface {
	Get(ctx context.Context, name string) (*nais_io_v1alpha1.Application, error)
	Create(ctx context.Context, obj *nais_io_v1alpha1.Application) (*nais_io_v1alpha1.Application, error)
}

func New(client dynamic.Interface, namespace string) AppClient {
	dynamicAppClient := client.Resource(nais_io_v1alpha1.GroupVersion.WithResource("applications")).Namespace(namespace)
	return &appClient{client: dynamicAppClient}
}

func (a *appClient) Get(ctx context.Context, name string) (*nais_io_v1alpha1.Application, error) {
	unstructuredApp, err := a.client.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	var app nais_io_v1alpha1.Application
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredApp.Object, &app)
	if err != nil {
		return nil, err
	}

	return &app, nil
}

func (a *appClient) Create(ctx context.Context, obj *nais_io_v1alpha1.Application) (*nais_io_v1alpha1.Application, error) {
	appData, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, err
	}

	unstructuredApp := &unstructured.Unstructured{Object: appData}

	unstructuredApp, err = a.client.Create(ctx, unstructuredApp, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	var app nais_io_v1alpha1.Application
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredApp.Object, &app)
	if err != nil {
		return nil, err
	}

	return &app, nil
}
