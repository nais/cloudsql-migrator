package instance

import (
	"context"
	"fmt"
	"github.com/GoogleCloudPlatform/k8s-config-connector/pkg/clients/generated/apis/k8s/v1alpha1"
	"github.com/GoogleCloudPlatform/k8s-config-connector/pkg/clients/generated/apis/sql/v1beta1"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config/setup"
	"github.com/nais/cloudsql-migrator/internal/pkg/resolved"
	"github.com/nais/liberator/pkg/namegen"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"os"
	"time"
)

const (
	CertPath     = "/tmp/client.crt"
	KeyPath      = "/tmp/client.key"
	RootCertPath = "/tmp/root.crt"
)

func CreateSslCert(ctx context.Context, cfg *setup.Config, mgr *common_main.Manager, instance string, sslCert *resolved.SslCert) error {
	helperName, err := namegen.ShortName(fmt.Sprintf("migrator-%s", instance), 63)
	if err != nil {
		return err
	}

	sqlSslCert, err := mgr.SqlSslCertClient.Get(ctx, helperName)
	if errors.IsNotFound(err) {
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
				CommonName: "test",
				InstanceRef: v1alpha1.ResourceRef{
					Name:      instance,
					Namespace: cfg.Namespace,
				},
			},
		})
	}

	if err != nil {
		return err
	}

	for sqlSslCert.Status.Cert == nil || sqlSslCert.Status.PrivateKey == nil || sqlSslCert.Status.ServerCaCert == nil {
		time.Sleep(3 * time.Second)
		sqlSslCert, err = mgr.SqlSslCertClient.Get(ctx, sqlSslCert.Name)
		if err != nil {
			return err
		}
		mgr.Logger.Info("Waiting for SQLSSLCert to be ready")
	}

	sslCert.SslCaCert = *sqlSslCert.Status.ServerCaCert
	sslCert.SslClientCert = *sqlSslCert.Status.Cert
	sslCert.SslClientKey = *sqlSslCert.Status.PrivateKey

	err = createTempFiles(&mgr.Resolved.SourceSslCert.SslClientCert, &mgr.Resolved.SourceSslCert.SslClientKey, &mgr.Resolved.SourceSslCert.SslCaCert)
	if err != nil {
		return err
	}
	mgr.Logger.Info(fmt.Sprintf("ssl certificate for %s created successfully", instance))

	return nil
}

func createTempFiles(cert, key, rootCert *string) error {
	err := os.WriteFile(CertPath, []byte(*cert), 0644)
	if err != nil {
		return err
	}

	err = os.WriteFile(KeyPath, []byte(*key), 0600)
	if err != nil {
		return err
	}

	err = os.WriteFile(RootCertPath, []byte(*rootCert), 0644)
	if err != nil {
		return err
	}

	return nil
}
