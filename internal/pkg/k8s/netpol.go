package k8s

import (
	"context"
	"fmt"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/nais/cloudsql-migrator/internal/pkg/resolved"
	v1 "k8s.io/api/networking/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"os"
)

func CreateNetworkPolicy(ctx context.Context, cfg *config.Config, source *resolved.Instance, target *resolved.Instance, mgr *common_main.Manager) error {
	v := os.Getenv("KUBERNETES_SERVICE_HOST")
	if v == "" {
		return nil
	}

	helperName, err := common_main.HelperName(cfg.ApplicationName)
	if err != nil {
		return err
	}

	netpol := &v1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      helperName,
			Namespace: cfg.Namespace,
			Labels: map[string]string{
				"app":                      cfg.ApplicationName,
				"team":                     cfg.Namespace,
				"migrator.nais.io/cleanup": cfg.ApplicationName,
			},
		},
		Spec: v1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					// This label must match the label on the job running the migration, as set by nais-cli
					"migrator.nais.io/migration-name": fmt.Sprintf("migration-%s-%s", cfg.ApplicationName, target.Name),
				},
			},
			Egress: []v1.NetworkPolicyEgressRule{{
				To: []v1.NetworkPolicyPeer{
					{
						IPBlock: &v1.IPBlock{
							CIDR: fmt.Sprintf("%s/32", source.PrimaryIp),
						},
					},
					{
						IPBlock: &v1.IPBlock{
							CIDR: fmt.Sprintf("%s/32", target.PrimaryIp),
						},
					},
				},
			}},
			PolicyTypes: []v1.PolicyType{v1.PolicyTypeEgress},
		},
	}

	_, err = mgr.K8sClient.NetworkingV1().NetworkPolicies(cfg.Namespace).Create(ctx, netpol, metav1.CreateOptions{})
	if err != nil {
		if k8s_errors.IsAlreadyExists(err) {
			_, err = mgr.K8sClient.NetworkingV1().NetworkPolicies(cfg.Namespace).Update(ctx, netpol, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("failed to update network policy: %w", err)
			}
		} else {
			return fmt.Errorf("failed to create network policy: %w", err)
		}
	}

	return nil
}
