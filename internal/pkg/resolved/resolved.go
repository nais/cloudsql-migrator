package resolved

import (
	"fmt"
	"github.com/nais/liberator/pkg/apis/nais.io/v1alpha1"
	"k8s.io/api/core/v1"
	"strings"
)

// Resolved is configuration that is resolved by looking up in the cluster

type SslCert struct {
	SslClientKey  string
	SslClientCert string
	SslCaCert     string
}

type Instance struct {
	Name             string
	Ip               string
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

func (r *Resolved) GcpParentURI() string {
	return fmt.Sprintf("projects/%s/locations/europe-north1", r.GcpProjectId)
}

func (r *Resolved) GcpComponentURI(kind, name string) string {
	return fmt.Sprintf("%s/%s/%s", r.GcpParentURI(), kind, name)
}

func (r *Resolved) MigrationName() (string, error) {
	if len(r.Source.Name) == 0 || len(r.Target.Name) == 0 {
		return "", fmt.Errorf("source and target must be resolved")
	}
	return fmt.Sprintf("%s-%s", r.Source.Name, r.Target.Name), nil
}

func (i *Instance) ResolveAppPassword(secret *v1.Secret) error {
	for key, bytes := range secret.Data {
		if strings.HasSuffix(key, "_PASSWORD") {
			i.AppPassword = string(bytes)
			return nil
		}
	}
	return fmt.Errorf("unable to find password in secret %s", secret.Name)
}

func (i *Instance) ResolveAppUsername(secret *v1.Secret) error {
	for key, bytes := range secret.Data {
		if strings.HasSuffix(key, "_USERNAME") {
			i.AppPassword = string(bytes)
			return nil
		}
	}
	return fmt.Errorf("unable to find password in secret %s", secret.Name)
}

func (r *Resolved) ResolveSourceAppUsername(app *nais_io_v1alpha1.Application) {
	r.Source.AppUsername = app.ObjectMeta.Name
}

func (r *Resolved) ResolveSourceInstanceName(app *nais_io_v1alpha1.Application) error {
	spec := app.Spec
	if spec.GCP != nil {
		gcp := spec.GCP
		if gcp.SqlInstances != nil && len(gcp.SqlInstances) == 1 {
			instance := gcp.SqlInstances[0]
			if len(instance.Name) > 0 {
				r.Source.Name = instance.Name
				return nil
			}
			r.Source.Name = app.ObjectMeta.Name
			return nil
		}
	}
	return fmt.Errorf("application does not have sql instance")
}

func (r *Resolved) ResolveDatabaseName(app *nais_io_v1alpha1.Application) error {
	spec := app.Spec
	if spec.GCP != nil {
		gcp := spec.GCP
		if gcp.SqlInstances != nil && len(gcp.SqlInstances) == 1 && len(gcp.SqlInstances[0].Databases) == 1 {
			database := gcp.SqlInstances[0].Databases[0]
			if len(database.Name) > 0 {
				r.DatabaseName = database.Name
				return nil
			}
			r.DatabaseName = app.ObjectMeta.Name
			return nil
		}
	}
	return fmt.Errorf("application does not have sql database")
}
