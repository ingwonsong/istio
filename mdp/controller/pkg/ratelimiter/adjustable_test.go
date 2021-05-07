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

package ratelimiter

import (
	"testing"
	"time"

	"github.com/onsi/gomega"
	"golang.org/x/time/rate"
	"k8s.io/client-go/util/workqueue"
)

func TestAdjustRateLimit(t *testing.T) {
	rl := workqueue.NewItemExponentialFailureRateLimiter(time.Second, time.Minute)
	q := NewMDPRateLimitingQueueWithSpeedLimit(rate.Inf, 1, &workqueue.BucketRateLimiter{rate.NewLimiter(rate.Inf, 1)}, rl).(*rateLimitingType)
	g := gomega.NewGomegaWithT(t)
	g.Expect(q.limiter.Burst()).To(gomega.Equal(1))
	g.Expect(q.limiter.Limit()).To(gomega.Equal(rate.Inf))
	q.AdjustRateLimit(rate.Every(time.Second), 2)
	g.Expect(q.limiter.Burst()).To(gomega.Equal(2))
	g.Expect(q.limiter.Limit()).To(gomega.Equal(rate.Every(time.Second)))
}
