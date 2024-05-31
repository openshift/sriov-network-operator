package leaderelection

import (
	"time"

	"k8s.io/client-go/tools/leaderelection"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
)

const (
	// Defaults follow conventions
	// https://github.com/openshift/enhancements/blob/master/CONVENTIONS.md#high-availability
	// Impl Calculations: https://github.com/openshift/library-go/commit/7e7d216ed91c3119800219c9194e5e57113d059a
	defaultLeaseDuration = 137 * time.Second
	defaultRenewDeadline = 107 * time.Second
	defaultRetryPeriod   = 26 * time.Second
)

func GetLeaderElectionConfig(c client.Client, enabled bool) (defaultConfig leaderelection.LeaderElectionConfig) {
	defaultConfig = leaderelection.LeaderElectionConfig{
		LeaseDuration: defaultLeaseDuration,
		RenewDeadline: defaultRenewDeadline,
		RetryPeriod:   defaultRetryPeriod,
	}

	if enabled {
		isSingleNode, err := utils.IsSingleNodeCluster(c)
		if err != nil {
			log.Log.Error(err, "warning, unable to get cluster infrastructure status, using HA cluster values for leader election")
			return
		}
		if isSingleNode {
			return leaderElectionSingleNodeConfig(defaultConfig)
		}
	}
	return
}

// Default leader election for Single Node environments
// Impl Calculations:
// https://github.com/openshift/library-go/commit/2612981f3019479805ac8448b997266fc07a236a#diff-61dd95c7fd45fa18038e825205fbfab8a803f1970068157608b6b1e9e6c27248R127
func leaderElectionSingleNodeConfig(config leaderelection.LeaderElectionConfig) leaderelection.LeaderElectionConfig {
	config.LeaseDuration = 270 * time.Second
	config.RenewDeadline = 240 * time.Second
	config.RetryPeriod = 60 * time.Second
	return config
}
