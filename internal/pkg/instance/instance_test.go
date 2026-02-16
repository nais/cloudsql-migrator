package instance_test

import (
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/nais/cloudsql-migrator/internal/pkg/instance"
	nais_io_v1 "github.com/nais/liberator/pkg/apis/nais.io/v1"
	nais_io_v1alpha1 "github.com/nais/liberator/pkg/apis/nais.io/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

const (
	targetInstanceName = "my-target-instance-name"
	targetType         = nais_io_v1.CloudSqlInstanceTypePostgres15
	targetTier         = "db-custom-1-3840"
	targetDiskSize     = 100

	sourceInstanceName = "my-source-instance-name"
	sourceType         = nais_io_v1.CloudSqlInstanceTypePostgres14
	sourceTier         = "db-custom-2-8192"
	sourceDiskSize     = 500
)

var _ = Describe("Instance", func() {
	var app *nais_io_v1alpha1.Application

	When("source application has no instance configuration", func() {
		BeforeEach(func() {
			app = &nais_io_v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-app-name",
					Namespace: "mynamespace",
				},
				Spec: nais_io_v1alpha1.ApplicationSpec{
					Image: "my-docker-image:latest",
					GCP: &nais_io_v1.GCP{
						SqlInstances: []nais_io_v1.CloudSqlInstance{
							{
								Name: sourceInstanceName,
							},
						},
					},
				},
			}
		})

		When("defining a new instance", func() {
			Context("with no configuration", func() {
				It("should define a new instance with zero values", func() {
					instanceSettings := &config.InstanceSettings{
						Name: targetInstanceName,
					}
					target := instance.DefineInstance(instanceSettings, app)
					Expect(target.Name).To(Equal(targetInstanceName))
					Expect(target.Tier).To(BeEmpty())
					Expect(target.DiskSize).To(BeZero())
					Expect(target.Type).To(BeEmpty())
					Expect(target.DiskAutoresize).To(BeFalse())
				})
			})

			Context("with complete configuration", func() {
				It("should set the configured values on the new instance", func() {
					instanceSettings := &config.InstanceSettings{
						Name:           targetInstanceName,
						Type:           string(targetType),
						Tier:           targetTier,
						DiskSize:       targetDiskSize,
						DiskAutoresize: ptr.To(false),
					}
					target := instance.DefineInstance(instanceSettings, app)
					Expect(target.Name).To(Equal(targetInstanceName))
					Expect(target.Tier).To(Equal(targetTier))
					Expect(target.DiskSize).To(Equal(targetDiskSize))
					Expect(target.Type).To(Equal(targetType))
					Expect(target.DiskAutoresize).To(BeFalse())
				})
			})

			Context("with disk autoresize unset", func() {
				var instanceSettings *config.InstanceSettings

				BeforeEach(func() {
					instanceSettings = &config.InstanceSettings{
						Name:           targetInstanceName,
						Type:           string(targetType),
						Tier:           targetTier,
						DiskSize:       targetDiskSize,
						DiskAutoresize: nil,
					}
				})

				It("should not enable disk autoresize on the new instance", func() {
					target := instance.DefineInstance(instanceSettings, app)
					Expect(target.DiskAutoresize).To(BeFalse())
				})

				It("should set the disk size on the new instance", func() {
					target := instance.DefineInstance(instanceSettings, app)
					Expect(target.DiskSize).To(Equal(targetDiskSize))
				})
			})

			Context("with disk autoresize enabled", func() {
				var instanceSettings *config.InstanceSettings

				BeforeEach(func() {
					instanceSettings = &config.InstanceSettings{
						Name:           targetInstanceName,
						Type:           string(targetType),
						Tier:           targetTier,
						DiskAutoresize: ptr.To(true),
					}
				})

				It("should enable disk autoresize on the new instance", func() {
					target := instance.DefineInstance(instanceSettings, app)
					Expect(target.DiskAutoresize).To(BeTrue())
				})

				It("should not set the disk size on the new instance", func() {
					target := instance.DefineInstance(instanceSettings, app)
					Expect(target.DiskSize).To(BeZero())
				})
			})
		})
	})

	When("source application has all instance configuration, no autoresize", func() {
		BeforeEach(func() {
			app = &nais_io_v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-app-name",
					Namespace: "mynamespace",
				},
				Spec: nais_io_v1alpha1.ApplicationSpec{
					Image: "my-docker-image:latest",
					GCP: &nais_io_v1.GCP{
						SqlInstances: []nais_io_v1.CloudSqlInstance{
							{
								Name:           sourceInstanceName,
								Type:           sourceType,
								Tier:           sourceTier,
								DiskSize:       sourceDiskSize,
								DiskAutoresize: false,
							},
						},
					},
				},
			}
		})

		When("defining a new instance", func() {
			Context("with no configuration", func() {
				It("should define a new instance with the source values", func() {
					instanceSettings := &config.InstanceSettings{
						Name: targetInstanceName,
					}
					target := instance.DefineInstance(instanceSettings, app)
					Expect(target.Name).To(Equal(targetInstanceName))
					Expect(target.Tier).To(Equal(sourceTier))
					Expect(target.DiskSize).To(Equal(sourceDiskSize))
					Expect(target.Type).To(Equal(sourceType))
					Expect(target.DiskAutoresize).To(BeFalse())
				})
			})

			Context("with complete configuration", func() {
				It("should set the configured values on the new instance", func() {
					instanceSettings := &config.InstanceSettings{
						Name:           targetInstanceName,
						Type:           string(targetType),
						Tier:           targetTier,
						DiskSize:       targetDiskSize,
						DiskAutoresize: ptr.To(false),
					}
					target := instance.DefineInstance(instanceSettings, app)
					Expect(target.Name).To(Equal(targetInstanceName))
					Expect(target.Tier).To(Equal(targetTier))
					Expect(target.DiskSize).To(Equal(targetDiskSize))
					Expect(target.Type).To(Equal(targetType))
					Expect(target.DiskAutoresize).To(BeFalse())
				})
			})

			Context("with disk autoresize unset", func() {
				var instanceSettings *config.InstanceSettings

				BeforeEach(func() {
					instanceSettings = &config.InstanceSettings{
						Name:           targetInstanceName,
						Type:           string(targetType),
						Tier:           targetTier,
						DiskSize:       targetDiskSize,
						DiskAutoresize: nil,
					}
				})

				It("should not enable disk autoresize on the new instance", func() {
					target := instance.DefineInstance(instanceSettings, app)
					Expect(target.DiskAutoresize).To(BeFalse())
				})

				It("should set the disk size on the new instance", func() {
					target := instance.DefineInstance(instanceSettings, app)
					Expect(target.DiskSize).To(Equal(targetDiskSize))
				})
			})

			Context("with disk autoresize enabled", func() {
				var instanceSettings *config.InstanceSettings

				BeforeEach(func() {
					instanceSettings = &config.InstanceSettings{
						Name:           targetInstanceName,
						Type:           string(targetType),
						Tier:           targetTier,
						DiskAutoresize: ptr.To(true),
					}
				})

				It("should enable disk autoresize on the new instance", func() {
					target := instance.DefineInstance(instanceSettings, app)
					Expect(target.DiskAutoresize).To(BeTrue())
				})

				It("should not set the disk size on the new instance", func() {
					target := instance.DefineInstance(instanceSettings, app)
					Expect(target.DiskSize).To(BeZero())
				})
			})
		})
	})

	When("source application has pgaudit flags", func() {
		BeforeEach(func() {
			app = &nais_io_v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-app-name",
					Namespace: "mynamespace",
				},
				Spec: nais_io_v1alpha1.ApplicationSpec{
					Image: "my-docker-image:latest",
					GCP: &nais_io_v1.GCP{
						SqlInstances: []nais_io_v1.CloudSqlInstance{
							{
								Name: sourceInstanceName,
								Tier: sourceTier,
								Flags: []nais_io_v1.CloudSqlFlag{
									{Name: "cloudsql.enable_pgaudit", Value: "on"},
									{Name: "pgaudit.log", Value: "write,ddl,role"},
									{Name: "pgaudit.log_parameter", Value: "on"},
									{Name: "cloudsql.logical_decoding", Value: "on"},
								},
							},
						},
					},
				},
			}
		})

		It("should copy flags via DefineInstance", func() {
			instanceSettings := &config.InstanceSettings{
				Name: targetInstanceName,
			}
			target := instance.DefineInstance(instanceSettings, app)
			Expect(target.Flags).To(HaveLen(4))
		})

		It("should strip pgaudit logging flags but keep cloudsql.enable_pgaudit", func() {
			instanceSettings := &config.InstanceSettings{
				Name: targetInstanceName,
			}
			target := instance.DefineInstance(instanceSettings, app)
			stripped := instance.StripPgAuditFlags(target)
			Expect(stripped).To(BeTrue())
			Expect(target.Flags).To(HaveLen(2))
			names := []string{target.Flags[0].Name, target.Flags[1].Name}
			Expect(names).To(ContainElements("cloudsql.enable_pgaudit", "cloudsql.logical_decoding"))
		})

		It("should return false when no pgaudit flags present", func() {
			instanceSettings := &config.InstanceSettings{
				Name: targetInstanceName,
			}
			target := instance.DefineInstance(instanceSettings, app)
			instance.StripPgAuditFlags(target)
			stripped := instance.StripPgAuditFlags(target)
			Expect(stripped).To(BeFalse())
		})
	})
})
