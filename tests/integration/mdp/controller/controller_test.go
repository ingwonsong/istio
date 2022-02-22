//go:build integ
// +build integ

//  Copyright Istio Authors
//
//  Licensed under the Apache License, Version 2.0 (the "License");
//  you may not use this file except in compliance with the License.
//  You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
//  Unless required by applicable law or agreed to in writing, software
//  distributed under the License is distributed on an "AS IS" BASIS,
//  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//  See the License for the specific language governing permissions and
//  limitations under the License.

package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"
	"time"

	v1 "k8s.io/api/core/v1"
	kubeApiMeta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	mdpapi "istio.io/istio/mdp/controller/pkg/apis/mdp/v1alpha1"
	"istio.io/istio/mdp/controller/pkg/name"
	"istio.io/istio/mdp/controller/pkg/util"
	"istio.io/istio/pkg/config/constants"
	"istio.io/istio/pkg/test/env"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/cluster"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/common"
	"istio.io/istio/pkg/test/framework/components/echo/echoboot"
	"istio.io/istio/pkg/test/framework/components/namespace"
	kube2 "istio.io/istio/pkg/test/kube"
	"istio.io/istio/pkg/test/scopes"
	"istio.io/istio/pkg/test/shell"
	"istio.io/istio/pkg/test/util/retry"
)

const (
	retryDelay         = 3 * time.Second
	retryTimeOut       = 7 * time.Minute
	workload1          = "mdp-app1"
	workload2          = "mdp-app2"
	workloadPDB        = "mdp-pdb-restricted"
	crTemplatePath     = "testdata/mdp_cr.yaml.tmpl"
	RunWithGenManifest = "RUN_WITH_GEN_MANIFEST"
	GcrProjectIDENV    = "GCR_PROJECT_ID"
)

var (
	mdpGVR = schema.GroupVersionResource{
		Group:    "mesh.cloud.google.com",
		Version:  "v1alpha1",
		Resource: "dataplanecontrols",
	}
	ns                namespace.Instance
	instances         echo.Instances
	prowTestImageHub  string
	builtProxyVersion string
	genManifestPath   = filepath.Join(env.IstioSrc, "mdp/manifest/gen-mdp-manifest.yaml")
	oldRevision       = os.Getenv("OLD_REVISION")
	newRevision       = os.Getenv("NEW_REVISION")
)

func TestMain(m *testing.M) {
	framework.
		NewSuite(m).
		Run()
}

// TestProxiesRestarted verify whether the MDP controller can upgrade proxies as expected
// 1. Two control planes would be installed at setup stage by providing the RevisionConfig to the test framework, i.e. 1.11-asm and master-asm.
//    Initially the proxies are injected with old version, i.e. 1.11-asm
// 2. Then it relabels the namespace for new version, i.e. master-asm,
//    so after MDP successfully evict a pod, the newly created pods would be injected with proxies of new version.
// 3. verify upgraded proxies percentage, CR status
func TestProxiesRestarted(t *testing.T) {
	framework.NewTest(t).
		Features("mdp.upgrade").
		Run(func(t framework.TestContext) {
			defer dumpCNI(t)
			cs := t.Clusters().Default()
			ns = namespace.NewOrFail(t, t, namespace.Config{
				Prefix: "mdp-workload",
				Inject: true,
				Labels: map[string]string{"istio.io/rev": oldRevision},
			})
			prowTestImageHub, builtProxyVersion = getIstiodVersion(t, cs, newRevision)
			t.Logf("got prow test hub: %v, tag: %v", prowTestImageHub, builtProxyVersion)
			stubEnvironmentCM(t, "env", "istio-system", builtProxyVersion)
			if af := os.Getenv(RunWithGenManifest); af == "true" {
				applyGenMDPManifest(t)
			}
			builder := echoboot.NewBuilder(t).WithClusters(cs)
			builder = builder.WithConfig(echo.Config{
				Namespace: ns,
				Service:   workload1,
				Ports:     common.EchoPorts,
			}).WithConfig(echo.Config{
				Namespace: ns,
				Service:   workload2,
				Ports:     common.EchoPorts,
			})
			instances = builder.BuildOrFail(t)
			_, err := kube2.WaitUntilPodsAreReady(kube2.NewSinglePodFetch(cs, "kube-system", "k8s-app=istio-cni-node"))
			if err != nil {
				t.Fatal(err)
			}

			testcases := []struct {
				expectedPercentage float32
				expectedDPState    mdpapi.DataPlaneState
				// This revision has to be match with available revision passed in to `--istio.test.revisions`
				newRevision string
				oldRevision string
			}{
				{
					expectedPercentage: 66,
					expectedDPState:    mdpapi.Ready,
					newRevision:        newRevision,
					oldRevision:        oldRevision,
				},
			}
			for _, test := range testcases {
				// check proxy version before upgrade, all proxies should be at old version
				verifyProxyVersion(t, cs, 100, test.oldRevision)
				updateNamespace(t, ns, test.newRevision)
				data := map[string]interface{}{
					"NewProxyVersion": builtProxyVersion,
				}
				createAndApplyTemplate(t, crTemplatePath, "", data)
				// check proxy version after upgrade, upgraded proxies percentage should be larger than expected.
				verifyProxyVersion(t, cs, test.expectedPercentage, test.newRevision)
				// check CR status
				verifyMDPCRStatus(t, cs, test.expectedDPState)
				// check workload
				verifyPodStatus(t, cs)
				// check pdb restricted workload
				checkPDBWorkload(t, cs, builder, test.oldRevision, test.newRevision, builtProxyVersion)
			}
		})
}

// applyGenMDPManifest is a helper function to apply gen mdp manifest template file
func applyGenMDPManifest(t framework.TestContext) {
	data := make(map[string]interface{})
	data["MDP_HUB"] = prowTestImageHub
	data["MDP_TAG"] = builtProxyVersion
	data["PROJECT_ID"] = os.Getenv(GcrProjectIDENV)
	createAndApplyTemplate(t, genManifestPath, name.KubeSystemNamespace, data)
	retryFunc := func() error {
		updateEnvCmd := fmt.Sprintf("kubectl set env daemonset/istio-cni-node -c mdp-controller MDP_RECONCILE_TIME=1m -n %s", name.KubeSystemNamespace)
		if _, err := shell.Execute(true, updateEnvCmd); err != nil {
			return fmt.Errorf("failed to update MDP_RECONCILE_TIME of mdp: %v", err)
		}
		return nil
	}
	if err := retry.UntilSuccess(retryFunc,
		retry.Timeout(2*time.Minute), retry.Delay(time.Second*5)); err != nil {
		t.Errorf(err.Error())
	} else {
		t.Logf("Successfully updated MDP_RECONCILE_TIME of mdp")
	}
}

func checkPDBWorkload(t framework.TestContext, cs cluster.Cluster, builder echo.Builder, oldRevision, newRevision, newVersion string) {
	pdbns := namespace.NewOrFail(t, t, namespace.Config{
		Prefix: "mdp-workload-pdb",
		Inject: true,
		Labels: map[string]string{"istio.io/rev": oldRevision},
	})
	pdbInst := builder.WithConfig(echo.Config{
		Namespace: pdbns,
		Service:   workloadPDB,
		Ports:     common.EchoPorts,
	}).BuildOrFail(t)
	pdbWL, err := pdbInst[len(pdbInst)-1].Workloads()
	if err != nil {
		t.Fatalf("failed to get PDB restricted workload: %v", err)
	}
	pdbWLName := pdbWL[0].PodName()
	createAndApplyTestdataFile(t, "testdata/pdb_unevictable.yaml", pdbns.Name())
	updateNamespace(t, pdbns, newRevision)
	data := map[string]interface{}{
		"NewProxyVersion": newVersion,
	}
	// increase dpc target so mdp would attempt to upgrade pdb restricted wl
	createAndApplyTemplate(t, crTemplatePath, "", data)
	verifyFailureEventsAndLabels(t, cs, pdbWLName, pdbns.Name())
}

func dumpCNI(t framework.TestContext) {
	kube2.DumpPods(t, t.CreateTmpDirectoryOrFail("cni-mdp"), constants.KubeSystemNamespace, []string{"k8s-app=istio-cni-node"})
}

func updateNamespace(t framework.TestContext, instance namespace.Instance, newRevision string) {
	t.Helper()
	if err := instance.SetLabel("istio.io/rev", newRevision); err != nil {
		t.Fatalf("failed to set label for ns: %v", err)
	}
	if err := instance.SetAnnotation(name.MDPEnabledAnnotation, "{\"managed\":\"true\"}"); err != nil {
		t.Fatalf("failed to set annotation for ns: %v", err)
	}
}

func getIstiodVersion(t framework.TestContext, cs cluster.Cluster, revision string) (hub, version string) {
	ls := fmt.Sprintf("istio.io/rev=%s", revision)
	istiodPods, err := cs.
		CoreV1().Pods("istio-system").
		List(context.Background(), kubeApiMeta.ListOptions{LabelSelector: ls})
	if err != nil {
		t.Fatalf("failed to get istiod pods: %v for revision: %v", err, revision)
	}
	for _, pod := range istiodPods.Items {
		for _, container := range pod.Spec.Containers {
			vs := strings.Split(container.Image, ":")
			if strings.Contains(vs[0], "pilot") {
				hub = strings.TrimSuffix(vs[0], "/pilot")
				version = vs[1]
			}
		}
	}
	return
}

func verifyFailureEventsAndLabels(t framework.TestContext, cs cluster.Cluster, involvedObj, nsName string) {
	retryFunc := func() error {
		fs := cs.CoreV1().Events(nsName).GetFieldSelector(&involvedObj, &nsName, nil, nil)
		t.Logf("checking events and labels for pod: %v/%v", nsName, involvedObj)
		events, err := cs.CoreV1().Events(nsName).List(context.Background(), kubeApiMeta.ListOptions{FieldSelector: fs.String()})
		if err != nil {
			return fmt.Errorf("failed to list possible upgrade failure events: %v", err)
		}
		vfEvent := false
		for _, ev := range events.Items {
			if ev.Reason == name.UpgradeErrorEventReason && strings.Contains(ev.Message, name.EvictionErrorEventMessage) {
				t.Log("verified upgrade failure events")
				vfEvent = true
				break
			}
		}
		if !vfEvent {
			return fmt.Errorf("failed to find expected upgrade failure events")
		}
		pd, err := cs.CoreV1().Pods(nsName).Get(context.Background(), involvedObj, kubeApiMeta.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get pdb restricted workload")
		}
		if ls := pd.Labels; ls[name.DataplaneUpgradeLabel] == "failed" {
			t.Log("verified dataplane upgrade label set to failed")
		} else {
			return fmt.Errorf("got unexpected dataplane upgrade label: %v,"+
				" expected: %s", ls[name.DataplaneUpgradeLabel], "failed")
		}
		return nil
	}
	if err := retry.UntilSuccess(retryFunc,
		retry.Timeout(retryTimeOut), retry.Delay(retryDelay)); err != nil {
		t.Errorf(err.Error())
	} else {
		scopes.Framework.Infof("Successfully verified proxy percentage")
	}
}

// verifyProxyVersion is a helper function to verify proxies percentage, it can be used before and after upgrade.
func verifyProxyVersion(t framework.TestContext, cs cluster.Cluster,
	expectedPercentage float32, revision string) {
	_, expectedVersion := getIstiodVersion(t, cs, revision)
	scopes.Framework.Infof("expected proxy version is: %v", expectedVersion)

	targetPodsSelection := fmt.Sprintf("app in (%s,%s,%s)", workload1, workload2, workloadPDB)
	retryFunc := func() error {
		allWorkloadPods, err := cs.
			CoreV1().Pods("").
			List(context.Background(), kubeApiMeta.ListOptions{LabelSelector: targetPodsSelection})
		if allWorkloadPods == nil || err != nil {
			return fmt.Errorf("error listing mdp managed pods: %v", err)
		}
		totalPods, newPods := 0, 0
		for _, pod := range allWorkloadPods.Items {
			t.Logf("Checking pod %s", pod.Name)
			ver, ok := util.ProxyVersion(&pod)
			if ok {
				totalPods++
				if ver == expectedVersion {
					newPods++
				} else {
					t.Logf("got unexpected proxy version: %v at pod: %v", ver, pod.Name)
				}
			}
		}
		if totalPods == 0 {
			t.Log("there are no pods managed by mdp, so no upgrade performed as expected\n")
			return nil
		}
		perc := 100.0 * float32(newPods) / float32(totalPods)
		if perc < expectedPercentage {
			msg := fmt.Sprintf("got unexpected upgraded percentage: %v,"+
				" total pod count: %d, upgraded pod count: %d", perc, totalPods, newPods)
			t.Log(msg)
			return fmt.Errorf(msg)
		}
		t.Logf("got percentage of upgraded proxies: %v, expected: %v", perc, expectedPercentage)
		return nil
	}
	if err := retry.UntilSuccess(retryFunc,
		retry.Timeout(retryTimeOut), retry.Delay(retryDelay)); err != nil {
		t.Errorf(err.Error())
	} else {
		scopes.Framework.Infof("Successfully verified proxy percentage")
	}
}

func verifyPodStatus(t framework.TestContext, cs cluster.Cluster) {
	targetPodsSelection := fmt.Sprintf("app in (%s,%s)", workload1, workload2)
	retryFunc := func() error {
		allWorkloadPods, err := cs.
			CoreV1().Pods("").
			List(context.Background(), kubeApiMeta.ListOptions{LabelSelector: targetPodsSelection})
		if allWorkloadPods == nil || err != nil {
			return fmt.Errorf("error listing pods: %v", err)
		}
		for _, pod := range allWorkloadPods.Items {
			if len(pod.Status.Conditions) > 0 {
				for _, condition := range pod.Status.Conditions {
					if condition.Type == v1.PodReady &&
						condition.Status == v1.ConditionTrue {
						return nil
					}
				}
			} else {
				return fmt.Errorf("pod: %v not ready", pod.Name)
			}
		}
		return nil
	}
	err := retry.UntilSuccess(retryFunc, retry.Timeout(retryTimeOut), retry.Delay(retryDelay))
	if err != nil {
		t.Errorf(err.Error())
	} else {
		scopes.Framework.Infof("Successfully verified workload pod status")
	}
}

func stubEnvironmentCM(t framework.TestContext, envFile, namespace, targetVersion string) {
	// TODO @iamwen: remove this stub once test is running against mcp.
	path := fmt.Sprintf("testdata/%s.yaml", envFile)
	mdpCM, err := ioutil.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read env cm file: %s: %v", envFile, err)
	}
	tmpl, err := template.New("MDPTest").Parse(string(mdpCM))
	if err != nil {
		t.Fatalf("failed to create template: %v", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]string{
		"NewProxyVersion": targetVersion,
	}); err != nil {
		t.Fatalf("failed to render template: %v", err)
	}
	if err := t.ConfigKube().ApplyYAML(namespace, buf.String()); err != nil {
		t.Fatalf("failed to apply env cm file: %s, %v", path, err)
	}
}

func createAndApplyTemplate(t framework.TestContext, path, namespace string, data map[string]interface{}) {
	tp, err := ioutil.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read template: %v", err)
	}
	tmpl, err := template.New("MDPTest").Parse(string(tp))
	if err != nil {
		t.Fatalf("failed to create template: %v", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("failed to render template: %v", err)
	}
	if err := t.ConfigKube().ApplyYAML(namespace, buf.String()); err != nil {
		t.Fatalf("failed to apply template: %s, %v", path, err)
	}
}

func createAndApplyTestdataFile(t framework.TestContext, path, namespace string) {
	f, err := ioutil.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read file: %s: %v", path, err)
	}
	if err := t.ConfigKube().ApplyYAML(namespace, string(f)); err != nil {
		t.Fatalf("failed to apply file: %s, %v", path, err)
	}
}

// verifyMDPCRStatus verify the status of MDP CR in the cluster as expected in different stages.
func verifyMDPCRStatus(t framework.TestContext, cs cluster.Cluster, expectedState mdpapi.DataPlaneState) {
	scopes.Framework.Infof("checking DataPlaneControl CR status")
	retryFunc := func() error {
		us, err := cs.Dynamic().Resource(mdpGVR).Get(context.TODO(), "test-mdp", kubeApiMeta.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get DataPlaneControl resource: %v", err)
		}
		mdpStatus := us.UnstructuredContent()["status"]
		if mdpStatus == nil {
			return fmt.Errorf("status not found from the DataPlaneControl resource")
		}
		mdpStatusString, err := json.Marshal(mdpStatus)
		if err != nil {
			return fmt.Errorf("failed to marshal DataPlaneControl status: %v", err)
		}
		status := &mdpapi.DataPlaneControlStatus{}
		if err := json.Unmarshal(mdpStatusString, status); err != nil {
			return fmt.Errorf("failed to unmarshal DataPlaneControl status: %v", err)
		}

		if status.State != expectedState {
			msg := fmt.Sprintf("expected DataPlaneControl status: %v, got: %v\n", expectedState, status.State)
			t.Logf(msg)
			return fmt.Errorf(msg)
		}
		return nil
	}
	err := retry.UntilSuccess(retryFunc, retry.Timeout(retryTimeOut), retry.Delay(retryDelay))
	if err != nil {
		t.Errorf("failed to get expected DataPlaneControl status: %v", err)
	} else {
		scopes.Framework.Infof("Successfully verified MDP CR status")
	}
}
