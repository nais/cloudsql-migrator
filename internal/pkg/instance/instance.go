package instance

import (
	"context"
	"errors"
	"fmt"
	"github.com/GoogleCloudPlatform/k8s-config-connector/pkg/clients/generated/apis/sql/v1beta1"
	_ "github.com/lib/pq"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	nais_io_v1 "github.com/nais/liberator/pkg/apis/nais.io/v1"
	nais_io_v1alpha1 "github.com/nais/liberator/pkg/apis/nais.io/v1alpha1"
	"google.golang.org/api/googleapi"
	"io"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"net/http"
	"os"
	"os/user"
	"time"
)

const (
	dummyAppImage = "europe-north1-docker.pkg.dev/nais-io/nais/images/kafka-debug:latest"
)

func CreateInstance(ctx context.Context, cfg *config.Config, mgr *common_main.Manager) error {
	app, err := mgr.AppClient.Get(ctx, cfg.ApplicationName)
	if err != nil {
		return err
	}

	targetInstance, err := DefineTargetInstance(cfg, app)
	if err != nil {
		return err
	}

	mgr.Resolved.Target.Name = targetInstance.Name

	helperName, err := common_main.HelperAppName(cfg.ApplicationName)
	if err != nil {
		return err
	}

	dummyApp, err := mgr.AppClient.Get(ctx, helperName)
	if k8s_errors.IsNotFound(err) {
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
					"migrator.nais.io/source-instance": mgr.Resolved.Source.Name,
					"migrator.nais.io/target-instance": cfg.TargetInstance.Name,
				},
			},
			Spec: nais_io_v1alpha1.ApplicationSpec{
				Replicas: &nais_io_v1.Replicas{
					Min: ptr.To(1),
					Max: ptr.To(1),
				},
				GCP: &nais_io_v1.GCP{
					SqlInstances: []nais_io_v1.CloudSqlInstance{*targetInstance},
				},
				Image: dummyAppImage,
			},
		}

		app, err = mgr.AppClient.Create(ctx, dummyApp)
	}
	if err != nil {
		return err
	}
	mgr.Logger.Info("started creation of target instance", "helperApp", helperName)
	for app.Status.DeploymentRolloutStatus != "complete" {
		mgr.Logger.Info("waiting for dummy app rollout")
		time.Sleep(5 * time.Second)
		app, err = mgr.AppClient.Get(ctx, helperName)
		if err != nil {
			return err
		}
	}

	mgr.Logger.Info("deleting kubernetes database resource for target instance")
	err = mgr.SqlDatabaseClient.DeleteCollection(ctx, metav1.ListOptions{
		LabelSelector: "app=" + helperName,
	})
	if err != nil {
		return fmt.Errorf("failed to delete databases from target instance: %w", err)
	}

	mgr.Logger.Info("deleting database in target instance")
	op, err := mgr.SqlAdminService.Databases.Delete(mgr.Resolved.GcpProjectId, mgr.Resolved.Target.Name, mgr.Resolved.DatabaseName).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to delete database from target instance: %w", err)
	}

	for op.Status != "DONE" {
		time.Sleep(1 * time.Second)
		op, err = mgr.SqlAdminService.Operations.Get(mgr.Resolved.GcpProjectId, op.Name).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("failed to get delete operation status: %w", err)
		}
	}

	return nil
}

func DefineTargetInstance(cfg *config.Config, app *nais_io_v1alpha1.Application) (*nais_io_v1.CloudSqlInstance, error) {
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
	}

	return targetInstance, nil
}

func PrepareSourceInstance(ctx context.Context, cfg *config.Config, mgr *common_main.Manager) error {
	getInstanceCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	var targetSqlInstance *v1beta1.SQLInstance
	var err error
	for targetSqlInstance == nil || targetSqlInstance.Status.PublicIpAddress == nil {
		mgr.Logger.Info("waiting for target instance to be ready")
		time.Sleep(5 * time.Second)
		targetSqlInstance, err = mgr.SqlInstanceClient.Get(getInstanceCtx, mgr.Resolved.Target.Name)
		if err != nil {
			if !k8s_errors.IsNotFound(err) {
				return err
			}
		}
	}

	mgr.Resolved.Target.Ip = *targetSqlInstance.Status.PublicIpAddress

	outgoingIp := mgr.Resolved.Target.Ip
	for _, address := range targetSqlInstance.Status.IpAddress {
		if *address.Type == "OUTGOING" {
			outgoingIp = *address.IpAddress
		}
	}

	mgr.Logger.Info("preparing source instance for migration")

	sourceSqlInstance, err := mgr.SqlInstanceClient.Get(ctx, mgr.Resolved.Source.Name)
	if err != nil {
		return err
	}

	authNetwork := v1beta1.InstanceAuthorizedNetworks{
		Name:  &mgr.Resolved.Target.Name,
		Value: fmt.Sprintf("%s/32", outgoingIp),
	}
	sourceSqlInstance.Spec.Settings.IpConfiguration.AuthorizedNetworks = appendAuthNetIfNotExists(sourceSqlInstance, authNetwork)

	if cfg.Development.AddAuthNetwork {
		authNetwork, err = createDevelopmentAuthNetwork()
		if err != nil {
			return err
		}

		targetSqlInstance.Spec.Settings.IpConfiguration.AuthorizedNetworks = appendAuthNetIfNotExists(targetSqlInstance, authNetwork)
	}

	setFlag(sourceSqlInstance, "cloudsql.enable_pglogical")
	setFlag(sourceSqlInstance, "cloudsql.logical_decoding")

	_, err = mgr.SqlInstanceClient.Update(ctx, sourceSqlInstance)
	if err != nil {
		return err
	}

	time.Sleep(5 * time.Second)
	updatedSqlInstance, err := mgr.SqlInstanceClient.Get(ctx, mgr.Resolved.Source.Name)
	if err != nil {
		return err
	}

	for updatedSqlInstance.Status.Conditions[0].Status != "True" {
		mgr.Logger.Info("waiting for source instance to be ready")
		time.Sleep(3 * time.Second)
		updatedSqlInstance, err = mgr.SqlInstanceClient.Get(ctx, mgr.Resolved.Source.Name)
		if err != nil {
			return err
		}

	}
	mgr.Logger.Info("source instance prepared for migration")
	return nil
}

func PrepareTargetInstance(ctx context.Context, cfg *config.Config, mgr *common_main.Manager) error {
	getInstanceCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	targetSqlInstance, err := mgr.SqlInstanceClient.Get(getInstanceCtx, mgr.Resolved.Target.Name)
	if err != nil {
		if !k8s_errors.IsNotFound(err) {
			return err
		}
	}

	mgr.Logger.Info("preparing target instance for migration")

	targetSqlInstance.Spec.Settings.BackupConfiguration.Enabled = ptr.To(false)

	if cfg.Development.AddAuthNetwork {
		var authNetwork v1beta1.InstanceAuthorizedNetworks
		authNetwork, err = createDevelopmentAuthNetwork()
		if err != nil {
			return err
		}

		targetSqlInstance.Spec.Settings.IpConfiguration.AuthorizedNetworks = appendAuthNetIfNotExists(targetSqlInstance, authNetwork)
	}

	_, err = mgr.SqlInstanceClient.Update(ctx, targetSqlInstance)
	if err != nil {
		return err
	}

	time.Sleep(5 * time.Second)
	updatedSqlInstance, err := mgr.SqlInstanceClient.Get(ctx, mgr.Resolved.Target.Name)
	if err != nil {
		return err
	}

	for updatedSqlInstance.Status.Conditions[0].Status != "True" {
		mgr.Logger.Info("waiting for target instance to be ready")
		time.Sleep(3 * time.Second)
		updatedSqlInstance, err = mgr.SqlInstanceClient.Get(ctx, mgr.Resolved.Target.Name)
		if err != nil {
			return err
		}
	}

	mgr.Logger.Info("target instance prepared for migration")
	return nil
}

func UpdateTargetInstanceAfterPromotion(ctx context.Context, mgr *common_main.Manager) error {
	targetSqlInstance, err := mgr.SqlInstanceClient.Get(ctx, mgr.Resolved.Target.Name)
	if err != nil {
		return fmt.Errorf("failed to get target instance: %w", err)
	}

	targetSqlInstance.Spec.InstanceType = ptr.To("CLOUD_SQL_INSTANCE")
	targetSqlInstance.Spec.MasterInstanceRef = nil

	_, err = mgr.SqlInstanceClient.Update(ctx, targetSqlInstance)
	if err != nil {
		return err
	}

	time.Sleep(5 * time.Second)
	updatedSqlInstance, err := mgr.SqlInstanceClient.Get(ctx, mgr.Resolved.Target.Name)
	if err != nil {
		return err
	}

	for updatedSqlInstance.Status.Conditions[0].Status != "True" {
		mgr.Logger.Info("waiting for target instance to be ready")
		time.Sleep(3 * time.Second)
		updatedSqlInstance, err = mgr.SqlInstanceClient.Get(ctx, mgr.Resolved.Target.Name)
		if err != nil {
			return err
		}
	}

	mgr.Logger.Info("target instance updated after promotion")
	return nil
}

func DeleteInstance(ctx context.Context, instanceName string, mgr *common_main.Manager) error {
	instancesService := mgr.SqlAdminService.Instances

	mgr.Logger.Debug("checking for existence before deletion")
	_, err := instancesService.Get(mgr.Resolved.GcpProjectId, instanceName).Context(ctx).Do()
	if err != nil {
		var ae *googleapi.Error
		if errors.As(err, &ae) && ae.Code == http.StatusNotFound {
			mgr.Logger.Info("instance not found, skipping deletion")
			return nil
		}
		return fmt.Errorf("failed to get instance: %w", err)
	}

	mgr.Logger.Info("deleting instance", "name", instanceName)
	_, err = instancesService.Delete(mgr.Resolved.GcpProjectId, instanceName).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to delete instance: %w", err)
	}
	return nil
}

func createDevelopmentAuthNetwork() (v1beta1.InstanceAuthorizedNetworks, error) {
	outgoingIp, err := getOutgoingIp()
	if err != nil {
		return v1beta1.InstanceAuthorizedNetworks{}, err
	}
	name, err := getDeveloperName()
	if err != nil {
		return v1beta1.InstanceAuthorizedNetworks{}, err
	}

	authNetwork := v1beta1.InstanceAuthorizedNetworks{
		Name:  &name,
		Value: fmt.Sprintf("%s/32", outgoingIp),
	}
	return authNetwork, nil
}

func getDeveloperName() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}

	h, err := os.Hostname()
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s@%s", u.Username, h), nil
}

func getOutgoingIp() (string, error) {
	httpClient := http.Client{
		Timeout: 15 * time.Second,
	}
	resp, err := httpClient.Get("https://api.ipify.org")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

func appendAuthNetIfNotExists(sqlInstance *v1beta1.SQLInstance, authNetwork v1beta1.InstanceAuthorizedNetworks) []v1beta1.InstanceAuthorizedNetworks {
	for _, network := range sqlInstance.Spec.Settings.IpConfiguration.AuthorizedNetworks {
		if network.Value == authNetwork.Value {
			return sqlInstance.Spec.Settings.IpConfiguration.AuthorizedNetworks
		}
	}

	return append(sqlInstance.Spec.Settings.IpConfiguration.AuthorizedNetworks, authNetwork)
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
