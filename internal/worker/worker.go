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

package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"

	"go.temporal.io/sdk/client"
	authv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/donaldgifford/boop/internal/activities"
	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/kube"
	"github.com/donaldgifford/boop/internal/observability"
	"github.com/donaldgifford/boop/internal/temporal"
	"github.com/donaldgifford/boop/internal/workflows"
)

// Options are everything the worker role runs with. cmd/boopd builds
// them from the config file and the environment; the e2e suite builds
// them against k3d, the dev server and a fake GitHub.
type Options struct {
	// Config is loaded, with ReadSecrets done.
	Config *config.Config
	// Temporal is connected to the namespace.
	Temporal  client.Client
	Namespace string
	Worker    temporal.WorkerConfig
	// Clientset reaches the cluster Jobs run in.
	Clientset kubernetes.Interface
	Logger    *slog.Logger
	// RunLog receives forwarded Renovate lines; stdout JSON when nil.
	RunLog  *slog.Logger
	Metrics *observability.Metrics
	// GitHub overrides the real clients (tests).
	GitHub activities.GitHub
	// Timings overrides the run clocks (tests).
	Timings activities.RunTimings

	// ListenAddr serves /healthz and /readyz; MetricsAddr serves
	// MetricsHandler at /metrics. Empty skips the server.
	ListenAddr     string
	MetricsAddr    string
	MetricsHandler http.Handler
}

// Run is the worker role (IMPL-0001 task 6.4): register the search
// attributes, start the versioned worker with every workflow and
// activity, promote its build, ensure the discovery schedules and serve
// health and metrics. When ctx ends it stops polling and waits up to
// the worker's stop timeout, so runs in flight reach their soft
// deadlines.
func Run(ctx context.Context, o *Options) error {
	log := o.Logger
	if log == nil {
		log = slog.Default()
	}
	if err := temporal.EnsureSearchAttributes(ctx, o.Temporal, o.Namespace, workflows.SearchAttributes); err != nil {
		return err
	}

	tq := o.Worker.TaskQueue
	var metrics activities.Metrics = activities.NopMetrics{}
	if o.Metrics != nil {
		metrics = o.Metrics
	}
	runner := kube.NewRunner(o.Clientset, o.Config.Runs.Namespace)
	acts := activities.New(&activities.Deps{
		Config: o.Config, GitHub: o.GitHub, Temporal: o.Temporal, TaskQueue: tq, Runner: runner,
		Metrics: metrics, Logger: log, RunLog: o.RunLog, Timings: o.Timings,
	})
	budget := activities.NewBudget(o.Temporal, tq, budgetConfig(o.Config))

	w := temporal.NewWorker(o.Temporal, &o.Worker)
	workflows.Register(w)
	acts.Register(w)
	budget.Register(w)

	var started atomic.Bool
	health := readiness(o, &started)

	srvCtx, stopServers := context.WithCancel(context.WithoutCancel(ctx))
	defer stopServers()
	var wg sync.WaitGroup
	serve := func(name, addr string, h http.Handler) {
		if addr == "" || h == nil {
			return
		}
		wg.Go(func() {
			if err := observability.Serve(srvCtx, addr, h); err != nil {
				log.Error("server failed", "server", name, "addr", addr, "error", err)
			}
		})
	}
	serve("health", o.ListenAddr, health.Handler())
	if o.MetricsHandler != nil {
		mux := http.NewServeMux()
		mux.Handle("GET /metrics", o.MetricsHandler)
		serve("metrics", o.MetricsAddr, mux)
	}

	if err := w.Start(); err != nil {
		return fmt.Errorf("start worker: %w", err)
	}
	if err := temporal.PromoteBuild(ctx, o.Temporal, o.Worker.DeploymentName(), o.Worker.BuildID, log); err != nil {
		w.Stop()
		return fmt.Errorf("promote build %s: %w", o.Worker.BuildID, err)
	}
	if err := EnsureSchedules(ctx, o.Temporal, o.Config, tq); err != nil {
		w.Stop()
		return err
	}
	started.Store(true)
	log.Info("worker started", "task_queue", tq, "build_id", o.Worker.BuildID, "namespace", o.Namespace)

	<-ctx.Done()
	log.Info("stopping: no new tasks; runs in flight continue to their soft deadlines", "stop_timeout", o.Worker.StopTimeout)
	started.Store(false)
	w.Stop()
	stopServers()
	wg.Wait()
	log.Info("worker stopped")
	return nil
}

// readiness is IMPL-0001 OQ9's ready: connected to Temporal, the worker
// started with its build the deployment's current version, and the API
// server allows creating Jobs in the namespace.
func readiness(o *Options, started *atomic.Bool) *observability.Health {
	health := observability.NewHealth()
	health.Add(observability.Check{Name: "temporal", Check: func(ctx context.Context) error { return temporal.Ping(ctx, o.Temporal) }})
	health.Add(observability.Check{Name: "worker", Check: func(context.Context) error {
		if !started.Load() {
			return errors.New("not started")
		}
		return nil
	}})
	health.Add(observability.Check{Name: "current-version", Check: func(ctx context.Context) error {
		return temporal.RequireCurrentVersion(ctx, o.Temporal, o.Worker.DeploymentName(), o.Worker.BuildID)
	}})
	health.Add(observability.Check{Name: "kubernetes", Check: func(ctx context.Context) error {
		return canCreateJobs(ctx, o.Clientset, o.Config.Runs.Namespace)
	}})
	return health
}

// budgetConfig is the first app's budget block: the spike runs one App.
func budgetConfig(cfg *config.Config) activities.BudgetConfig {
	bc := activities.DefaultBudgetConfig()
	if len(cfg.Apps) == 0 {
		return bc
	}
	b := cfg.Apps[0].Budget
	if b.ReserveFraction > 0 {
		bc.ReserveFraction = b.ReserveFraction
	}
	bc.MaxConcurrentRuns = b.MaxConcurrentRuns
	for k, v := range b.DefaultEstimate {
		bc.DefaultEstimates[k] = float64(v)
	}
	return bc
}

// canCreateJobs is the readiness check DESIGN-0001 names: the API
// server answers a SelfSubjectAccessReview for create jobs.
func canCreateJobs(ctx context.Context, cs kubernetes.Interface, namespace string) error {
	review, err := cs.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authv1.SelfSubjectAccessReview{
		Spec: authv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authv1.ResourceAttributes{
			Namespace: namespace, Verb: "create", Group: "batch", Resource: "jobs",
		}},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("access review: %w", err)
	}
	if !review.Status.Allowed {
		return fmt.Errorf("may not create jobs in %s: %s", namespace, review.Status.Reason)
	}
	return nil
}
