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

package mcpserviceentrystatus

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"

	"istio.io/api/meta/v1alpha1"
	networking "istio.io/api/networking/v1alpha3"
	"istio.io/istio/pilot/pkg/model"
	"istio.io/istio/pilot/pkg/model/status"
	statusctl "istio.io/istio/pilot/pkg/status"
	"istio.io/istio/pkg/config"
	"istio.io/istio/pkg/config/schema/gvk"
	"istio.io/istio/pkg/test/util/retry"
)

func TestSetStatusWrite(t *testing.T) {
	statusManager := statusctl.NewManager(NewFakeStore())

	tests := []struct {
		name                       string
		enable                     bool
		statusManager              *statusctl.Manager
		wantNonNilStatusController bool
	}{
		{
			name:                       "noStatusManagerAndDisabled",
			enable:                     false,
			statusManager:              nil,
			wantNonNilStatusController: false,
		},
		{
			name:                       "statusManagerReadyButNotElectedYet",
			enable:                     false,
			statusManager:              statusManager,
			wantNonNilStatusController: false,
		},
		{
			name:                       "noStatusManagerButElected",
			enable:                     true,
			statusManager:              nil,
			wantNonNilStatusController: false,
		},
		{
			name:                       "statusManagerReadyAndElected",
			enable:                     true,
			statusManager:              statusManager,
			wantNonNilStatusController: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := &Controller{
				curServices: func() []*model.Service {
					return nil
				},
				curStatuses: make(map[statusctl.Resource]string),
			}

			if test.wantNonNilStatusController {
				ctrl.statusController.Store(nil)
			} else {
				ctrl.statusController.Store(&statusctl.Controller{})
			}

			ctrl.SetStatusWrite(test.enable, test.statusManager)
			if p := ctrl.statusController.Load(); (p != nil) != test.wantNonNilStatusController {
				t.Errorf("SetStatusWrite has the unexpected status controller field. got %p want %s", p, func() string {
					if test.wantNonNilStatusController {
						return "non-nil"
					}
					return "nil"
				}())
			}
		})
	}
}

var globalTime = time.Now()

func TestHandleConfig(t *testing.T) {
	svcEntryBase := config.Config{
		Meta: config.Meta{
			GroupVersionKind:  gvk.ServiceEntry,
			Name:              "tcpSvc",
			Namespace:         "tcpSvc",
			CreationTimestamp: globalTime,
			Generation:        99,
		},
		Spec: &networking.ServiceEntry{
			Hosts: []string{"test.google.com"},
			Ports: []*networking.ServicePort{
				{Number: 22, Name: "tcp", Protocol: "tcp"},
			},
			Endpoints: []*networking.WorkloadEntry{
				{
					Address: "12.34.56.78",
					Ports:   map[string]uint32{"tcp": 10022},
				},
			},
			Location:   networking.ServiceEntry_MESH_EXTERNAL,
			Resolution: networking.ServiceEntry_STATIC,
		},
	}

	withIstioStatus := func(config config.Config, value string) config.Config {
		config.Status = &v1alpha1.IstioStatus{
			Conditions: []*v1alpha1.IstioCondition{
				{
					Type:   ipAllocationStatusCondName,
					Status: value,
				},
			},
		}
		return config
	}

	withUnsupportedStatus := func(config config.Config, value string) config.Config {
		config.Status = &value
		return config
	}

	tests := []struct {
		name                string
		svcEntry            config.Config
		wantCurStatuses     map[statusctl.Resource]string
		wantServiceEntryRef model.MCPServiceEntryRef
	}{
		{
			name:            "noStatus",
			svcEntry:        svcEntryBase,
			wantCurStatuses: map[statusctl.Resource]string{},
			wantServiceEntryRef: model.MCPServiceEntryRef{
				GroupVersionResource: gvk.MustToGVR(gvk.ServiceEntry),
				Name:                 "tcpSvc",
				Generation:           "99",
			},
		},
		{
			name:     "withStatus",
			svcEntry: withIstioStatus(svcEntryBase, "teststatus"),
			wantCurStatuses: map[statusctl.Resource]string{
				statusctl.ResourceFromModelConfig(svcEntryBase): "teststatus",
			},
			wantServiceEntryRef: model.MCPServiceEntryRef{
				GroupVersionResource: gvk.MustToGVR(gvk.ServiceEntry),
				Name:                 "tcpSvc",
				Generation:           "99",
			},
		},
		{
			name:            "withUnsupportedStatus",
			svcEntry:        withUnsupportedStatus(svcEntryBase, "teststatus"),
			wantCurStatuses: map[statusctl.Resource]string{},
			wantServiceEntryRef: model.MCPServiceEntryRef{
				GroupVersionResource: gvk.MustToGVR(gvk.ServiceEntry),
				Name:                 "tcpSvc",
				Generation:           "99",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := &Controller{
				curStatuses: make(map[statusctl.Resource]string),
			}
			svc := &model.Service{
				Attributes: model.ServiceAttributes{
					ServiceRegistry: "External",
					Name:            "test.google.com",
					Namespace:       "tcpSvc",
				},
				Hostname:       "test.google.com",
				DefaultAddress: "0.0.0.0",
				MeshExternal:   true,
			}
			ctrl.HandleConfig(test.svcEntry, []*model.Service{svc})

			if diff := cmp.Diff(test.wantCurStatuses, ctrl.curStatuses); diff != "" {
				t.Errorf("unexpected curStatuses (-want, +got):\n%s", diff)
			}

			if diff := cmp.Diff(test.wantServiceEntryRef, svc.Attributes.MCPServiceEntryRef); diff != "" {
				t.Errorf("unexpected serviceEntryRef in the model.Service (-want, +got):\n%s", diff)
			}
		})
	}
}

func TestHandleIPAllocation(t *testing.T) {
	tests := []struct {
		name            string
		svcEntries      []config.Config
		services        []*model.Service
		disableIPAlloc  bool
		wantIstioStatus map[types.NamespacedName]*v1alpha1.IstioStatus
	}{
		{
			name: "empty-allocation",
			svcEntries: []config.Config{
				{
					Meta: config.Meta{
						GroupVersionKind:  gvk.ServiceEntry,
						Name:              "tcpSvc1",
						Namespace:         "tcpSvc1",
						CreationTimestamp: globalTime,
						Generation:        99,
					},
					Spec: &networking.ServiceEntry{
						Hosts: []string{"test1.google1.com", "test2.google2.com"},
						Ports: []*networking.ServicePort{
							{Number: 22, Name: "tcp", Protocol: "tcp"},
						},
						Location:   networking.ServiceEntry_MESH_EXTERNAL,
						Resolution: networking.ServiceEntry_STATIC,
					},
					Status: &v1alpha1.IstioStatus{},
				},
				{
					Meta: config.Meta{
						GroupVersionKind:  gvk.ServiceEntry,
						Name:              "tcpSvc2",
						Namespace:         "tcpSvc2",
						CreationTimestamp: globalTime,
						Generation:        99,
					},
					Spec: &networking.ServiceEntry{
						Hosts: []string{"test1.google2.com"},
						Ports: []*networking.ServicePort{
							{Number: 22, Name: "tcp", Protocol: "tcp"},
						},
						Location:   networking.ServiceEntry_MESH_EXTERNAL,
						Resolution: networking.ServiceEntry_STATIC,
					},
					Status: &v1alpha1.IstioStatus{},
				},
			},
			services: []*model.Service{
				{
					Attributes: model.ServiceAttributes{
						ServiceRegistry: "External",
						Name:            "test1.google1.com",
						Namespace:       "tcpSvc1",
						MCPServiceEntryRef: toResourceRef(statusctl.Resource{
							GroupVersionResource: gvk.MustToGVR(gvk.ServiceEntry),
							Name:                 "tcpSvc1",
							Generation:           "99",
						}),
					},
					Hostname:       "test1.google1.com",
					DefaultAddress: "0.0.0.0",
					MeshExternal:   true,
				},
				{
					Attributes: model.ServiceAttributes{
						ServiceRegistry: "External",
						Name:            "test2.google1.com",
						Namespace:       "tcpSvc1",
						MCPServiceEntryRef: toResourceRef(statusctl.Resource{
							GroupVersionResource: gvk.MustToGVR(gvk.ServiceEntry),
							Name:                 "tcpSvc1",
							Generation:           "99",
						}),
					},
					Hostname:       "test2.google1.com",
					DefaultAddress: "0.0.0.0",
					MeshExternal:   true,
				},
				{
					Attributes: model.ServiceAttributes{
						ServiceRegistry: "External",
						Name:            "test1.google2.com",
						Namespace:       "tcpSvc2",
						MCPServiceEntryRef: toResourceRef(statusctl.Resource{
							GroupVersionResource: gvk.MustToGVR(gvk.ServiceEntry),
							Name:                 "tcpSvc2",
							Generation:           "99",
						}),
					},
					Hostname:       "test1.google2.com",
					DefaultAddress: "0.0.0.0",
					MeshExternal:   true,
				},
			},
			wantIstioStatus: nil,
		},
		{
			name: "delete-status-for-empty-allocation",
			svcEntries: []config.Config{
				{
					Meta: config.Meta{
						GroupVersionKind:  gvk.ServiceEntry,
						Name:              "tcpSvc1",
						Namespace:         "tcpSvc1",
						CreationTimestamp: globalTime,
						Generation:        99,
					},
					Spec: &networking.ServiceEntry{
						Hosts: []string{"test1.google1.com", "test2.google2.com"},
						Ports: []*networking.ServicePort{
							{Number: 22, Name: "tcp", Protocol: "tcp"},
						},
						Location:   networking.ServiceEntry_MESH_EXTERNAL,
						Resolution: networking.ServiceEntry_STATIC,
					},
					Status: &v1alpha1.IstioStatus{
						Conditions: []*v1alpha1.IstioCondition{
							{
								Type: "AutoAllocatedIPs",
								Status: mustMarshal(t, []map[string]any{
									{
										"hostname": "test1.google1.com",
										"serviceEntryIPs": []map[string]string{
											{"ip": "3.3.3.3"},
											{"ip": "0::ffff:303:303"},
										},
									},
								}),
							},
							{
								Type: "AutoAllocatedIPs",
								Status: mustMarshal(t, []map[string]any{
									{
										"hostname": "test2.google2.com",
										"serviceEntryIPs": []map[string]string{
											{"ip": "4.4.4.4"},
											{"ip": "0::ffff:303:303"},
										},
									},
								}),
							},
						},
					},
				},
				{
					Meta: config.Meta{
						GroupVersionKind:  gvk.ServiceEntry,
						Name:              "tcpSvc2",
						Namespace:         "tcpSvc2",
						CreationTimestamp: globalTime,
						Generation:        99,
					},
					Spec: &networking.ServiceEntry{
						Hosts: []string{"test1.google2.com"},
						Ports: []*networking.ServicePort{
							{Number: 22, Name: "tcp", Protocol: "tcp"},
						},
						Location:   networking.ServiceEntry_MESH_EXTERNAL,
						Resolution: networking.ServiceEntry_STATIC,
					},
					Status: &v1alpha1.IstioStatus{},
				},
			},
			services: []*model.Service{
				{
					Attributes: model.ServiceAttributes{
						ServiceRegistry: "External",
						Name:            "test1.google1.com",
						Namespace:       "tcpSvc1",
						MCPServiceEntryRef: toResourceRef(statusctl.Resource{
							GroupVersionResource: gvk.MustToGVR(gvk.ServiceEntry),
							Name:                 "tcpSvc1",
							Generation:           "99",
						}),
					},
					Hostname:       "test1.google1.com",
					DefaultAddress: "0.0.0.0",
					MeshExternal:   true,
				},
				{
					Attributes: model.ServiceAttributes{
						ServiceRegistry: "External",
						Name:            "test2.google1.com",
						Namespace:       "tcpSvc1",
						MCPServiceEntryRef: toResourceRef(statusctl.Resource{
							GroupVersionResource: gvk.MustToGVR(gvk.ServiceEntry),
							Name:                 "tcpSvc1",
							Generation:           "99",
						}),
					},
					Hostname:       "test2.google1.com",
					DefaultAddress: "0.0.0.0",
					MeshExternal:   true,
				},
				{
					Attributes: model.ServiceAttributes{
						ServiceRegistry: "External",
						Name:            "test1.google2.com",
						Namespace:       "tcpSvc2",
						MCPServiceEntryRef: toResourceRef(statusctl.Resource{
							GroupVersionResource: gvk.MustToGVR(gvk.ServiceEntry),
							Name:                 "tcpSvc2",
							Generation:           "99",
						}),
					},
					Hostname:       "test1.google2.com",
					DefaultAddress: "0.0.0.0",
					MeshExternal:   true,
				},
			},
			wantIstioStatus: map[types.NamespacedName]*v1alpha1.IstioStatus{},
		},
		{
			name: "normal-allocation",
			svcEntries: []config.Config{
				{
					Meta: config.Meta{
						GroupVersionKind:  gvk.ServiceEntry,
						Name:              "tcpSvc1",
						Namespace:         "tcpSvc1",
						CreationTimestamp: globalTime,
						Generation:        99,
					},
					Spec: &networking.ServiceEntry{
						Hosts: []string{"test1.google1.com", "test2.google2.com"},
						Ports: []*networking.ServicePort{
							{Number: 22, Name: "tcp", Protocol: "tcp"},
						},
						Location:   networking.ServiceEntry_MESH_EXTERNAL,
						Resolution: networking.ServiceEntry_STATIC,
					},
					Status: &v1alpha1.IstioStatus{},
				},
				{
					Meta: config.Meta{
						GroupVersionKind:  gvk.ServiceEntry,
						Name:              "tcpSvc2",
						Namespace:         "tcpSvc2",
						CreationTimestamp: globalTime,
						Generation:        99,
					},
					Spec: &networking.ServiceEntry{
						Hosts: []string{"test1.google2.com"},
						Ports: []*networking.ServicePort{
							{Number: 22, Name: "tcp", Protocol: "tcp"},
						},
						Location:   networking.ServiceEntry_MESH_EXTERNAL,
						Resolution: networking.ServiceEntry_STATIC,
					},
					Status: &v1alpha1.IstioStatus{},
				},
			},
			services: []*model.Service{
				{
					Attributes: model.ServiceAttributes{
						ServiceRegistry: "External",
						Name:            "test1.google1.com",
						Namespace:       "tcpSvc1",
						MCPServiceEntryRef: toResourceRef(statusctl.Resource{
							GroupVersionResource: gvk.MustToGVR(gvk.ServiceEntry),
							Name:                 "tcpSvc1",
							Generation:           "99",
						}),
					},
					Hostname:                 "test1.google1.com",
					DefaultAddress:           "0.0.0.0",
					AutoAllocatedIPv4Address: "1.1.1.1",
					AutoAllocatedIPv6Address: "0::ffff:101:101",
					MeshExternal:             true,
				},
				{
					Attributes: model.ServiceAttributes{
						ServiceRegistry: "External",
						Name:            "test2.google1.com",
						Namespace:       "tcpSvc1",
						MCPServiceEntryRef: toResourceRef(statusctl.Resource{
							GroupVersionResource: gvk.MustToGVR(gvk.ServiceEntry),
							Name:                 "tcpSvc1",
							Generation:           "99",
						}),
					},
					Hostname:                 "test2.google1.com",
					DefaultAddress:           "0.0.0.0",
					AutoAllocatedIPv4Address: "2.2.2.2",
					AutoAllocatedIPv6Address: "0::ffff:202:202",
					MeshExternal:             true,
				},
				{
					Attributes: model.ServiceAttributes{
						ServiceRegistry: "External",
						Name:            "test1.google2.com",
						Namespace:       "tcpSvc2",
						MCPServiceEntryRef: toResourceRef(statusctl.Resource{
							GroupVersionResource: gvk.MustToGVR(gvk.ServiceEntry),
							Name:                 "tcpSvc2",
							Generation:           "99",
						}),
					},
					Hostname:                 "test1.google2.com",
					DefaultAddress:           "0.0.0.0",
					AutoAllocatedIPv4Address: "3.3.3.3",
					AutoAllocatedIPv6Address: "0::ffff:303:303",
					MeshExternal:             true,
				},
			},
			wantIstioStatus: map[types.NamespacedName]*v1alpha1.IstioStatus{
				{Namespace: "tcpSvc1", Name: "tcpSvc1"}: {
					Conditions: []*v1alpha1.IstioCondition{
						{
							Type: "AutoAllocatedIPs",
							Status: mustMarshal(t, []map[string]any{
								{
									"hostname": "test1.google1.com",
									"serviceEntryIPs": []map[string]string{
										{"ip": "1.1.1.1"},
										{"ip": "0::ffff:101:101"},
									},
								},
								{
									"hostname": "test2.google1.com",
									"serviceEntryIPs": []map[string]string{
										{"ip": "2.2.2.2"},
										{"ip": "0::ffff:202:202"},
									},
								},
							}),
						},
					},
					ObservedGeneration: 99,
				},
				{Namespace: "tcpSvc2", Name: "tcpSvc2"}: {
					Conditions: []*v1alpha1.IstioCondition{
						{
							Type: "AutoAllocatedIPs",
							Status: mustMarshal(t, []map[string]any{
								{
									"hostname": "test1.google2.com",
									"serviceEntryIPs": []map[string]string{
										{"ip": "3.3.3.3"},
										{"ip": "0::ffff:303:303"},
									},
								},
							}),
						},
					},
					ObservedGeneration: 99,
				},
			},
		},
		{
			name:           "normal-allocation-but-disabled",
			disableIPAlloc: true,
			svcEntries: []config.Config{
				{
					Meta: config.Meta{
						GroupVersionKind:  gvk.ServiceEntry,
						Name:              "tcpSvc1",
						Namespace:         "tcpSvc1",
						CreationTimestamp: globalTime,
						Generation:        99,
					},
					Spec: &networking.ServiceEntry{
						Hosts: []string{"test1.google1.com", "test2.google2.com"},
						Ports: []*networking.ServicePort{
							{Number: 22, Name: "tcp", Protocol: "tcp"},
						},
						Location:   networking.ServiceEntry_MESH_EXTERNAL,
						Resolution: networking.ServiceEntry_STATIC,
					},
					Status: &v1alpha1.IstioStatus{},
				},
				{
					Meta: config.Meta{
						GroupVersionKind:  gvk.ServiceEntry,
						Name:              "tcpSvc2",
						Namespace:         "tcpSvc2",
						CreationTimestamp: globalTime,
						Generation:        99,
					},
					Spec: &networking.ServiceEntry{
						Hosts: []string{"test1.google2.com"},
						Ports: []*networking.ServicePort{
							{Number: 22, Name: "tcp", Protocol: "tcp"},
						},
						Location:   networking.ServiceEntry_MESH_EXTERNAL,
						Resolution: networking.ServiceEntry_STATIC,
					},
					Status: &v1alpha1.IstioStatus{},
				},
			},
			services: []*model.Service{
				{
					Attributes: model.ServiceAttributes{
						ServiceRegistry: "External",
						Name:            "test1.google1.com",
						Namespace:       "tcpSvc1",
						MCPServiceEntryRef: toResourceRef(statusctl.Resource{
							GroupVersionResource: gvk.MustToGVR(gvk.ServiceEntry),
							Name:                 "tcpSvc1",
							Generation:           "99",
						}),
					},
					Hostname:                 "test1.google1.com",
					DefaultAddress:           "0.0.0.0",
					AutoAllocatedIPv4Address: "1.1.1.1",
					AutoAllocatedIPv6Address: "0::ffff:101:101",
					MeshExternal:             true,
				},
				{
					Attributes: model.ServiceAttributes{
						ServiceRegistry: "External",
						Name:            "test2.google1.com",
						Namespace:       "tcpSvc1",
						MCPServiceEntryRef: toResourceRef(statusctl.Resource{
							GroupVersionResource: gvk.MustToGVR(gvk.ServiceEntry),
							Name:                 "tcpSvc1",
							Generation:           "99",
						}),
					},
					Hostname:                 "test2.google1.com",
					DefaultAddress:           "0.0.0.0",
					AutoAllocatedIPv4Address: "2.2.2.2",
					AutoAllocatedIPv6Address: "0::ffff:202:202",
					MeshExternal:             true,
				},
				{
					Attributes: model.ServiceAttributes{
						ServiceRegistry: "External",
						Name:            "test1.google2.com",
						Namespace:       "tcpSvc2",
						MCPServiceEntryRef: toResourceRef(statusctl.Resource{
							GroupVersionResource: gvk.MustToGVR(gvk.ServiceEntry),
							Name:                 "tcpSvc2",
							Generation:           "99",
						}),
					},
					Hostname:                 "test1.google2.com",
					DefaultAddress:           "0.0.0.0",
					AutoAllocatedIPv4Address: "3.3.3.3",
					AutoAllocatedIPv6Address: "0::ffff:303:303",
					MeshExternal:             true,
				},
			},
			wantIstioStatus: nil,
		},
		{
			name:           "normal-allocation-with-previsou-status-but-disabled",
			disableIPAlloc: true,
			svcEntries: []config.Config{
				{
					Meta: config.Meta{
						GroupVersionKind:  gvk.ServiceEntry,
						Name:              "tcpSvc1",
						Namespace:         "tcpSvc1",
						CreationTimestamp: globalTime,
						Generation:        99,
					},
					Spec: &networking.ServiceEntry{
						Hosts: []string{"test1.google1.com", "test2.google2.com"},
						Ports: []*networking.ServicePort{
							{Number: 22, Name: "tcp", Protocol: "tcp"},
						},
						Location:   networking.ServiceEntry_MESH_EXTERNAL,
						Resolution: networking.ServiceEntry_STATIC,
					},
					Status: &v1alpha1.IstioStatus{
						Conditions: []*v1alpha1.IstioCondition{
							{
								Type: "AutoAllocatedIPs",
								Status: mustMarshal(t, []map[string]any{
									{
										"hostname": "test1.google1.com",
										"serviceEntryIPs": []map[string]string{
											{"ip": "1.1.1.1"},
											{"ip": "0::ffff:101:101"},
										},
									},
									{
										"hostname": "test2.google1.com",
										"serviceEntryIPs": []map[string]string{
											{"ip": "2.2.2.2"},
											{"ip": "0::ffff:202:202"},
										},
									},
								}),
							},
						},
						ObservedGeneration: 99,
					},
				},
				{
					Meta: config.Meta{
						GroupVersionKind:  gvk.ServiceEntry,
						Name:              "tcpSvc2",
						Namespace:         "tcpSvc2",
						CreationTimestamp: globalTime,
						Generation:        99,
					},
					Spec: &networking.ServiceEntry{
						Hosts: []string{"test1.google2.com"},
						Ports: []*networking.ServicePort{
							{Number: 22, Name: "tcp", Protocol: "tcp"},
						},
						Location:   networking.ServiceEntry_MESH_EXTERNAL,
						Resolution: networking.ServiceEntry_STATIC,
					},
					Status: &v1alpha1.IstioStatus{
						Conditions: []*v1alpha1.IstioCondition{
							{
								Type: "AutoAllocatedIPs",
								Status: mustMarshal(t, []map[string]any{
									{
										"hostname": "test1.google2.com",
										"serviceEntryIPs": []map[string]string{
											{"ip": "3.3.3.3"},
											{"ip": "0::ffff:303:303"},
										},
									},
								}),
							},
						},
						ObservedGeneration: 99,
					},
				},
			},
			services: []*model.Service{
				{
					Attributes: model.ServiceAttributes{
						ServiceRegistry: "External",
						Name:            "test1.google1.com",
						Namespace:       "tcpSvc1",
						MCPServiceEntryRef: toResourceRef(statusctl.Resource{
							GroupVersionResource: gvk.MustToGVR(gvk.ServiceEntry),
							Name:                 "tcpSvc1",
							Generation:           "99",
						}),
					},
					Hostname:                 "test1.google1.com",
					DefaultAddress:           "0.0.0.0",
					AutoAllocatedIPv4Address: "1.1.1.1",
					AutoAllocatedIPv6Address: "0::ffff:101:101",
					MeshExternal:             true,
				},
				{
					Attributes: model.ServiceAttributes{
						ServiceRegistry: "External",
						Name:            "test2.google1.com",
						Namespace:       "tcpSvc1",
						MCPServiceEntryRef: toResourceRef(statusctl.Resource{
							GroupVersionResource: gvk.MustToGVR(gvk.ServiceEntry),
							Name:                 "tcpSvc1",
							Generation:           "99",
						}),
					},
					Hostname:                 "test2.google1.com",
					DefaultAddress:           "0.0.0.0",
					AutoAllocatedIPv4Address: "2.2.2.2",
					AutoAllocatedIPv6Address: "0::ffff:202:202",
					MeshExternal:             true,
				},
				{
					Attributes: model.ServiceAttributes{
						ServiceRegistry: "External",
						Name:            "test1.google2.com",
						Namespace:       "tcpSvc2",
						MCPServiceEntryRef: toResourceRef(statusctl.Resource{
							GroupVersionResource: gvk.MustToGVR(gvk.ServiceEntry),
							Name:                 "tcpSvc2",
							Generation:           "99",
						}),
					},
					Hostname:                 "test1.google2.com",
					DefaultAddress:           "0.0.0.0",
					AutoAllocatedIPv4Address: "3.3.3.3",
					AutoAllocatedIPv6Address: "0::ffff:303:303",
					MeshExternal:             true,
				},
			},
			wantIstioStatus: map[types.NamespacedName]*v1alpha1.IstioStatus{},
		},
		{
			name: "same-allocation",
			svcEntries: []config.Config{
				{
					Meta: config.Meta{
						GroupVersionKind:  gvk.ServiceEntry,
						Name:              "tcpSvc1",
						Namespace:         "tcpSvc1",
						CreationTimestamp: globalTime,
						Generation:        99,
					},
					Spec: &networking.ServiceEntry{
						Hosts: []string{"test1.google1.com", "test2.google1.com"},
						Ports: []*networking.ServicePort{
							{Number: 22, Name: "tcp", Protocol: "tcp"},
						},
						Location:   networking.ServiceEntry_MESH_EXTERNAL,
						Resolution: networking.ServiceEntry_STATIC,
					},
					Status: &v1alpha1.IstioStatus{
						Conditions: []*v1alpha1.IstioCondition{
							{
								Type: "AutoAllocatedIPs",
								Status: mustMarshal(t, []map[string]any{
									{
										"hostname": "test1.google1.com",
										"serviceEntryIPs": []map[string]string{
											{"ip": "1.1.1.1"},
											{"ip": "0::ffff:101:101"},
										},
									},
									{
										"hostname": "test2.google1.com",
										"serviceEntryIPs": []map[string]string{
											{"ip": "2.2.2.2"},
											{"ip": "0::ffff:202:202"},
										},
									},
								}),
							},
						},
						ObservedGeneration: 99,
					},
				},
				{
					Meta: config.Meta{
						GroupVersionKind:  gvk.ServiceEntry,
						Name:              "tcpSvc2",
						Namespace:         "tcpSvc2",
						CreationTimestamp: globalTime,
						Generation:        99,
					},
					Spec: &networking.ServiceEntry{
						Hosts: []string{"test1.google2.com"},
						Ports: []*networking.ServicePort{
							{Number: 22, Name: "tcp", Protocol: "tcp"},
						},
						Location:   networking.ServiceEntry_MESH_EXTERNAL,
						Resolution: networking.ServiceEntry_STATIC,
					},
					Status: &v1alpha1.IstioStatus{
						Conditions: []*v1alpha1.IstioCondition{
							{
								Type: "AutoAllocatedIPs",
								Status: mustMarshal(t, []map[string]any{
									{
										"hostname": "test1.google2.com",
										"serviceEntryIPs": []map[string]string{
											{"ip": "3.3.3.3"},
											{"ip": "0::ffff:303:303"},
										},
									},
								}),
							},
						},
						ObservedGeneration: 99,
					},
				},
			},
			services: []*model.Service{
				{
					Attributes: model.ServiceAttributes{
						ServiceRegistry: "External",
						Name:            "test1.google1.com",
						Namespace:       "tcpSvc1",
						MCPServiceEntryRef: toResourceRef(statusctl.Resource{
							GroupVersionResource: gvk.MustToGVR(gvk.ServiceEntry),
							Name:                 "tcpSvc1",
							Generation:           "99",
						}),
					},
					Hostname:                 "test1.google1.com",
					DefaultAddress:           "0.0.0.0",
					AutoAllocatedIPv4Address: "1.1.1.1",
					AutoAllocatedIPv6Address: "0::ffff:101:101",
					MeshExternal:             true,
				},
				{
					Attributes: model.ServiceAttributes{
						ServiceRegistry: "External",
						Name:            "test2.google1.com",
						Namespace:       "tcpSvc1",
						MCPServiceEntryRef: toResourceRef(statusctl.Resource{
							GroupVersionResource: gvk.MustToGVR(gvk.ServiceEntry),
							Name:                 "tcpSvc1",
							Generation:           "99",
						}),
					},
					Hostname:                 "test2.google1.com",
					DefaultAddress:           "0.0.0.0",
					AutoAllocatedIPv4Address: "2.2.2.2",
					AutoAllocatedIPv6Address: "0::ffff:202:202",
					MeshExternal:             true,
				},
				{
					Attributes: model.ServiceAttributes{
						ServiceRegistry: "External",
						Name:            "test1.google2.com",
						Namespace:       "tcpSvc2",
						MCPServiceEntryRef: toResourceRef(statusctl.Resource{
							GroupVersionResource: gvk.MustToGVR(gvk.ServiceEntry),
							Name:                 "tcpSvc2",
							Generation:           "99",
						}),
					},
					Hostname:                 "test1.google2.com",
					DefaultAddress:           "0.0.0.0",
					AutoAllocatedIPv4Address: "3.3.3.3",
					AutoAllocatedIPv6Address: "0::ffff:303:303",
					MeshExternal:             true,
				},
			},
			wantIstioStatus: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configStore := NewFakeStore()
			for _, svcEntry := range test.svcEntries {
				_, err := configStore.Create(svcEntry)
				if err != nil {
					t.Fatalf("failed to create a service entry in the config store")
				}
			}

			statusManager := statusctl.NewManager(configStore)

			ctrl := &Controller{
				curServices: func() []*model.Service {
					return nil
				},
				curStatuses: make(map[statusctl.Resource]string),
			}

			for _, config := range test.svcEntries {
				cond := status.GetConditionFromSpec(config, ipAllocationStatusCondName)
				if cond == nil {
					continue
				}
				rsrc := statusctl.ResourceFromModelConfig(config)
				ctrl.curStatuses[rsrc] = cond.Status
			}

			ov := enableServiceEntryIPAutoAllocationStatus
			if !test.disableIPAlloc {
				enableServiceEntryIPAutoAllocationStatus = true
				defer func() {
					enableServiceEntryIPAutoAllocationStatus = ov
				}()
			}

			ctrl.setStatusWrite(true, statusManager, false)

			enqueued := ctrl.HandleIPAllocation(test.services)
			if test.wantIstioStatus != nil {
				// Give a time to write the status by the internal status controller.
				retry.UntilOrFail(t, func() bool {
					return configStore.LastEvent() == UpdateStatusEvent
				}, retry.Timeout(time.Second*2), retry.Delay(time.Millisecond*100))
			} else if enqueued {
				t.Errorf("unexpected status update was triggered.")
			}

			for nsname, wantStatus := range test.wantIstioStatus {
				storedConfig, ok := configStore.GetCopy(gvk.ServiceEntry, nsname.Name, nsname.Namespace)
				if !ok {
					t.Fatalf("the given resource %v is not found in the config store", nsname)
				}
				gotStatus, ok := storedConfig.Status.(*v1alpha1.IstioStatus)
				if !ok || gotStatus == nil {
					t.Fatalf("failed to get the status from stored config(%v)", storedConfig.Status)
				}

				if diff := cmp.Diff(wantStatus, gotStatus, protocmp.Transform()); diff != "" {
					t.Errorf("unexpected status is in the stored config (-want, +got):\n%s", diff)
				}
			}
		})
	}
}

func mustMarshal(t *testing.T, m any) string {
	s, err := yaml.Marshal(m)
	if err != nil {
		t.Fatalf("failed to marshal the ip allocations")
	}
	return string(s)
}
