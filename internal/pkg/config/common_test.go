package config_test

import (
	"context"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sethvargo/go-envconfig"
	"k8s.io/utils/ptr"
)

const (
	appName            = "myapp"
	namespace          = "mynamespace"
	targetInstanceName = "myinstance"
)

var _ = Describe("Common", func() {
	When("no environment variables are set", func() {
		It("should fail", func() {
			cfg := &config.Config{}
			err := envconfig.ProcessWith(context.Background(), &envconfig.Config{
				Target:   cfg,
				Lookuper: envconfig.MapLookuper(map[string]string{}),
			})
			Expect(err).To(HaveOccurred())
		})
	})

	When("only required environment variables are set", func() {
		It("should not fail, and set the correct values", func() {
			cfg := &config.Config{}
			err := envconfig.ProcessWith(context.Background(), &envconfig.Config{
				Target: cfg,
				Lookuper: envconfig.MapLookuper(map[string]string{
					"APP_NAME":             appName,
					"NAMESPACE":            namespace,
					"TARGET_INSTANCE_NAME": targetInstanceName,
				}),
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.ApplicationName).To(Equal(appName))
			Expect(cfg.Namespace).To(Equal(namespace))
			Expect(cfg.TargetInstance.Name).To(Equal(targetInstanceName))
		})

		It("should result in a nil pointer for optional bool value", func() {
			cfg := &config.Config{}
			err := envconfig.ProcessWith(context.Background(), &envconfig.Config{
				Target: cfg,
				Lookuper: envconfig.MapLookuper(map[string]string{
					"APP_NAME":             appName,
					"NAMESPACE":            namespace,
					"TARGET_INSTANCE_NAME": targetInstanceName,
				}),
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.TargetInstance.DiskAutoresize).To(BeNil())
		})
	})

	When("all regular environment variables are set", func() {
		It("should not fail, and set the correct values", func() {
			cfg := &config.Config{}
			err := envconfig.ProcessWith(context.Background(), &envconfig.Config{
				Target: cfg,
				Lookuper: envconfig.MapLookuper(map[string]string{
					"APP_NAME":                        appName,
					"NAMESPACE":                       namespace,
					"TARGET_INSTANCE_NAME":            targetInstanceName,
					"TARGET_INSTANCE_TIER":            "db-f1-micro",
					"TARGET_INSTANCE_DISK_SIZE":       "10",
					"TARGET_INSTANCE_TYPE":            "POSTGRES_16",
					"TARGET_INSTANCE_DISK_AUTORESIZE": "true",
				}),
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.ApplicationName).To(Equal(appName))
			Expect(cfg.Namespace).To(Equal(namespace))
			Expect(cfg.TargetInstance.Name).To(Equal(targetInstanceName))
			Expect(cfg.TargetInstance.Tier).To(Equal("db-f1-micro"))
			Expect(cfg.TargetInstance.DiskSize).To(Equal(10))
			Expect(cfg.TargetInstance.Type).To(Equal("POSTGRES_16"))
			Expect(cfg.TargetInstance.DiskAutoresize).To(Equal(ptr.To(true)))
		})
	})

	When("disk autoresize is set to false", func() {
		It("should not fail, and set the correct values", func() {
			cfg := &config.Config{}
			err := envconfig.ProcessWith(context.Background(), &envconfig.Config{
				Target: cfg,
				Lookuper: envconfig.MapLookuper(map[string]string{
					"APP_NAME":                        appName,
					"NAMESPACE":                       namespace,
					"TARGET_INSTANCE_NAME":            targetInstanceName,
					"TARGET_INSTANCE_DISK_AUTORESIZE": "false",
				}),
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.TargetInstance.DiskAutoresize).To(Equal(ptr.To(false)))
		})
	})
})
