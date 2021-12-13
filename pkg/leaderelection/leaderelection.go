package leaderelection

import (
	"context"
	"fmt"
	"time"

	"github.com/golang/glog"

	configv1 "github.com/openshift/api/config/v1"

	openshiftcorev1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/client-go/rest"
)

const (
	infraResourceName = "cluster"

	// Defaults follow conventions
	// https://github.com/openshift/enhancements/blob/master/CONVENTIONS.md#high-availability
	// Impl Calculations: https://github.com/openshift/library-go/commit/7e7d216ed91c3119800219c9194e5e57113d059a
	defaultLeaseDuration = 137 * time.Second
	defaultRenewDeadline = 107 * time.Second
	defaultRetryPeriod   = 26 * time.Second
)

func GetLeaderElectionConfig(restClient *rest.Config, enabled bool) (defaultConfig configv1.LeaderElection) {
	defaultConfig = configv1.LeaderElection{
		Disable:       !enabled,
		LeaseDuration: metav1.Duration{Duration: defaultLeaseDuration},
		RenewDeadline: metav1.Duration{Duration: defaultRenewDeadline},
		RetryPeriod:   metav1.Duration{Duration: defaultRetryPeriod},
	}

	if enabled {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(time.Second*3))
		defer cancel()
		infra, err := getClusterInfraStatus(ctx, restClient)
		if err != nil {
			glog.Warningf("unable to get cluster infrastructure status, using HA cluster values for leader election: %v", err)
			return
		}
		if infra != nil && infra.ControlPlaneTopology == configv1.SingleReplicaTopologyMode {
			return leaderElectionSNOConfig(defaultConfig)
		}
	}
	return
}

// Default leader election for SNO environments
// Impl Calculations:
// https://github.com/openshift/library-go/commit/2612981f3019479805ac8448b997266fc07a236a#diff-61dd95c7fd45fa18038e825205fbfab8a803f1970068157608b6b1e9e6c27248R127
func leaderElectionSNOConfig(config configv1.LeaderElection) configv1.LeaderElection {
	ret := *(&config).DeepCopy()
	ret.LeaseDuration.Duration = 270 * time.Second
	ret.RenewDeadline.Duration = 240 * time.Second
	ret.RetryPeriod.Duration = 60 * time.Second
	return ret
}

// Retrieve the cluster status, used to determine if we should use different leader election.
func getClusterInfraStatus(ctx context.Context, restClient *rest.Config) (*configv1.InfrastructureStatus, error) {
	client, err := openshiftcorev1.NewForConfig(restClient)
	if err != nil {
		return nil, err
	}
	infra, err := client.Infrastructures().Get(ctx, infraResourceName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if infra == nil {
		return nil, fmt.Errorf("getting resource Infrastructure (name: %s) succeeded but object was nil", infraResourceName)
	}
	return &infra.Status, nil
}
