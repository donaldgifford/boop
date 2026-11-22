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

package api

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/donaldgifford/boop/internal/api/gen"
	"github.com/donaldgifford/boop/internal/store"
)

// Estate numbers are computed on read and cached per (scope, owner,
// range, metric) for estateTTL (DESIGN-0004 § Estate). Each query's
// p95 over the last timingWindow computations is logged, for the
// decision on rollup tables.
const (
	estateTTL    = 60 * time.Second
	timingWindow = 100
	defaultRange = "30d"
)

// estateRanges are the range parameter's values.
var estateRanges = map[string]time.Duration{
	"1d": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour, "90d": 90 * 24 * time.Hour,
}

// ageLabels name EstateAgeBuckets' columns, youngest first.
var ageLabels = []string{"<1d", "1-7d", "7-30d", "30-90d", ">90d"}

// estateCache holds computed answers and the query timings.
type estateCache struct {
	mu      sync.Mutex
	entries map[string]estateEntry
	timings map[string][]time.Duration
}

type estateEntry struct {
	at  time.Time
	val any
}

func (c *estateCache) get(key string, now time.Time) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || now.Sub(e.at) >= estateTTL {
		return nil, false
	}
	return e.val, true
}

func (c *estateCache) put(key string, now time.Time, val any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]estateEntry)
	}
	for k, e := range c.entries {
		if now.Sub(e.at) >= estateTTL {
			delete(c.entries, k)
		}
	}
	c.entries[key] = estateEntry{at: now, val: val}
}

// observe records the timings and logs each query's p95.
func (c *estateCache) observe(ctx context.Context, log *slog.Logger, timings map[string]time.Duration) {
	c.mu.Lock()
	if c.timings == nil {
		c.timings = make(map[string][]time.Duration)
	}
	attrs := make([]any, 0, 2*len(timings))
	for name, d := range timings {
		w := c.timings[name]
		w = append(w, d)
		if len(w) > timingWindow {
			w = w[len(w)-timingWindow:]
		}
		c.timings[name] = w
		attrs = append(attrs, name+"_p95", p95(w))
	}
	c.mu.Unlock()
	log.InfoContext(ctx, "estate query timings", attrs...)
}

// p95 is the 95th percentile of ds, nearest rank.
func p95(ds []time.Duration) time.Duration {
	s := slices.Clone(ds)
	slices.Sort(s)
	i := int(math.Ceil(0.95*float64(len(s)))) - 1
	return s[max(i, 0)]
}

// estateRequest resolves the owner filter and the range.
func estateRequest(ctx context.Context, owner, rng *string) (Scope, string, time.Duration, error) {
	sc, err := scope(ctx, owner)
	if err != nil {
		return Scope{}, "", 0, err
	}
	r := defaultRange
	if rng != nil && *rng != "" {
		r = *rng
	}
	d, ok := estateRanges[r]
	if !ok {
		return Scope{}, "", 0, statusError(http.StatusBadRequest, "range must be 1d, 7d, 30d or 90d")
	}
	return sc, r, d, nil
}

func cacheKey(sc Scope, parts ...string) string {
	return strconv.FormatBool(sc.All()) + "|" + strings.Join(sc.Owners(), ",") + "|" + strings.Join(parts, "|")
}

// GetEstate is the KPI numbers over the visible owners, or one.
func (s *server) GetEstate(ctx context.Context, req gen.GetEstateRequestObject) (gen.GetEstateResponseObject, error) {
	sc, rng, d, err := estateRequest(ctx, req.Params.Owner, req.Params.Range)
	if err != nil {
		return nil, err
	}
	now := s.opts.Now().UTC()
	key := cacheKey(sc, "estate", rng)
	if v, ok := s.estate.get(key, now); ok {
		if e, ok := v.(gen.Estate); ok {
			return gen.GetEstate200JSONResponse(e), nil
		}
	}
	e, err := s.opts.Reader.Estate(ctx, sc.Store(), now, now.Add(-d))
	if err != nil {
		return nil, err
	}
	s.estate.observe(ctx, s.opts.Logger, e.Timings)
	out := estate(e, rng, now)
	if o := nonEmpty(req.Params.Owner); o != nil {
		lower := strings.ToLower(*o)
		out.Owner = &lower
	}
	s.estate.put(key, now, out)
	return gen.GetEstate200JSONResponse(out), nil
}

func estate(e *store.Estate, rng string, now time.Time) gen.Estate {
	n := e.Numbers
	merged := int(n.MergedInRange)
	out := gen.Estate{
		Range: rng, ComputedAt: now, Repositories: int(n.Repositories), OpenPrs: int(n.OpenPrs),
		Waiting: int(n.Waiting), OldestOpenPrOpenedAt: e.OldestOpen, Runs24h: int(n.Runs24h),
		LostDeliveries24h: int(n.LostDeliveries24h), MergedInRange: &merged,
		AgeBuckets: make([]gen.Bucket, 0, len(ageLabels)), Ecosystems: make([]gen.Bucket, 0, len(e.Ecosystems)),
		TopPackages: make([]gen.Package, 0, len(e.TopPackages)),
	}
	if n.Finished24h > 0 {
		rate := float32(math.Floor(float64(n.Succeeded24h)*1000/float64(n.Finished24h)) / 10)
		out.RunSuccessRate24h = &rate
	}
	a := e.Ages
	for i, c := range []int32{a.Under1d, a.Under7d, a.Under30d, a.Under90d, a.Over90d} {
		out.AgeBuckets = append(out.AgeBuckets, gen.Bucket{Label: ageLabels[i], Count: int(c)})
	}
	for _, eco := range e.Ecosystems {
		out.Ecosystems = append(out.Ecosystems, gen.Bucket{Label: eco.Label, Count: int(eco.Count)})
	}
	for i := range e.TopPackages {
		out.TopPackages = append(out.TopPackages, pkg(&e.TopPackages[i]))
	}
	return out
}

// GetEstateSeries is one metric over the range: hourly for 1d, daily
// otherwise, every bucket present.
func (s *server) GetEstateSeries(ctx context.Context, req gen.GetEstateSeriesRequestObject) (gen.GetEstateSeriesResponseObject, error) {
	sc, rng, d, err := estateRequest(ctx, req.Params.Owner, req.Params.Range)
	if err != nil {
		return nil, err
	}
	metric := req.Params.Metric
	now := s.opts.Now().UTC()
	key := cacheKey(sc, "series", metric, rng)
	if v, ok := s.estate.get(key, now); ok {
		if e, ok := v.(gen.EstateSeries); ok {
			return gen.GetEstateSeries200JSONResponse(e), nil
		}
	}
	step, bucket := 24*time.Hour, "day"
	if d <= 24*time.Hour {
		step, bucket = time.Hour, "hour"
	}
	last := now.Truncate(step)
	origin := last.Add(-(d/step - 1) * step)
	start := time.Now()
	points, err := s.opts.Reader.Series(ctx, sc.Store(), metric, origin, last, step)
	if errors.Is(err, store.ErrUnknownMetric) {
		return nil, statusError(http.StatusBadRequest, "metric must be open_prs, runs, waiting or merged")
	}
	if err != nil {
		return nil, err
	}
	s.estate.observe(ctx, s.opts.Logger, map[string]time.Duration{"series_" + metric: time.Since(start)})
	out := gen.EstateSeries{Metric: metric, Range: rng, Bucket: bucket, Points: make([]gen.SeriesPoint, 0, len(points))}
	for _, p := range points {
		sp := gen.SeriesPoint{At: p.At, Value: p.Value}
		if metric == store.SeriesRuns {
			by := p.By
			if by == nil {
				by = map[string]int{}
			}
			sp.By = &by
		}
		out.Points = append(out.Points, sp)
	}
	s.estate.put(key, now, out)
	return gen.GetEstateSeries200JSONResponse(out), nil
}
