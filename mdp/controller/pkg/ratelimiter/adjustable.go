/*
Copyright 2018 The Kubernetes Authors.

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
	"golang.org/x/time/rate"
	"k8s.io/client-go/util/workqueue"
)

// AdjustableRateLimitingInterface allows modifying rate limiter during the lifespan of the queue.
type AdjustableRateLimitingInterface interface {
	workqueue.RateLimitingInterface

	// AdjustRateLimit changes the rate at which AddRateLimited adds elements to the queue.
	AdjustRateLimit(limit rate.Limit, burst int)
}

// rateLimitingType wraps an Interface and provides rateLimited re-enquing
type rateLimitingType struct {
	workqueue.DelayingInterface

	rateLimiter        workqueue.RateLimiter
	limiter            *rate.Limiter
	failureRateLimiter workqueue.RateLimiter
}

// AdjustRateLimit dynamically changes the applied rate limit on the fly.
func (q *rateLimitingType) AdjustRateLimit(limit rate.Limit, burst int) {
	q.limiter.SetBurst(burst)
	q.limiter.SetLimit(limit)
}

// AddRateLimited implements workqueue.RateLimitingInterface
func (q *rateLimitingType) AddRateLimited(item interface{}) {
	q.DelayingInterface.AddAfter(item, q.rateLimiter.When(item))
}

// NumRequeues implements workqueue.RateLimitingInterface
func (q *rateLimitingType) NumRequeues(item interface{}) int {
	return q.rateLimiter.NumRequeues(item)
}

// Forget implements workqueue.RateLimitingInterface
func (q *rateLimitingType) Forget(item interface{}) {
	q.failureRateLimiter.Forget(item)
}

// AddFailed adds an item, using both the standard rate limiters and the failure rate limiter, to allow for exponential
// backoff of failures only.
func (q *rateLimitingType) AddFailed(item interface{}) {
	q.DelayingInterface.AddAfter(item, q.failureRateLimiter.When(item))
}

// MDPUpdateRateLimiter wraps AdjustableRateLimitingInterface and adds a specific function for adding failed
// items to queue.  Most Item rate limiters assume all calls to AddRateLimited represent failures, but
// this interface limits both failed and non-failed attempts, with exponential backoff for failures.
type MDPUpdateRateLimiter interface {
	AdjustableRateLimitingInterface
	// AddFailed adds an item, using both the standard rate limiters and the failure rate limiter, to allow for exponential
	// backoff of failures only.
	AddFailed(item interface{})
}

// NewMDPRateLimitingQueueWithSpeedLimit builds a multi-dimensional rate limiting queue, including a bucket limiter on
// limit and burst, an overall speedLimit, and a special limit for handling failures.  Note that unlike other
// RateLimiting Queues, AddRateLimited() is designed for use when no failure is present, and AddFailed is used after
// a failure.
func NewMDPRateLimitingQueueWithSpeedLimit(limit rate.Limit, burst int, speedLimit workqueue.RateLimiter,
	failureLimiter workqueue.RateLimiter,
) MDPUpdateRateLimiter {
	l := rate.NewLimiter(limit, burst)
	bucketltr := &workqueue.BucketRateLimiter{Limiter: l}
	maxSuccess := workqueue.NewMaxOfRateLimiter(bucketltr, speedLimit)
	return &rateLimitingType{
		rateLimiter: maxSuccess,
		limiter:     l, DelayingInterface: workqueue.NewDelayingQueue(),
		failureRateLimiter: workqueue.NewMaxOfRateLimiter(maxSuccess, failureLimiter),
	}
}
