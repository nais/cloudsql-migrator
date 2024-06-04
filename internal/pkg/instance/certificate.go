package instance

import (
	"context"
	"fmt"
	"github.com/GoogleCloudPlatform/k8s-config-connector/pkg/clients/generated/apis/k8s/v1alpha1"
	"github.com/GoogleCloudPlatform/k8s-config-connector/pkg/clients/generated/apis/sql/v1beta1"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/nais/cloudsql-migrator/internal/pkg/resolved"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"os"
	"time"
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
