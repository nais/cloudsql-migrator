package instance

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/GoogleCloudPlatform/k8s-config-connector/pkg/clients/generated/apis/k8s/v1alpha1"
	"github.com/GoogleCloudPlatform/k8s-config-connector/pkg/clients/generated/apis/sql/v1beta1"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/nais/cloudsql-migrator/internal/pkg/resolved"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type CertPaths struct {
	RootCertPath string
	CertPath     string
	KeyPath      string
}

func CreateSslCert(ctx context.Context, cfg *config.Config, mgr *common_main.Manager, instance string, sslCert *resolved.SslCert) (*CertPaths, error) {
	helperName, err := common_main.HelperName(instance)
	if err != nil {
		return nil, err
	}

	logger := mgr.Logger.With("instance", instance, "certName", helperName)

	sqlSslCert, err := mgr.SqlSslCertClient.Get(ctx, helperName)
	if errors.IsNotFound(err) {
		logger.Info("creating new ssl certificate")
		sqlSslCert, err = mgr.SqlSslCertClient.Create(ctx, &v1beta1.SQLSSLCert{
			TypeMeta: v1.TypeMeta{
				APIVersion: "sql.cnrm.cloud.google.com/v1beta1",
				Kind:       "SQLSSLCert",
			},
			ObjectMeta: v1.ObjectMeta{
				Name:      helperName,
				Namespace: cfg.Namespace,
				Labels: map[string]string{
					"app":                      cfg.ApplicationName,
					"team":                     cfg.Namespace,
					"migrator.nais.io/cleanup": cfg.ApplicationName,
				},
			},
			Spec: v1beta1.SQLSSLCertSpec{
				CommonName: helperName,
				InstanceRef: v1alpha1.ResourceRef{
					Name:      instance,
					Namespace: cfg.Namespace,
				},
			},
		})
	}

	if err != nil {
		return nil, err
	}

	for sqlSslCert.Status.Cert == nil || sqlSslCert.Status.PrivateKey == nil || sqlSslCert.Status.ServerCaCert == nil {
		time.Sleep(3 * time.Second)
		sqlSslCert, err = mgr.SqlSslCertClient.Get(ctx, sqlSslCert.Name)
		if err != nil {
			return nil, err
		}
		logger.Info("Waiting for SQLSSLCert to be ready")
	}

	sslCert.SslCaCert = *sqlSslCert.Status.ServerCaCert
	sslCert.SslClientCert = *sqlSslCert.Status.Cert
	sslCert.SslClientKey = *sqlSslCert.Status.PrivateKey

	rootCertPath, err := createTempFile(sslCert.SslCaCert, "root.crt")
	if err != nil {
		return nil, fmt.Errorf("failed to create root cert file: %w", err)
	}

	certPath, err := createTempFile(sslCert.SslClientCert, "client.crt")
	if err != nil {
		return nil, fmt.Errorf("failed to create cert file: %w", err)
	}

	keyPath, err := createTempFile(sslCert.SslClientKey, "client.key")
	if err != nil {
		return nil, fmt.Errorf("failed to create key file: %w", err)
	}

	logger.Info("ssl certificate created successfully")

	return &CertPaths{
		RootCertPath: rootCertPath,
		CertPath:     certPath,
		KeyPath:      keyPath,
	}, nil
}

func createTempFile(data, filename string) (string, error) {
	f, err := os.CreateTemp("", filename)
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}

	_, err = f.WriteString(data)
	if err != nil {
		return "", fmt.Errorf("failed to write to temp file: %w", err)
	}

	return f.Name(), nil
}

func DeleteSslCertByCommonName(ctx context.Context, instanceName, commonName string, gcpProject *resolved.GcpProject, mgr *common_main.Manager) error {
	sslCertsService := mgr.SqlAdminService.SslCerts
	operationsService := mgr.SqlAdminService.Operations

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	mgr.Logger.Info("listing ssl certs", "instance", instanceName)
	listResponse, err := sslCertsService.List(gcpProject.Id, instanceName).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to list ssl certs: %w", err)
	}

	for _, item := range listResponse.Items {
		if item.CommonName == commonName {
			mgr.Logger.Info("deleting ssl certificate", "commonName", commonName)
			op, err := sslCertsService.Delete(gcpProject.Id, instanceName, item.Sha1Fingerprint).Context(ctx).Do()
			for op.Status != "DONE" {
				time.Sleep(1 * time.Second)
				op, err = operationsService.Get(gcpProject.Id, op.Name).Context(ctx).Do()
				if err != nil {
					return fmt.Errorf("failed to get ssl cert delete operation status: %w", err)
				}
			}
			return nil
		}
	}

	mgr.Logger.Warn("ssl cert not found", "commonName", commonName)
	return nil
}
