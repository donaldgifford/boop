/*
Copyright 2026.

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

package workflows

import (
	"strconv"
	"time"
)

// LeaseBook is the open leases of a lease entity: InstallationWorkflow's
// budget and CapacityWorkflow's cluster cap. It is embedded in each
// entity's ContinueAsNew state, so its fields encode at the state's top
// level, where BudgetState always carried them.
type LeaseBook struct {
	Leases    map[string]Lease
	NextLease int64
}

// init makes the map a fresh or decoded state may lack.
func (l *LeaseBook) init() {
	if l.Leases == nil {
		l.Leases = map[string]Lease{}
	}
}

// open is the number of open leases.
func (l *LeaseBook) open() int { return len(l.Leases) }

// full reports whether limit leases are open; a limit of 0 never is.
func (l *LeaseBook) full(limit int) bool { return limit > 0 && len(l.Leases) >= limit }

// grant opens a lease for holder that expires ttl after now and returns
// its id.
func (l *LeaseBook) grant(now time.Time, holder string, ttl time.Duration) string {
	id := holder + "#" + strconv.FormatInt(l.NextLease, 10)
	l.NextLease++
	l.Leases[id] = Lease{Holder: holder, Expires: now.Add(ttl)}

	return id
}

// close ends a lease; an unknown or already swept id is a no-op.
func (l *LeaseBook) close(id string) { delete(l.Leases, id) }

// sweep releases leases past their expiry.
func (l *LeaseBook) sweep(now time.Time) {
	for id, lease := range l.Leases {
		if !now.Before(lease.Expires) {
			delete(l.Leases, id)
		}
	}
}

// nextExpiry is the earliest lease expiry, or now plus idleSweep with
// no lease open.
func (l *LeaseBook) nextExpiry(now time.Time) time.Time {
	var next time.Time

	for _, lease := range l.Leases {
		if next.IsZero() || lease.Expires.Before(next) {
			next = lease.Expires
		}
	}

	if next.IsZero() {
		return now.Add(idleSweep)
	}

	return next
}

// capRetry is when a caller turned away by a full cap should ask again:
// the next expiry or capWait, whichever is first, jittered.
func (l *LeaseBook) capRetry(now time.Time) time.Time {
	return l.jitter(earliest(l.nextExpiry(now), now.Add(capWait)))
}

// jitter spreads the callers told to wait for t over the minute after
// it, deterministically.
func (l *LeaseBook) jitter(t time.Time) time.Time {
	d := time.Duration(l.NextLease%waitJitterSpan) * time.Second
	l.NextLease++

	return t.Add(d)
}

func earliest(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}

	return a
}
