package netpol

import (
	"context"
	"fmt"
	"os"

	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/nais/cloudsql-migrator/internal/pkg/resolved"
	v1 "k8s.io/api/networking/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func CreateNetworkPolicy(ctx context.Context, cfg *config.Config, source *resolved.Instance, target *resolved.Instance, mgr *common_main.Manager) error {
	v := os.Getenv("KUBERNETES_SERVICE_HOST")
	if v == "" {
		mgr.Logger.Info("not running in kubernetes, skipping network policy creation")
		return nil
	}

	netpol := &v1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("migration-%s-%s", cfg.ApplicationName, target.Name),
			Namespace: cfg.Namespace,
			Labels: map[string]string{
				"app":                       cfg.ApplicationName,
				"team":                      cfg.Namespace,
				"migrator.nais.io/finalize": cfg.ApplicationName,
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
					makeIPBlock(source.PrimaryIp),
					makeIPBlock(target.PrimaryIp),
					// IPs for api.ipify.org
					makeIPBlock("104.26.13.205"),
					makeIPBlock("104.26.12.205"),
					makeIPBlock("172.67.74.152"),
				},
			}},
			PolicyTypes: []v1.PolicyType{v1.PolicyTypeEgress},
		},
	}

	mgr.Logger.Info("creating network policy", "name", netpol.Name)
	_, err := mgr.K8sClient.NetworkingV1().NetworkPolicies(cfg.Namespace).Create(ctx, netpol, metav1.CreateOptions{})
	if err != nil {
		if k8s_errors.IsAlreadyExists(err) {
			mgr.Logger.Info("network policy already exists, updating", "name", netpol.Name)
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

func makeIPBlock(ip string) v1.NetworkPolicyPeer {
	return v1.NetworkPolicyPeer{
		IPBlock: &v1.IPBlock{
			CIDR: fmt.Sprintf("%s/32", ip),
		},
	}
}
