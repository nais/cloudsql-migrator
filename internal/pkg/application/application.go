package application

import (
	"context"
	"fmt"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/nais/cloudsql-migrator/internal/pkg/instance"
	nais_io_v1 "github.com/nais/liberator/pkg/apis/nais.io/v1"
	autoscaling_v1 "k8s.io/api/autoscaling/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strconv"
	"time"
)

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

func UpdateApplicationInstance(ctx context.Context, cfg *config.Config, mgr *common_main.Manager) error {
	mgr.Logger.Info("updating application to use new instance", "name", cfg.ApplicationName)

	app, err := mgr.AppClient.Get(ctx, cfg.ApplicationName)
	if err != nil {
		return err
	}

	targetInstance, err := instance.DefineTargetInstance(cfg, app)
	if err != nil {
		return err
	}

	app.Spec.GCP.SqlInstances = []nais_io_v1.CloudSqlInstance{
		*targetInstance,
	}

	_, err = mgr.AppClient.Update(ctx, app)
	if err != nil {
		return err
	}

	return nil
}

func UpdateApplicationUser(ctx context.Context, mgr *common_main.Manager) error {
	mgr.Logger.Info("updating application user")

	user, err := mgr.SqlUserClient.Get(ctx, mgr.Resolved.Target.AppUsername)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	// update label and annotation to force update of user in instance
	user.ObjectMeta.Labels["migrator.nais.io/touched"] = strconv.FormatInt(time.Now().Unix(), 10)
	user.ObjectMeta.Annotations["cnrm.cloud.google.com/observed-secret-versions"] = "{}"

	user, err = mgr.SqlUserClient.Update(ctx, user)
	if err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}

	return nil
}

func DeleteHelperApplication(ctx context.Context, cfg *config.Config, mgr *common_main.Manager) error {
	helperName, err := common_main.HelperAppName(cfg.ApplicationName)
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
