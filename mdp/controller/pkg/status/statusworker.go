/*
 Copyright Istio Authors

 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package status

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"istio.io/istio/mdp/controller/pkg/apis/mdp/v1alpha1"
	"istio.io/pkg/log"
)

type Worker interface {
	Start(ctx context.Context)
	Stop()
	EnqueueStatus(dpc *v1alpha1.DataPlaneControl)
	Len() int
}

// NewWorker creates a status worker based on a BucketRateLimiter with the provided limit.
func NewWorker(limit rate.Limit, cl client.Client) Worker {
	rl := rate.NewLimiter(limit, 1)
	// burn the first ticket, which has no delay.
	rl.Reserve().Delay()
	return &tickWorker{
		client:   cl,
		cache:    make(map[types.NamespacedName]*v1alpha1.DataPlaneControl),
		interval: rl.Reserve().Delay(),
	}
}

type tickWorker struct {
	cancel context.CancelFunc
	client client.Client
	mu     sync.Mutex
	// cache holds the desired status value so that two calls to EnqueueStatus don't result in two separate writes to K8s
	cache    map[types.NamespacedName]*v1alpha1.DataPlaneControl
	interval time.Duration
}

func (t *tickWorker) Start(ctx context.Context) {
	sctx, can := context.WithCancel(ctx)
	t.cancel = can
	ticker := time.NewTicker(t.interval)
	go func() {
		for {
			select {
			case <-sctx.Done():
				ticker.Stop()
				return
			case <-ticker.C:
				t.sendStatusUpdates(sctx)
			}
		}
	}()
}

func (t *tickWorker) Stop() {
	if t.cancel == nil {
		log.Fatalf("Stop() called on unstarted Updater")
	}
	t.cancel()
}

func (t *tickWorker) EnqueueStatus(dpc *v1alpha1.DataPlaneControl) {
	nsn := types.NamespacedName{Name: dpc.Name, Namespace: dpc.Namespace}
	log.Debugf("adding to status update for dpc %s to queue", dpc.Name)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cache[nsn] = dpc.DeepCopy()
}

func (t *tickWorker) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.cache)
}

func (t *tickWorker) sendStatusUpdates(ctx context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for nsn, dpc := range t.cache {
		log.Debugf("writing status for dpc %s", dpc.Name)
		err := t.client.Status().Update(ctx, dpc)
		if err != nil {
			runtime.HandleError(err)
		} else {
			delete(t.cache, nsn)
		}
	}
}
