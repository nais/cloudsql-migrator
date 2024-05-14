package k8s

import (
	"context"
	"github.com/GoogleCloudPlatform/k8s-config-connector/pkg/clients/generated/apis/sql/v1beta1"
	naisv1alpha1 "github.com/nais/liberator/pkg/apis/nais.io/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

type genericClient[T interface {
	runtime.Object
	*P
}, P any] struct {
	client dynamic.ResourceInterface
}

type GenericClient[T interface {
	runtime.Object
	*P
}, P any] interface {
	Get(ctx context.Context, name string) (*P, error)
	Update(ctx context.Context, obj *P) (*P, error)
	Create(ctx context.Context, obj *P) (*P, error)
}

type AppClient GenericClient[*naisv1alpha1.Application, naisv1alpha1.Application]
type SqlInstanceClient GenericClient[*v1beta1.SQLInstance, v1beta1.SQLInstance]
type SqlSslCertClient GenericClient[*v1beta1.SQLSSLCert, v1beta1.SQLSSLCert]

func New[T interface {
	runtime.Object
	*P
}, P any](client dynamic.Interface, namespace string, groupVersionResource schema.GroupVersionResource) GenericClient[T, P] {
	dynamicClient := client.Resource(groupVersionResource).Namespace(namespace)
	return &genericClient[T, P]{client: dynamicClient}
}

func (g *genericClient[T, P]) Get(ctx context.Context, name string) (*P, error) {
	obj := new(P)

	u, err := g.client.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return obj, err
	}

	err = runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, obj)
	if err != nil {
		return obj, err
	}

	return obj, nil
}

func (g *genericClient[T, P]) Update(ctx context.Context, obj *P) (*P, error) {
	data, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, err
	}

	u := &unstructured.Unstructured{Object: data}

	u, err = g.client.Update(ctx, u, metav1.UpdateOptions{})
	if err != nil {
		return nil, err
	}

	err = runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, obj)
	if err != nil {
		return nil, err
	}

	return obj, nil
}

func (g *genericClient[T, P]) Create(ctx context.Context, obj *P) (*P, error) {
	data, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, err
	}

	u := &unstructured.Unstructured{Object: data}

	u, err = g.client.Create(ctx, u, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	err = runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, obj)
	if err != nil {
		return nil, err
	}

	return obj, nil
}
