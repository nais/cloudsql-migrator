package instance

import (
	"context"
	"fmt"
	"github.com/GoogleCloudPlatform/k8s-config-connector/pkg/clients/generated/apis/sql/v1beta1"
	_ "github.com/lib/pq"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config/setup"
	nais_io_v1 "github.com/nais/liberator/pkg/apis/nais.io/v1"
	nais_io_v1alpha1 "github.com/nais/liberator/pkg/apis/nais.io/v1alpha1"
	"github.com/nais/liberator/pkg/namegen"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	dummyAppImage = "europe-north1-docker.pkg.dev/nais-io/nais/images/kafka-debug:latest"
)

func CreateInstance(ctx context.Context, cfg *setup.Config, mgr *common_main.Manager) error {
	app, err := mgr.AppClient.Get(ctx, cfg.ApplicationName)
	if err != nil {
		return err
	}

	targetInstance, err := defineTargetInstance(cfg, app)
	if err != nil {
		return err
	}

	mgr.Resolved.TargetInstanceName = targetInstance.Name

	helperName, err := namegen.ShortName(fmt.Sprintf("migrator-%s", cfg.ApplicationName), 63)
	if err != nil {
		return err
	}

	dummyApp, err := mgr.AppClient.Get(ctx, helperName)
	if errors.IsNotFound(err) {
		dummyApp = &nais_io_v1alpha1.Application{
			TypeMeta: app.TypeMeta,
			ObjectMeta: metav1.ObjectMeta{
				Name:      helperName,
				Namespace: cfg.Namespace,
				Labels: map[string]string{
					"app":                      app.Name,
					"team":                     cfg.Namespace,
					"migrator.nais.io/cleanup": app.Name,
				},
				Annotations: map[string]string{
					"migrator.nais.io/source-instance": mgr.Resolved.SourceInstanceName,
					"migrator.nais.io/target-instance": cfg.TargetInstance.Name,
				},
			},
			Spec: nais_io_v1alpha1.ApplicationSpec{
				GCP: &nais_io_v1.GCP{
					SqlInstances: []nais_io_v1.CloudSqlInstance{*targetInstance},
				},
				Image: dummyAppImage,
			},
		}

		_, err = mgr.AppClient.Create(ctx, dummyApp)
	}
	if err != nil {
		return err
	}

	mgr.Logger.Info("started creation of target instance", "helperApp", helperName)

	return nil
}

func defineTargetInstance(cfg *setup.Config, app *nais_io_v1alpha1.Application) (*nais_io_v1.CloudSqlInstance, error) {
	sourceInstance := app.Spec.GCP.SqlInstances[0]
	targetInstance := sourceInstance.DeepCopy()

	targetInstance.Name = cfg.TargetInstance.Name
	targetInstance.CascadingDelete = false
	if cfg.TargetInstance.Tier != "" {
		targetInstance.Tier = cfg.TargetInstance.Tier
	}
	if cfg.TargetInstance.DiskSize != 0 {
		targetInstance.DiskSize = cfg.TargetInstance.DiskSize
	}
	if cfg.TargetInstance.Type != "" {
		targetInstance.Type = nais_io_v1.CloudSqlInstanceType(cfg.TargetInstance.Type)
	} else {
		switch sourceInstance.Type {
		case nais_io_v1.CloudSqlInstanceTypePostgres11:
			targetInstance.Type = nais_io_v1.CloudSqlInstanceTypePostgres12
		case nais_io_v1.CloudSqlInstanceTypePostgres12:
			targetInstance.Type = nais_io_v1.CloudSqlInstanceTypePostgres13
		case nais_io_v1.CloudSqlInstanceTypePostgres13:
			targetInstance.Type = nais_io_v1.CloudSqlInstanceTypePostgres14
		case nais_io_v1.CloudSqlInstanceTypePostgres14:
			targetInstance.Type = nais_io_v1.CloudSqlInstanceTypePostgres15
		default:
			return nil, fmt.Errorf("no valid target type for instance of type %v", sourceInstance.Type)

		}
	}

	return targetInstance, nil
}

func PrepareSourceInstance(ctx context.Context, mgr *common_main.Manager) error {
	mgr.Logger.Info("preparing source instance for migration")

	sqlInstance, err := mgr.SqlInstanceClient.Get(ctx, mgr.Resolved.SourceInstanceName)
	if err != nil {
		return err
	}

	setFlag(sqlInstance, "cloudsql.enable_pglogical")
	setFlag(sqlInstance, "cloudsql.logical_decoding")

	_, err = mgr.SqlInstanceClient.Update(ctx, sqlInstance)
	if err != nil {
		return err
	}

	mgr.Logger.Info("source instance prepared for migration")
	return nil
}

func PrepareTargetInstance(ctx context.Context, mgr *common_main.Manager) error {
	mgr.Logger.Info("preparing source instance for migration")

	sqlInstance, err := mgr.SqlInstanceClient.Get(ctx, mgr.Resolved.TargetInstanceName)
	if err != nil {
		return err
	}

	mgr.Resolved.TargetInstanceIp = *sqlInstance.Status.PublicIpAddress
	mgr.Logger.Info("source instance prepared for migration")
	return nil
}

func setFlag(sqlInstance *v1beta1.SQLInstance, flagName string) {
	actualFlag := findFlag(sqlInstance.Spec.Settings.DatabaseFlags, flagName)
	if actualFlag == nil {
		sqlInstance.Spec.Settings.DatabaseFlags = append(sqlInstance.Spec.Settings.DatabaseFlags, v1beta1.InstanceDatabaseFlags{
			Name:  flagName,
			Value: "on",
		})
	} else if actualFlag.Value != "on" {
		actualFlag.Value = "on"
	}
}

func findFlag(flags []v1beta1.InstanceDatabaseFlags, key string) *v1beta1.InstanceDatabaseFlags {
	for _, flag := range flags {
		if flag.Name == key {
			return &flag
		}
	}
	return nil
}
