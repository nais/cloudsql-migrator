package application

import (
	"context"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/nais/cloudsql-migrator/internal/pkg/database"
	"github.com/nais/cloudsql-migrator/internal/pkg/instance"
	"github.com/nais/cloudsql-migrator/internal/pkg/resolved"
	nais_io_v1 "github.com/nais/liberator/pkg/apis/nais.io/v1"
	"github.com/nais/liberator/pkg/apis/nais.io/v1alpha1"
	autoscaling_v1 "k8s.io/api/autoscaling/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"time"
)

const UpdateRetries = 3

func ScaleApplication(ctx context.Context, cfg *config.Config, mgr *common_main.Manager, replicas int32) error {
	mgr.Logger.Info("scaling application", "name", cfg.ApplicationName, "replicas", replicas)
	scaleApplyConfiguration := autoscaling_v1.Scale{
		ObjectMeta: meta_v1.ObjectMeta{
			Name:      cfg.ApplicationName,
			Namespace: cfg.Namespace,
		},
		Spec: autoscaling_v1.ScaleSpec{
			Replicas: replicas,
		},
	}
	_, err := mgr.K8sClient.AppsV1().Deployments(cfg.Namespace).UpdateScale(ctx, cfg.ApplicationName, &scaleApplyConfiguration, meta_v1.UpdateOptions{
		FieldManager: "cloudsql-migrator",
	})
	if err != nil {
		return err
	}
	return nil
}

func UpdateApplicationInstance(ctx context.Context, cfg *config.Config, mgr *common_main.Manager) (*nais_io_v1alpha1.Application, error) {
	mgr.Logger.Info("updating application to use new instance", "name", cfg.ApplicationName)

	return updateApplicationInstanceWithRetries(ctx, cfg, mgr, UpdateRetries)
}

func updateApplicationInstanceWithRetries(ctx context.Context, cfg *config.Config, mgr *common_main.Manager, retries int) (*nais_io_v1alpha1.Application, error) {
	app, err := mgr.AppClient.Get(ctx, cfg.ApplicationName)
	if err != nil {
		return nil, err
	}

	targetInstance, err := instance.DefineTargetInstance(cfg, app)
	if err != nil {
		return nil, err
	}

	app.Spec.GCP.SqlInstances = []nais_io_v1.CloudSqlInstance{
		*targetInstance,
	}

	app, err = mgr.AppClient.Update(ctx, app)
	if err != nil {
		if errors.IsConflict(err) && retries > 0 {
			mgr.Logger.Info("retrying update of application", "remaining_retries", retries)
			return updateApplicationInstanceWithRetries(ctx, cfg, mgr, retries-1)
		}
		return nil, err
	}

	return app, nil
}

func UpdateApplicationUser(ctx context.Context, target *resolved.Instance, gcpProject *resolved.GcpProject, mgr *common_main.Manager) error {
	mgr.Logger.Info("updating application user")

	for {
		mgr.Logger.Debug("waiting for user to be up to date")
		sqlUser, err := mgr.SqlUserClient.Get(ctx, target.AppUsername)
		if err != nil {
			if errors.IsNotFound(err) {
				time.Sleep(1 * time.Second)
				continue
			}
		}

		if sqlUser.Status.Conditions[0].Reason != "UpToDate" {
			time.Sleep(3 * time.Second)
			continue
		}
		break
	}

	return database.SetDatabasePassword(ctx, target.Name, target.AppUsername, target.AppPassword, gcpProject, mgr)
}

func DeleteHelperApplication(ctx context.Context, cfg *config.Config, mgr *common_main.Manager) error {
	helperName, err := common_main.HelperName(cfg.ApplicationName)
	if err != nil {
		return err
	}

	mgr.Logger.Info("deleting migration application", "name", helperName)

	err = mgr.AppClient.Delete(ctx, helperName)
	if err != nil {
		return err
	}

	return nil
}

func DisableCascadingDelete(ctx context.Context, cfg *config.Config, mgr *common_main.Manager) error {
	mgr.Logger.Info("disabling cascading delete", "name", cfg.ApplicationName)

	app, err := mgr.AppClient.Get(ctx, cfg.ApplicationName)
	if err != nil {
		return err
	}

	app.Spec.GCP.SqlInstances[0].CascadingDelete = false

	_, err = mgr.AppClient.Update(ctx, app)
	if err != nil {
		return err
	}

	return nil
}
