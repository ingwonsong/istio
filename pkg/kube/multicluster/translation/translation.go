// Copyright Istio Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package translation

import (
	"fmt"
	"net"
	"net/url"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"

	"istio.io/istio/pkg/asm"
	"istio.io/istio/pkg/env"
	"istio.io/istio/pkg/log"
	"istio.io/istio/pkg/monitoring"
)

var (
	ipBasedRemoteSecretsTranslatedCount = monitoring.NewGauge(
		"ip_based_remote_secrets_translated",
		"Number of remote secrets with IP for server field translated to use connect gateway endpoint",
	)
	success                   = monitoring.CreateLabel("success")
	successfulTranslations    = ipBasedRemoteSecretsTranslatedCount.With(success.Value("true"))
	failedTranslations        = ipBasedRemoteSecretsTranslatedCount.With(success.Value("false"))
	ipBasedRemoteSecretsCount = monitoring.NewGauge(
		"ip_based_remote_secrets",
		"Number of remote secrets with IP for server field",
	)

	enableTranslationCache = env.RegisterBoolVar("ENABLE_TRANSLATION_CACHE", false,
		"If enabled, attempt to translated remote secrets with IP endpoints to CGW endpoint.").Get()
)

func MaybeNewConfigHook() *ConfigHook {
	if !enableTranslationCache {
		return nil
	}
	cache, err := newIPMembershipCache()
	if err != nil {
		return nil
	}

	return &ConfigHook{
		cache: cache,
	}
}

type ConfigHook struct {
	cache *membershipCache
}

func (c *ConfigHook) Inject(hookErr *error, refresh bool) func(*rest.Config) {
	if c == nil {
		// This means membership cache is disabled.
		return func(*rest.Config) {}
	}

	apiConfig := func(conf *rest.Config) (api.Config, error) {
		if conf == nil {
			return api.Config{}, nil
		}
		if conf.AuthProvider != nil && conf.AuthProvider.Name == "gcp" {
			return connectGatewayKubeConfig(conf.Host)
		}
		serverURL, err := url.Parse(conf.Host)
		if err != nil {
			return api.Config{}, nil
		}
		if net.ParseIP(serverURL.Host) == nil {
			return api.Config{}, nil
		}

		ipBasedRemoteSecretsCount.Increment()
		cgwConfig, found, public := c.cache.get(serverURL.Host, refresh)
		if public && asm.ConnectGatewayForPublicRemoteCluster() == asm.CGWForPublicClusterDisabled {
			return api.Config{}, nil
		}
		if found {
			log.Infof("Translated secret with host: %s\nconfig: %v", serverURL.Host, conf)
			successfulTranslations.Increment()
			return cgwConfig, nil
		}
		err = fmt.Errorf("failed to translate secret with host: %s\nconfig: %v", serverURL.Host, conf)
		log.Warn(err)
		failedTranslations.Increment()
		if asm.ConnectGatewayForPublicRemoteCluster() == asm.CGWForPublicClusterEnabledWithoutFallback {
			return api.Config{}, err
		}
		return api.Config{}, nil
	}

	return func(conf *rest.Config) {
		aconf, err := apiConfig(conf)
		if err != nil {
			*hookErr = err
			return
		}
		if len(aconf.Clusters) == 0 {
			// Skip to override.
			return
		}
		clientConfig := clientcmd.NewDefaultClientConfig(aconf, &clientcmd.ConfigOverrides{})
		restConfig, err := clientConfig.ClientConfig()
		if err != nil {
			*hookErr = err
			return
		}
		*conf = *restConfig
	}
}
