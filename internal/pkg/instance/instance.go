package instance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/user"
	"strings"
	"time"

	"github.com/GoogleCloudPlatform/k8s-config-connector/pkg/clients/generated/apis/sql/v1beta1"
	_ "github.com/lib/pq"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/nais/cloudsql-migrator/internal/pkg/resolved"
	nais_io_v1 "github.com/nais/liberator/pkg/apis/nais.io/v1"
	nais_io_v1alpha1 "github.com/nais/liberator/pkg/apis/nais.io/v1alpha1"
	"google.golang.org/api/googleapi"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

const (
	dummyAppImage              = "europe-north1-docker.pkg.dev/nais-io/nais/images/kafka-debug:latest"
	updateRetries              = 3
	migrationAuthNetworkPrefix = "migrator:"
)

func CreateInstance(ctx context.Context, cfg *config.Config, source *resolved.Instance, gcpProject *resolved.GcpProject, databaseName string, mgr *common_main.Manager) (*resolved.Instance, error) {
	mgr.Logger.Info("getting source application", "name", cfg.ApplicationName)
	app, err := mgr.AppClient.Get(ctx, cfg.ApplicationName)
	if err != nil {
		return nil, err
	}

	targetInstance := DefineInstance(&cfg.TargetInstance, app)

	helperName, err := common_main.HelperName(cfg.ApplicationName)
	if err != nil {
		return nil, err
	}

	mgr.Logger.Info("get helper application", "name", helperName)
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
					"migrator.nais.io/source-instance": source.Name,
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

		mgr.Logger.Info("creating helper application", "name", helperName)
		app, err = mgr.AppClient.Create(ctx, dummyApp)
	}
	if err != nil {
		return nil, err
	}
	mgr.Logger.Info("started creation of target instance", "helperApp", helperName)
	for app.Status.SynchronizationState != "RolloutComplete" {
		mgr.Logger.Info("waiting for dummy app rollout")
		time.Sleep(5 * time.Second)
		app, err = mgr.AppClient.Get(ctx, helperName)
		if err != nil {
			return nil, err
		}
	}

	return resolved.ResolveInstance(ctx, dummyApp, mgr)
}

func DefineInstance(instanceSettings *config.InstanceSettings, app *nais_io_v1alpha1.Application) *nais_io_v1.CloudSqlInstance {
	sourceInstance := app.Spec.GCP.SqlInstances[0]
	instance := sourceInstance.DeepCopy()

	instance.Name = instanceSettings.Name
	instance.CascadingDelete = false
	if instanceSettings.Tier != "" {
		instance.Tier = instanceSettings.Tier
	}
	if instanceSettings.DiskSize != 0 {
		instance.DiskSize = instanceSettings.DiskSize
	}
	if instanceSettings.Type != "" {
		instance.Type = nais_io_v1.CloudSqlInstanceType(instanceSettings.Type)
	}

	return instance
}

func PrepareSourceInstance(ctx context.Context, cfg *config.Config, source *resolved.Instance, target *resolved.Instance, mgr *common_main.Manager) error {
	mgr.Logger.Info("preparing source instance for migration")

	err := prepareSourceInstanceWithRetries(ctx, cfg, source, target, mgr, updateRetries)
	if err != nil {
		return err
	}

	time.Sleep(1 * time.Second)
	updatedSqlInstance, err := mgr.SqlInstanceClient.Get(ctx, source.Name)
	if err != nil {
		return err
	}

	for updatedSqlInstance.Status.Conditions[0].Status != "True" {
		mgr.Logger.Info("waiting for source instance to be ready")
		time.Sleep(3 * time.Second)
		updatedSqlInstance, err = mgr.SqlInstanceClient.Get(ctx, source.Name)
		if err != nil {
			return err
		}

	}
	mgr.Logger.Info("source instance prepared for migration")
	return nil
}

func WaitForInstanceToGoAway(ctx context.Context, name string, mgr *common_main.Manager) error {
	ctx, cancel := context.WithTimeout(ctx, 1*time.Minute)
	defer cancel()

	for {
		instance, err := mgr.SqlInstanceClient.Get(ctx, name)
		if err != nil {
			if k8s_errors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("failed to get instance: %w", err)
		}
		time.Sleep(3 * time.Second)
		mgr.Logger.Info("waiting for instance to go away", "instance_waited_on", instance.Name)
	}
}

func prepareSourceInstanceWithRetries(ctx context.Context, cfg *config.Config, source *resolved.Instance, target *resolved.Instance, mgr *common_main.Manager, retries int) error {
	sourceSqlInstance, err := mgr.SqlInstanceClient.Get(ctx, source.Name)
	if err != nil {
		return err
	}

	authNetwork := v1beta1.InstanceAuthorizedNetworks{
		Name:  &target.Name,
		Value: fmt.Sprintf("%s/32", target.OutgoingIp),
	}
	sourceSqlInstance.Spec.Settings.IpConfiguration.AuthorizedNetworks = appendAuthNetIfNotExists(sourceSqlInstance, authNetwork)

	authNetwork, err = createMigratorAuthNetwork()
	if err != nil {
		return err
	}
	sourceSqlInstance.Spec.Settings.IpConfiguration.AuthorizedNetworks = appendAuthNetIfNotExists(sourceSqlInstance, authNetwork)

	setFlag(sourceSqlInstance, "cloudsql.enable_pglogical")
	setFlag(sourceSqlInstance, "cloudsql.logical_decoding")

	_, err = mgr.SqlInstanceClient.Update(ctx, sourceSqlInstance)
	if err != nil {
		if k8s_errors.IsConflict(err) && retries > 0 {
			mgr.Logger.Info("retrying update of source instance", "remaining_retries", retries)
			return prepareSourceInstanceWithRetries(ctx, cfg, source, target, mgr, retries-1)
		}
		return err
	}
	return nil
}

func PrepareTargetInstance(ctx context.Context, cfg *config.Config, target *resolved.Instance, mgr *common_main.Manager) error {
	getInstanceCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	err := prepareTargetInstanceWithRetries(getInstanceCtx, cfg, target, mgr, updateRetries)
	if err != nil {
		return err
	}

	time.Sleep(1 * time.Second)
	updatedSqlInstance, err := mgr.SqlInstanceClient.Get(ctx, target.Name)
	if err != nil {
		return err
	}

	for updatedSqlInstance.Status.Conditions[0].Status != "True" {
		mgr.Logger.Info("waiting for target instance to be ready")
		time.Sleep(3 * time.Second)
		updatedSqlInstance, err = mgr.SqlInstanceClient.Get(ctx, target.Name)
		if err != nil {
			return err
		}
	}

	mgr.Logger.Info("target instance prepared for migration")
	return nil
}

func prepareTargetInstanceWithRetries(ctx context.Context, cfg *config.Config, target *resolved.Instance, mgr *common_main.Manager, retries int) error {
	mgr.Logger.Info("preparing target instance for migration")

	targetSqlInstance, err := mgr.SqlInstanceClient.Get(ctx, target.Name)
	if err != nil {
		if !k8s_errors.IsNotFound(err) {
			return err
		}
	}

	targetSqlInstance.Spec.Settings.BackupConfiguration.Enabled = ptr.To(false)

	var authNetwork v1beta1.InstanceAuthorizedNetworks
	authNetwork, err = createMigratorAuthNetwork()
	if err != nil {
		return err
	}

	targetSqlInstance.Spec.Settings.IpConfiguration.AuthorizedNetworks = appendAuthNetIfNotExists(targetSqlInstance, authNetwork)

	mgr.Logger.Info("updating target instance", "name", target.Name)
	_, err = mgr.SqlInstanceClient.Update(ctx, targetSqlInstance)
	if err != nil {
		if k8s_errors.IsConflict(err) && retries > 0 {
			mgr.Logger.Info("retrying update of target instance", "remaining_retries", retries)
			return prepareTargetInstanceWithRetries(ctx, cfg, target, mgr, retries-1)
		}
		return err
	}
	return nil
}

func UpdateTargetInstanceAfterPromotion(ctx context.Context, target *resolved.Instance, mgr *common_main.Manager) error {
	err := updateTargetInstanceAfterPromotionWithRetries(ctx, target, mgr, updateRetries)
	if err != nil {
		return err
	}

	time.Sleep(5 * time.Second)
	updatedSqlInstance, err := mgr.SqlInstanceClient.Get(ctx, target.Name)
	if err != nil {
		return err
	}

	for updatedSqlInstance.Status.Conditions[0].Status != "True" {
		mgr.Logger.Info("waiting for target instance to be ready")
		time.Sleep(3 * time.Second)
		updatedSqlInstance, err = mgr.SqlInstanceClient.Get(ctx, target.Name)
		if err != nil {
			return err
		}
	}

	mgr.Logger.Info("target instance updated after promotion")
	return nil
}

func updateTargetInstanceAfterPromotionWithRetries(ctx context.Context, target *resolved.Instance, mgr *common_main.Manager, retries int) error {
	targetSqlInstance, err := mgr.SqlInstanceClient.Get(ctx, target.Name)
	if err != nil {
		return fmt.Errorf("failed to get target instance: %w", err)
	}

	targetSqlInstance.Spec.InstanceType = ptr.To("CLOUD_SQL_INSTANCE")
	targetSqlInstance.Spec.MasterInstanceRef = nil

	_, err = mgr.SqlInstanceClient.Update(ctx, targetSqlInstance)
	if err != nil {
		if k8s_errors.IsConflict(err) && retries > 0 {
			mgr.Logger.Info("retrying update of target instance", "remaining_retries", retries)
			return updateTargetInstanceAfterPromotionWithRetries(ctx, target, mgr, retries-1)
		}
		return err
	}
	return nil
}

func DeleteInstance(ctx context.Context, instanceName string, gcpProject *resolved.GcpProject, mgr *common_main.Manager) error {
	instancesService := mgr.SqlAdminService.Instances

	mgr.Logger.Info("checking for instance existence before deletion", "name", instanceName)
	_, err := instancesService.Get(gcpProject.Id, instanceName).Context(ctx).Do()
	if err != nil {
		var ae *googleapi.Error
		if errors.As(err, &ae) && ae.Code == http.StatusNotFound {
			mgr.Logger.Info("instance not found, skipping deletion")
			return nil
		}
		return fmt.Errorf("failed to get instance: %w", err)
	}

	mgr.Logger.Info("deleting instance", "name", instanceName)
	_, err = instancesService.Delete(gcpProject.Id, instanceName).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to delete instance: %w", err)
	}
	return nil
}

func CleanupAuthNetworks(ctx context.Context, target *resolved.Instance, mgr *common_main.Manager) error {
	return cleanupAuthNetworksWithRetries(ctx, target, mgr, updateRetries)
}

func cleanupAuthNetworksWithRetries(ctx context.Context, target *resolved.Instance, mgr *common_main.Manager, retries int) error {
	mgr.Logger.Info("deleting authorized networks")
	targetSqlInstance, err := mgr.SqlInstanceClient.Get(ctx, target.Name)
	if err != nil {
		return fmt.Errorf("failed to get target instance: %w", err)
	}

	targetSqlInstance.Spec.Settings.IpConfiguration.AuthorizedNetworks = removeMigrationAuthNetwork(targetSqlInstance)

	_, err = mgr.SqlInstanceClient.Update(ctx, targetSqlInstance)
	if err != nil {
		if k8s_errors.IsConflict(err) && retries > 0 {
			mgr.Logger.Info("retrying update of target instance", "remaining_retries", retries)
			return cleanupAuthNetworksWithRetries(ctx, target, mgr, retries-1)
		}
		return err
	}
	return nil
}

func createMigratorAuthNetwork() (v1beta1.InstanceAuthorizedNetworks, error) {
	outgoingIp, err := getOutgoingIp()
	if err != nil {
		return v1beta1.InstanceAuthorizedNetworks{}, err
	}
	name, err := getNetworkName()
	if err != nil {
		return v1beta1.InstanceAuthorizedNetworks{}, err
	}

	authNetwork := v1beta1.InstanceAuthorizedNetworks{
		Name:  &name,
		Value: fmt.Sprintf("%s/32", outgoingIp),
	}
	return authNetwork, nil
}

func getNetworkName() (string, error) {
	u, err := user.Current()
	if err != nil {
		u = &user.User{Username: "unknown"}
	}

	h, err := os.Hostname()
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s%s@%s", migrationAuthNetworkPrefix, u.Username, h), nil
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

func removeMigrationAuthNetwork(sqlInstance *v1beta1.SQLInstance) []v1beta1.InstanceAuthorizedNetworks {
	newAuthNetworks := make([]v1beta1.InstanceAuthorizedNetworks, 0)
	for _, network := range sqlInstance.Spec.Settings.IpConfiguration.AuthorizedNetworks {
		if strings.HasPrefix(migrationAuthNetworkPrefix, *network.Name) {
			continue
		}
		newAuthNetworks = append(newAuthNetworks, network)
	}
	return newAuthNetworks
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
