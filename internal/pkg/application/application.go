package application

import (
	"context"
	"fmt"
	"github.com/sethvargo/go-retry"
	"time"

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

func UpdateApplicationInstance(ctx context.Context, cfg *config.Config, instanceSettings *config.InstanceSettings, mgr *common_main.Manager) (*nais_io_v1alpha1.Application, error) {
	mgr.Logger.Info("updating application to use new instance", "name", cfg.ApplicationName)

	b := retry.NewConstant(1 * time.Second)
	b = retry.WithMaxDuration(5*time.Minute, b)

	app, err := retry.DoValue(ctx, b, func(ctx context.Context) (*nais_io_v1alpha1.Application, error) {
		app, err := mgr.AppClient.Get(ctx, cfg.ApplicationName)
		if err != nil {
			return nil, err
		}

		targetInstance := instance.DefineInstance(instanceSettings, app)
		app.Spec.GCP.SqlInstances = []nais_io_v1.CloudSqlInstance{
			*targetInstance,
		}

		app, err = mgr.AppClient.Update(ctx, app)
		if err != nil {
			if errors.IsConflict(err) {
				mgr.Logger.Info("retrying update of application")
				return nil, retry.RetryableError(err)
			}
			return nil, err
		}

		return app, nil
	})
	if err != nil {
		return nil, err
	}

	// Make sure naiserator and sqeletor has reacted before returning, so downstream resources have been updated
	time.Sleep(15 * time.Second)
	for app.Status.SynchronizationState != "RolloutComplete" {
		mgr.Logger.Info("waiting for app rollout", "appName", app.Name)
		time.Sleep(5 * time.Second)
		app, err = mgr.AppClient.Get(ctx, app.Name)
		if err != nil {
			return nil, err
		}
	}

	return app, err
}

func UpdateApplicationUser(ctx context.Context, target *resolved.Instance, gcpProject *resolved.GcpProject, mgr *common_main.Manager) error {
	mgr.Logger.Info("updating application user", "user", target.AppUsername)

	getCtx, cancel := context.WithTimeout(ctx, 1*time.Minute)
	defer cancel()
	for {
		mgr.Logger.Info("waiting for user to be up to date", "user", target.AppUsername)
		sqlUser, err := mgr.SqlUserClient.Get(getCtx, target.AppUsername)
		if err != nil {
			if errors.IsNotFound(err) {
				time.Sleep(1 * time.Second)
				continue
			}
			return fmt.Errorf("failed to get sql user: %w", err)
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
