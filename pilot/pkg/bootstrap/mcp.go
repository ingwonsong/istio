// Copyright Istio Authors
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

package bootstrap

import (
	"net/http"

	"istio.io/istio/pilot/pkg/leaderelection"
	"istio.io/istio/pilot/pkg/serviceregistry/serviceentry"
	"istio.io/istio/pkg/asm/mcpserviceentrystatus"
	"istio.io/istio/pkg/kube"
)

func SetKubeClient(c kube.Client) func(*Server) {
	return func(server *Server) {
		server.kubeClient = c
	}
}

func mcpInjectResponseHeaders(w http.ResponseWriter) {
	w.Header().Set("x-internal-mcp-backend", "istiod")
}

func (s *Server) mcpServiceEntryStatusOption(args *PilotArgs) serviceentry.Option {
	seStatusController := mcpserviceentrystatus.MaybeNewController()
	if seStatusController == nil {
		return func(*serviceentry.Controller) {}
	}
	if s.statusManager == nil {
		s.initStatusManager(args)
	}
	s.addTerminatingStartFunc("service entry status", func(stop <-chan struct{}) error {
		// Electing a leader in a cluster.
		// There would be just one leader in a cluster even if there are multiple revisions.
		// (Please check out the comments on NewLeaderElection function for the details)
		// ConfigMap as a lock will be created in `Namespace` with given the election ID
		// "mcp-service-entry-status-leader" as the name of ConfigMap.
		leaderelection.
			NewLeaderElection(args.Namespace, args.PodName, "mcp-service-entry-status-leader", args.Revision, s.kubeClient).
			AddRunFunction(func(leaderStop <-chan struct{}) {
				seStatusController.SetStatusWrite(true, s.statusManager)
				<-leaderStop
				seStatusController.SetStatusWrite(false, nil)
			}).
			Run(stop)
		return nil
	})
	return serviceentry.WithMCPServiceEntryStatusController(seStatusController)
}
