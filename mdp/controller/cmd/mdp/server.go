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

package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"go.opencensus.io/stats/view"
	"golang.org/x/time/rate"
	v1 "k8s.io/api/core/v1"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"istio.io/istio/mdp/controller/pkg/apis"
	"istio.io/istio/mdp/controller/pkg/apis/mdp/v1alpha1"
	"istio.io/istio/mdp/controller/pkg/metrics"
	"istio.io/istio/mdp/controller/pkg/name"
	"istio.io/istio/mdp/controller/pkg/reconciler"
	"istio.io/istio/mdp/controller/pkg/revision"
	"istio.io/istio/mdp/controller/pkg/status"
	"istio.io/pkg/ctrlz"
	"istio.io/pkg/env"
	"istio.io/pkg/log"
)

// Should match deploy/service.yaml
const (
	metricsHost                = "0.0.0.0"
	metricsPort          int32 = 15015
	upTimeReportInterval       = 30 * time.Second
	controllerName             = "mdp-controller"
)

var (
	scope     = log.RegisterScope("mdp", "Managed Data Plane", 0)
	startTime = time.Now()
	runLocal  bool
)

func serverCmd() *cobra.Command {
	loggingOptions := log.DefaultOptions()
	introspectionOptions := ctrlz.DefaultOptions()

	serverCmd := &cobra.Command{
		Use:   "server",
		Short: "Starts the MDP server",
		Args:  cobra.ExactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := log.Configure(loggingOptions); err != nil {
				scope.Errorf("Unable to configure logging: %v", err)
			}

			if cs, err := ctrlz.Run(introspectionOptions, nil); err == nil {
				defer cs.Close()
			} else {
				scope.Errorf("Unable to initialize ControlZ: %v", err)
			}

			run()
			return nil
		},
	}

	serverCmd.Flags().BoolVar(&runLocal, "run-local", false, "ignore errors related to not being in a gke cluster")

	loggingOptions.AttachCobraFlags(serverCmd)
	introspectionOptions.AttachCobraFlags(serverCmd)

	return serverCmd
}

func run() {
	// Get a config to talk to the apiserver
	cfg, err := config.GetConfig()
	if err != nil {
		scope.Fatalf("Could not get apiserver config: %v", err)
	}

	mgrOpt := manager.Options{
		MetricsBindAddress:      fmt.Sprintf("%s:%d", metricsHost, metricsPort),
		LeaderElection:          !runLocal,
		LeaderElectionNamespace: "istio-system",
		LeaderElectionID:        "mdp-eviction-leader",
	}

	// Create a new Cmd to provide shared dependencies and start components
	mgr, err := manager.New(cfg, mgrOpt)
	if err != nil {
		scope.Fatalf("Could not create a controller manager: %v", err)
	}

	scope.Info("Creating MDP metrics exporter")
	if err := registerExporter(); err != nil {
		if runLocal {
			scope.Warnf("Failed to register MDP metrics exporter: %v", err)
		} else {
			scope.Fatalf("Failed to register MDP metrics exporter: %v", err)
		}
	}

	// Setup Scheme for all resources
	if err := apis.AddToScheme(mgr.GetScheme()); err != nil {
		scope.Fatalf("Could not add manager scheme: %v", err)
	}

	mrt := env.RegisterDurationVar("MDP_RECONCILE_TIME", reconciler.MaxTimeToReconcile, "the maximum time to reconcile an "+
		"entire revision using MDP")
	reconciler.MaxTimeToReconcile = mrt.Get()
	scope.Infof("using max reconcile time of %v", reconciler.MaxTimeToReconcile)

	mapper := revision.NewMapper(mgr.GetClient())
	cmhandler, cmcache := revision.NewConfigMapHandler(mapper)
	pcache := revision.NewPodCache(mapper, cmcache)
	nscache := revision.NewNamespaceHandler(pcache, mgr.GetClient(), mapper)
	sw := status.NewWorker(rate.Every(10*time.Second), mgr.GetClient())

	// Setup all Controller
	err = builder.ControllerManagedBy(mgr).
		For(&v1alpha1.DataPlaneControl{}).
		// these predicates will be applied to all watches, not just the 'For' watch
		WithEventFilter(predicate.Or(predicate.GenerationChangedPredicate{},
			predicate.AnnotationChangedPredicate{}, predicate.LabelChangedPredicate{})).
		Watches(&source.Kind{Type: &v1.Pod{}}, revision.NewPodHandler(mapper, pcache)).
		WithLogger(log.NewLogrAdapter(scope)).
		Watches(&source.Kind{Type: &v1.Namespace{}}, nscache).
		Watches(&source.Kind{Type: &v1.ConfigMap{}}, cmhandler,
			builder.WithPredicates(predicate.NewPredicateFuncs(func(object client.Object) bool {
				return object.GetNamespace() == name.IstioSystemNamespace &&
					strings.HasPrefix(object.GetName(), name.EnablementCMPrefix)
			}))).
		Complete(reconciler.New(pcache, sw, mgr.GetClient(), mgr.GetConfig(), mgr.GetEventRecorderFor(controllerName)))
	if err != nil {
		scope.Fatalf("Could not create a controller: %v", err)
	}

	scope.Info("Starting the server.")

	stopUP := make(chan struct{})
	defer close(stopUP)
	go func() {
		upTimeProber(stopUP)
	}()

	// Start the Cmd
	ctx := signals.SetupSignalHandler()
	sw.Start(ctx)
	pcache.Start(ctx)
	if err := mgr.Start(ctx); err != nil {
		scope.Fatalf("Manager exited non-zero: %v", err)
	}
}

func upTimeProber(stop <-chan struct{}) {
	ticker := time.NewTicker(upTimeReportInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			ut := time.Since(startTime)
			scope.Debugf("uptime: %v", ut.Seconds())
			metrics.ReportMDPUpTime(ut.Seconds())
			metrics.ReportServingState("READY")
		case <-stop:
			metrics.ReportServingState("UNREADY")
			return
		}
	}
}

func registerExporter() error {
	mdpExporter, err := metrics.NewMDPExporter()
	if err != nil {
		return err
	}
	view.RegisterExporter(mdpExporter)
	return nil
}
