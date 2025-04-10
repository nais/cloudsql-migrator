package resolved

import (
	"context"
	"fmt"
	"strings"
	"time"

	nais_io_v1 "github.com/nais/liberator/pkg/apis/nais.io/v1"
	"github.com/nais/liberator/pkg/namegen"
	"github.com/sethvargo/go-retry"

	"github.com/GoogleCloudPlatform/k8s-config-connector/pkg/clients/generated/apis/sql/v1beta1"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/nais/liberator/pkg/apis/nais.io/v1alpha1"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Resolved is configuration that is resolved by looking up in the cluster

type SslCert struct {
	SslClientKey  string
	SslClientCert string
	SslCaCert     string
}

type Instance struct {
	Name             string
	PrimaryIp        string
	OutgoingIps      []string
	AppUsername      string
	AppPassword      string
	PostgresPassword string
	SslCert          SslCert
}

type Resolved struct {
	GcpProjectId string
	DatabaseName string
	Source       Instance
	Target       Instance
}

type GcpProject struct {
	Id string
}

type Require int

const (
	RequireOutgoingIp Require = iota
)

func (r Require) In(required []Require) bool {
	for _, req := range required {
		if req == r {
			return true
		}
	}
	return false
}

func ResolveGcpProject(ctx context.Context, cfg *config.Config, mgr *common_main.Manager) (*GcpProject, error) {
	mgr.Logger.Info("resolving google project id", "namespace", cfg.Namespace)
	ns, err := mgr.K8sClient.CoreV1().Namespaces().Get(ctx, cfg.Namespace, meta_v1.GetOptions{})
	if err != nil {
		return nil, err
	}

	if projectId, ok := ns.Annotations["cnrm.cloud.google.com/project-id"]; ok {
		return &GcpProject{Id: projectId}, nil
	} else {
		return nil, fmt.Errorf("unable to determine google project id for namespace %s", cfg.Namespace)
	}
}

func (r *GcpProject) GcpParentURI() string {
	return fmt.Sprintf("projects/%s/locations/europe-north1", r.Id)
}

func (r *GcpProject) GcpComponentURI(kind, name string) string {
	return fmt.Sprintf("%s/%s/%s", r.GcpParentURI(), kind, name)
}

func MigrationName(sourceName, targetName string) (string, error) {
	if len(sourceName) == 0 || len(targetName) == 0 {
		return "", fmt.Errorf("source and target must be resolved")
	}
	name := fmt.Sprintf("%s-%s", sourceName, targetName)

	const maxlen = 60 // Google allows a max length of 60 for migration job names
	if len(name) > maxlen {
		var err error
		name, err = namegen.ShortName(name, maxlen)
		if err != nil {
			return "", fmt.Errorf("generating migration name: %w", err)
		}
	}

	return name, nil
}

func (i *Instance) resolveAppPassword(secret *v1.Secret) error {
	for key, bytes := range secret.Data {
		if strings.HasSuffix(key, "_PASSWORD") {
			i.AppPassword = string(bytes)
			return nil
		}
	}
	return fmt.Errorf("unable to find password in secret %s", secret.Name)
}

func (i *Instance) resolveAppUsername(secret *v1.Secret) error {
	for key, bytes := range secret.Data {
		if strings.HasSuffix(key, "_USERNAME") {
			i.AppUsername = string(bytes)
			return nil
		}
	}
	return fmt.Errorf("unable to find password in secret %s", secret.Name)
}

func resolveInstanceName(app *nais_io_v1alpha1.Application) (string, error) {
	spec := app.Spec
	if spec.GCP != nil {
		gcp := spec.GCP
		if len(gcp.SqlInstances) == 1 {
			instance := gcp.SqlInstances[0]
			if len(instance.Name) > 0 {
				return instance.Name, nil
			}
			return app.ObjectMeta.Name, nil
		}
	}
	return "", fmt.Errorf("application does not have sql instance")
}

func ResolveInstance(ctx context.Context, app *nais_io_v1alpha1.Application, mgr *common_main.Manager, required ...Require) (*Instance, error) {
	name, err := resolveInstanceName(app)
	if err != nil {
		return nil, err
	}
	instance := &Instance{
		Name: name,
	}

	mgr.Logger.Info("resolving sql instance", "name", name)

	b := retry.NewConstant(5 * time.Second)
	b = retry.WithMaxDuration(15*time.Minute, b)

	sqlInstance, err := retry.DoValue(ctx, b, func(ctx context.Context) (*v1beta1.SQLInstance, error) {
		mgr.Logger.Info("waiting for sql instance to be ready", "instance", instance.Name)
		sqlInstance, err := mgr.SqlInstanceClient.Get(ctx, instance.Name)
		if err != nil {
			if errors.IsNotFound(err) {
				mgr.Logger.Info("sql instance not found, retrying", "instance", instance.Name)
				return nil, retry.RetryableError(fmt.Errorf("sql instance not found, retrying: %w", err))
			}
			mgr.Logger.Info("unable to get sql instance, retrying", "instance", instance.Name)
			return nil, retry.RetryableError(fmt.Errorf("unable to get sql instance, retrying: %w", err))
		}

		conditionsNotEmpty := len(sqlInstance.Status.Conditions) > 0
		if conditionsNotEmpty {
			condition := sqlInstance.Status.Conditions[0]

			if condition.Reason == "UpdateFailed" {
				return nil, retry.RetryableError(fmt.Errorf("sql instance update has failed, retrying to see if it resolves itself: %s", condition.Message))
			}

			if condition.Reason == "UpToDate" {
				return sqlInstance, nil
			}
		}
		mgr.Logger.Info("sql instance not ready, retrying")
		return nil, retry.RetryableError(fmt.Errorf("sql instance not ready, retrying"))
	})
	if err != nil {
		return nil, err
	}

	mgr.Logger.Info("sql instance is ready, resolving values")
	if sqlInstance.Status.PublicIpAddress == nil {
		return nil, fmt.Errorf("sql instance %s does not have public ip address", instance.Name)
	}
	instance.PrimaryIp = *sqlInstance.Status.PublicIpAddress

	err = resolveOutgoingIps(ctx, instance, mgr, RequireOutgoingIp.In(required))
	if err != nil {
		return nil, err
	}

	b = retry.NewConstant(5 * time.Second)
	b = retry.WithMaxDuration(15*time.Minute, b)

	secretName := "google-sql-" + app.Name
	secret, err := retry.DoValue(ctx, b, func(ctx context.Context) (*v1.Secret, error) {
		var secret *v1.Secret
		secret, err = mgr.K8sClient.CoreV1().Secrets(app.Namespace).Get(ctx, secretName, meta_v1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				mgr.Logger.Info("waiting for secret to be created", "secret", secretName)
				return nil, retry.RetryableError(err)
			}
			return nil, err
		}
		if secret.Annotations[nais_io_v1.DeploymentCorrelationIDAnnotation] != app.Status.CorrelationID {
			mgr.Logger.Info("waiting for secret to be updated", "secret", secretName)
			return nil, retry.RetryableError(fmt.Errorf("secret not updated, retrying"))
		}
		return secret, nil
	})
	if err != nil {
		return nil, err
	}

	err = instance.resolveAppUsername(secret)
	if err != nil {
		return nil, err
	}

	err = instance.resolveAppPassword(secret)
	if err != nil {
		return nil, err
	}

	return instance, nil
}

func resolveOutgoingIps(ctx context.Context, instance *Instance, mgr *common_main.Manager, required bool) error {
	b := retry.NewConstant(30 * time.Second)
	b = retry.WithMaxDuration(15*time.Minute, b)

	outgoingIps, err := retry.DoValue(ctx, b, func(ctx context.Context) ([]string, error) {
		sqlInstance, err := mgr.SqlInstanceClient.Get(ctx, instance.Name)
		if err != nil {
			if errors.IsNotFound(err) {
				mgr.Logger.Info("sql instance not found, retrying", "instance", instance.Name)
				return nil, retry.RetryableError(fmt.Errorf("sql instance not found: %w", err))
			}
			mgr.Logger.Info("unable to get sql instance, retrying", "instance", instance.Name)
			return nil, retry.RetryableError(fmt.Errorf("unable to get sql instance: %w", err))
		}

		outgoingIps := make([]string, 0, 2)
		for _, ip := range sqlInstance.Status.IpAddress {
			if *ip.Type == "OUTGOING" {
				outgoingIps = append(instance.OutgoingIps, *ip.IpAddress)
			}
		}
		if !required || len(outgoingIps) > 0 {
			return outgoingIps, nil
		}
		mgr.Logger.Info("sql instance does not have required outgoing ip addresses, waiting some more", "instance", instance.Name)
		return nil, retry.RetryableError(fmt.Errorf("sql instance %s does not have required outgoing ip addresses", instance.Name))
	})
	if err != nil {
		return err
	}
	instance.OutgoingIps = outgoingIps
	return nil
}

func ResolveDatabaseName(app *nais_io_v1alpha1.Application) (string, error) {
	spec := app.Spec
	if spec.GCP != nil {
		gcp := spec.GCP
		if len(gcp.SqlInstances) == 1 && len(gcp.SqlInstances[0].Databases) == 1 {
			database := gcp.SqlInstances[0].Databases[0]
			if len(database.Name) > 0 {
				return database.Name, nil
			}
			return app.ObjectMeta.Name, nil
		}
	}
	return "", fmt.Errorf("application does not have sql database")
}
