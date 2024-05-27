package promote

import (
	"context"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	autoscaling_v1 "k8s.io/api/autoscaling/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ScaleApplication(ctx context.Context, cfg *config.CommonConfig, mgr *common_main.Manager, replicas int32) error {
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
