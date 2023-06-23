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

// Package ratelog implements rate controller logging.
package ratelog

import (
	"fmt"
	"sync"
	"time"

	"istio.io/istio/pkg/log"
)

// Log is a rate controlled logger.
type Log struct {
	minLogInterval time.Duration
	outputBuffer   *[]string

	// mu protects the following block.
	mu        sync.RWMutex
	errBuffer []logEntry
}

type severity string

const (
	severityError   = "error"
	severityWarning = "warn"
	severityInfo    = "info"
)

type logEntry struct {
	msg       string
	logAtTime time.Time
}

// New creates a new Log and returns a ptr to it. It spawns a goroutine which services the internal
// rate queue. The goroutine never exits.
func New(minLogInterval time.Duration, outputBuffer *[]string) *Log {
	l := &Log{minLogInterval: minLogInterval, outputBuffer: outputBuffer}
	go func() {
		for {
			l.serviceMsgQueue()
		}
	}()
	return l
}

// Info logs a message with info severity, unless the message was already logged
// more recently than minLogInterval.
func (l *Log) Info(msg string) {
	l.log(msg, severityInfo)
}

// Infof is the format string version of Info.
func (l *Log) Infof(format string, a ...interface{}) {
	l.log(fmt.Sprintf(format, a...), severityInfo)
}

// Warn logs a message with warn severity, unless the message was already logged
// more recently than minLogInterval.
func (l *Log) Warn(msg string) {
	l.log(msg, severityWarning)
}

// Warnf is the format string version of Warn.
func (l *Log) Warnf(format string, a ...interface{}) {
	l.log(fmt.Sprintf(format, a...), severityWarning)
}

// Error logs a message with error severity, unless the message was already
// logged more recently than minLogInterval.
func (l *Log) Error(msg string) {
	l.log(msg, severityError)
}

// Errorf is the format string version of Error.
func (l *Log) Errorf(format string, a ...interface{}) {
	l.log(fmt.Sprintf(format, a...), severityError)
}

func (l *Log) log(msg string, severity severity) {
	l.mu.RLock()
	for _, e := range l.errBuffer {
		if e.msg == msg {
			// same message has been logged too recently
			l.mu.RUnlock()
			return
		}
	}
	l.mu.RUnlock()

	// Not in queue so log it.
	l.doLog(msg, severity)

	l.mu.Lock()
	defer l.mu.Unlock()
	// But also queue it so we don't print the same thing again for a while.
	l.errBuffer = append(l.errBuffer, logEntry{msg: msg, logAtTime: time.Now().Add(l.minLogInterval)})
}

// serviceMsgQueue waits a short while if the queue is empty.
// If the queue is non-empty, it sleeps until the next message is due to be popped, then pops
// it and returns.
func (l *Log) serviceMsgQueue() {
	// if empty, poll every second
	l.mu.RLock()
	if len(l.errBuffer) == 0 {
		l.mu.RUnlock()
		time.Sleep(time.Second)
		return
	}
	e := l.errBuffer[0]
	l.mu.RUnlock()

	time.Sleep(time.Until(e.logAtTime))

	l.mu.Lock()
	l.errBuffer = l.errBuffer[1:]
	l.mu.Unlock()
}

func (l *Log) doLog(msg string, severity severity) {
	if l.outputBuffer != nil {
		*l.outputBuffer = append(*l.outputBuffer, fmt.Sprintf("%s %s", severity, msg))
		return
	}
	switch severity {
	case severityError:
		log.Error(msg)
	case severityWarning:
		log.Warn(msg)
	case severityInfo:
		log.Info(msg)
	default:
	}
}
