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

	"github.com/google/uuid"
	"github.com/sethvargo/go-retry"

	"github.com/GoogleCloudPlatform/k8s-config-connector/pkg/clients/generated/apis/sql/v1beta1"
	_ "github.com/lib/pq"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/nais/cloudsql-migrator/internal/pkg/resolved"
	nais_io_v1 "github.com/nais/liberator/pkg/apis/nais.io/v1"
	nais_io_v1alpha1 "github.com/nais/liberator/pkg/apis/nais.io/v1alpha1"
	"golang.org/x/sync/errgroup"
	"google.golang.org/api/googleapi"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

const (
	dummyAppImage              = "europe-north1-docker.pkg.dev/nais-io/nais/images/kafka-debug:latest"
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
		var correlationUUID uuid.UUID
		correlationUUID, err = uuid.NewRandom()
		if err != nil {
			return nil, fmt.Errorf("failed to generate correlation ID: %w", err)
		}
		correlationID := correlationUUID.String()

		dummyApp = &nais_io_v1alpha1.Application{
			TypeMeta: app.TypeMeta,
			ObjectMeta: metav1.ObjectMeta{
				Name:      helperName,
				Namespace: cfg.Namespace,
				Labels: map[string]string{
					"app":                       app.Name,
					"team":                      cfg.Namespace,
					"migrator.nais.io/finalize": app.Name,
				},
				Annotations: map[string]string{
					nais_io_v1.DeploymentCorrelationIDAnnotation: correlationID,
					"migrator.nais.io/source-instance":           source.Name,
					"migrator.nais.io/target-instance":           cfg.TargetInstance.Name,
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
		dummyApp, err = mgr.AppClient.Create(ctx, dummyApp)
	}
	if err != nil {
		return nil, err
	}

	correlationID, ok := dummyApp.Annotations[nais_io_v1.DeploymentCorrelationIDAnnotation]
	if !ok {
		return nil, fmt.Errorf("missing correlation ID in dummy app %s", helperName)
	}
	mgr.Logger.Info("started creation of target instance", "helperApp", helperName)
	for dummyApp.Status.CorrelationID != correlationID || dummyApp.Status.SynchronizationState != "RolloutComplete" {
		mgr.Logger.Info("waiting for dummy app rollout")
		time.Sleep(5 * time.Second)
		dummyApp, err = mgr.AppClient.Get(ctx, helperName)
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

func PrepareSourceInstance(ctx context.Context, source *resolved.Instance, target *resolved.Instance, mgr *common_main.Manager) error {
	mgr.Logger.Info("preparing source instance for migration")

	b := retry.NewConstant(1 * time.Second)
	b = retry.WithMaxDuration(5*time.Minute, b)

	err := retry.Do(ctx, b, func(ctx context.Context) error {
		sourceSqlInstance, err := mgr.SqlInstanceClient.Get(ctx, source.Name)
		if err != nil {
			mgr.Logger.Warn("failed to get source instance, retrying", "error", err)
			return retry.RetryableError(err)
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
			if k8s_errors.IsConflict(err) {
				mgr.Logger.Warn("retrying update of source instance")
				return retry.RetryableError(err)
			}
			return err
		}

		mgr.Logger.Info("update of source instance applied")
		return nil
	})

	if err != nil {
		mgr.Logger.Error("failed to prepare source instance", "error", err)
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

func WaitForSQLDatabaseResourceToGoAway(ctx context.Context, appName string, mgr *common_main.Manager) error {
	mgr.Logger.Info("waiting for SQLDatabase resource to go away...")

	b := retry.NewConstant(5 * time.Second)
	b = retry.WithMaxDuration(5*time.Minute, b)

	err := retry.Do(ctx, b, func(ctx context.Context) error {
		exists, err := mgr.SqlDatabaseClient.ExistsByLabel(ctx, fmt.Sprintf("app=%s", appName))
		if err != nil {
			mgr.Logger.Warn("failed to list SQLDatabase resource, retrying...", "error", err.Error())
			return retry.RetryableError(err)
		}
		if exists {
			mgr.Logger.Info("waiting for SQLDatabase resource to go away...")
			return retry.RetryableError(errors.New("resource still exists"))
		}
		mgr.Logger.Info("SQLDatabase resource has been deleted")
		return nil
	})
	if err == nil {
		mgr.Logger.Info("resource has been deleted")
	}
	return err
}

func WaitForCnrmResourcesToGoAway(ctx context.Context, instanceName, applicationName string, mgr *common_main.Manager) error {
	logger := mgr.Logger.With("instance_name", instanceName)
	logger.Info("waiting for relevant CNRM resources to go away...")

	type resource[ struct {
		kind   string
		getter func() (metav1.Object, error)
	}
	resources := []resource{
		{
			"SQLInstance",
			func() (metav1.Object, error) {
				instance, err := mgr.SqlInstanceClient.Get(ctx, instanceName)
				if err != nil {
					return nil, err
				}
				return instance.GetObjectMeta(), nil
			},
		},
		{
			"SQLUser",
			func() (metav1.Object, error){
				user, err := mgr.SqlUserClient.Get(ctx, instanceName)
				if err != nil {
					return nil, err
				}
				return user.GetObjectMeta(), nil
			},
		},
	}

	g, ctx := errgroup.WithContext(ctx)

	for _, r := range resources {
		g.Go(func() error {
			b := retry.NewConstant(5 * time.Second)
			b = retry.WithMaxDuration(5*time.Minute, b)

			err := retry.Do(ctx, b, func(ctx context.Context) error {
				obj, err := r.getter()
				if err == nil {
					for _, ref := range obj.GetOwnerReferences() {
						if ref.Name == applicationName {
							logger.Info("resource already transferred to target application", "kind", r.kind)
							return nil
						}
					}

					logger.Info("waiting for resource to go away...", "kind", r.kind)
					return retry.RetryableError(errors.New("resource still exists"))
				}
				if k8s_errors.IsNotFound(err) {
					mgr.Logger.Info("resource has been deleted", "kind", r.kind)
					return nil
				}
				logger.Warn("failed to get resource, retrying...", "kind", r.kind, "error", err.Error())
				return retry.RetryableError(err)
			})
			if err != nil {
				logger.Error("resource refuses to go away", "kind", r.kind, "error", err.Error())
			}
			return err
		})
	}

	return g.Wait()
}

func PrepareTargetInstance(ctx context.Context, target *resolved.Instance, mgr *common_main.Manager) error {
	mgr.Logger.Info("preparing target instance for migration")

	b := retry.NewConstant(1 * time.Second)
	b = retry.WithMaxDuration(15*time.Minute, b)

	err := retry.Do(ctx, b, func(ctx context.Context) error {
		targetSqlInstance, err := mgr.SqlInstanceClient.Get(ctx, target.Name)
		if err != nil {
			// Target is assumed to exist, so any error here is fatal
			return err
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
			if k8s_errors.IsConflict(err) {
				mgr.Logger.Warn("retrying update of target instance", "error", err)
				return retry.RetryableError(err)
			}
			return err
		}

		mgr.Logger.Info("update of target instance applied")
		return nil
	})
	if err != nil {
		mgr.Logger.Error("failed to prepare target instance", "error", err)
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

func UpdateTargetInstanceAfterPromotion(ctx context.Context, target *resolved.Instance, mgr *common_main.Manager) error {
	mgr.Logger.Info("updating target instance after promotion")

	b := retry.NewConstant(1 * time.Second)
	b = retry.WithMaxDuration(5*time.Minute, b)

	err := retry.Do(ctx, b, func(ctx context.Context) error {
		targetSqlInstance, err := mgr.SqlInstanceClient.Get(ctx, target.Name)
		if err != nil {
			return fmt.Errorf("failed to get target instance: %w", err)
		}

		targetSqlInstance.Spec.InstanceType = ptr.To("CLOUD_SQL_INSTANCE")
		targetSqlInstance.Spec.MasterInstanceRef = nil

		_, err = mgr.SqlInstanceClient.Update(ctx, targetSqlInstance)
		if err != nil {
			if k8s_errors.IsConflict(err) {
				mgr.Logger.Warn("retrying update of target instance", "error", err)
				return retry.RetryableError(err)
			}
			return err
		}

		mgr.Logger.Info("update of target instance applied")
		return nil
	})
	if err != nil {
		mgr.Logger.Error("failed to update target instance after promotion", "error", err)
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
	mgr.Logger.Info("deleting authorized networks")

	b := retry.NewConstant(1 * time.Second)
	b = retry.WithMaxDuration(5*time.Minute, b)

	err := retry.Do(ctx, b, func(ctx context.Context) error {
		targetSqlInstance, err := mgr.SqlInstanceClient.Get(ctx, target.Name)
		if err != nil {
			return fmt.Errorf("failed to get target instance: %w", err)
		}

		targetSqlInstance.Spec.Settings.IpConfiguration.AuthorizedNetworks = removeMigrationAuthNetwork(targetSqlInstance)

		_, err = mgr.SqlInstanceClient.Update(ctx, targetSqlInstance)
		if err != nil {
			if k8s_errors.IsConflict(err) {
				mgr.Logger.Warn("retrying update of target instance", "error", err)
				return retry.RetryableError(err)
			}
			return err
		}

		mgr.Logger.Info("update of target instance applied")
		return nil
	})
	if err != nil {
		mgr.Logger.Error("failed to cleanup authorized networks", "error", err)
	}
	return err
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
