//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	"k8s.io/utils/ptr"

	"github.com/donaldgifford/boop/internal/workflows"
	"github.com/donaldgifford/boop/test/fakegithub"
)

// The ingest e2e's App: app id 1 in the stub, bot login "e2e[bot]".
const (
	webhookSecret = "e2e-webhook-secret"
	e2eBot        = "e2e[bot]"
	human         = "octocat"
	forwarder     = "temporal-forwarder"
)

// ingestHarness is the chart with ingest on, Temporal behind an
// in-cluster forwarder, and a port-forward to the ingest pod.
type ingestHarness struct {
	*chartHarness
	gh    *fakegithub.Server // signs deliveries and keeps what ingest answered
	url   string             // the webhook URL through the port-forward
	repos []fakegithub.Repo  // the stub's repositories
}

// TestIngest runs DESIGN-0003's scenarios in order against one install
// (IMPL-0004 Phase 11): later cases build on earlier ones, and the
// delivery audit counts the failure TemporalDown leaves. The subtests
// are ordered, so they do not run in parallel; fakegithub.Deliver bounds
// each delivery by the test's own context.
//
//nolint:tparallel,contextcheck // ordered subtests; Deliver uses t.Context
func TestIngest(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 14*time.Minute)
	defer cancel()
	h := installIngest(ctx, t)
	repo := h.repos[0]
	repo.HasConfig = true

	t.Run("DashboardTickRuns", func(t *testing.T) {
		h.onboard(ctx, t)
		before := time.Now()
		h.deliver(t, http.StatusAccepted, "issues", fakegithub.IssuesEdited(&repo, 1, e2eBot, human))
		s := h.waitRun(ctx, t, repo.ID, before, 3*time.Minute)
		if s.LastRun.Outcome != workflows.OutcomeSucceeded {
			t.Errorf("dashboard run = %+v, want Succeeded", s.LastRun)
		}
	})

	t.Run("OwnBotIgnored", func(t *testing.T) {
		before := h.state(ctx, t, repo.ID)
		h.deliver(t, http.StatusAccepted, "issues", fakegithub.IssuesEdited(&repo, 1, e2eBot, e2eBot))
		time.Sleep(10 * time.Second) // well past the 2s settle
		after := h.state(ctx, t, repo.ID)
		if !after.EventDue.IsZero() || !after.LastRun.StartedAt.Equal(before.LastRun.StartedAt) {
			t.Errorf("own edit scheduled or ran: event due %v, last run %+v", after.EventDue, after.LastRun)
		}
	})

	added := fakegithub.Repo{ID: repo.ID + 2, NodeID: "R_added", Slug: "boop-e2e/added", DefaultBranch: "main"}
	t.Run("ConfigPushOnboards", func(t *testing.T) {
		added.HasConfig, added.ConfigPath, added.Config = true, "renovate.json5", `{extends: ["config:recommended"]}`
		h.setRepos(ctx, t, append(h.repos, added))
		before := time.Now()
		h.deliver(t, http.StatusAccepted, "push",
			fakegithub.Push(&added, 1, "refs/heads/main", human, fakegithub.Commit{Added: []string{"renovate.json5"}}))
		h.waitRun(ctx, t, added.ID, before, 3*time.Minute)
	})

	t.Run("ConfigRemovedOffboards", func(t *testing.T) {
		added.HasConfig = false
		h.setRepos(ctx, t, append(h.repos, added))
		h.deliver(t, http.StatusAccepted, "push",
			fakegithub.Push(&added, 1, "refs/heads/main", human, fakegithub.Commit{Removed: []string{"renovate.json5"}}))
		h.waitClosed(ctx, t, workflows.RepoWorkflowID(added.ID))
	})

	discovered := fakegithub.Repo{
		ID: repo.ID + 3, NodeID: "R_discovered", Slug: "boop-e2e/discovered", DefaultBranch: "main",
		HasConfig: true, ConfigPath: "renovate.json5", Config: `{extends: ["config:recommended"]}`,
	}
	t.Run("RepositoriesAddedDiscovers", func(t *testing.T) {
		h.repos = append(h.repos, discovered)
		h.setRepos(ctx, t, append(h.repos, added))
		h.deliver(t, http.StatusAccepted, "installation_repositories",
			fakegithub.InstallationRepositories("added", fakegithub.Installation{ID: 1, Account: "acme"}, human, discovered))
		h.waitClosed(ctx, t, workflows.TargetedDiscoveryID("e2e", 1))
		eventually(t, time.Minute, "the discovered repository's workflow", func() bool {
			_, ok := h.query(ctx, discovered.ID)
			return ok
		})
	})

	t.Run("InstallationDeleted", func(t *testing.T) {
		h.stub(ctx, t, http.MethodDelete, "_stub/installations/1", nil)
		h.deliver(t, http.StatusAccepted, "installation",
			fakegithub.InstallationEvent("deleted", fakegithub.Installation{ID: 1, Account: "acme"}, human, repo, discovered))
		h.waitClosed(ctx, t, workflows.RepoWorkflowID(repo.ID))
		h.waitClosed(ctx, t, workflows.RepoWorkflowID(discovered.ID))
	})

	t.Run("BadSignature", func(t *testing.T) {
		stray := fakegithub.Repo{ID: repo.ID + 4, Slug: "boop-e2e/stray", DefaultBranch: "main"}
		d := h.gh.Deliver(t, h.url, "push",
			fakegithub.Push(&stray, 1, "refs/heads/main", human, fakegithub.Commit{Added: []string{"renovate.json5"}}),
			fakegithub.WithSecret("not-the-secret"))
		if d.StatusCode != http.StatusUnauthorized {
			t.Errorf("bad signature = %d, want 401", d.StatusCode)
		}
		time.Sleep(5 * time.Second)
		if _, err := h.srv.Client.DescribeWorkflowExecution(ctx, workflows.RepoWorkflowID(stray.ID), ""); err == nil {
			t.Error("a rejected delivery started a workflow")
		}
	})

	t.Run("TemporalDown", func(t *testing.T) {
		h.scaleForwarder(ctx, t, 0)
		d := h.deliver(t, http.StatusServiceUnavailable, "issues", fakegithub.IssuesEdited(&repo, 1, e2eBot, human))
		// GitHub lists the 503; the audit reads it from the stub.
		body, err := json.Marshal(map[string]any{
			"event": "issues", "guid": d.GUID, "status": d.StatusCode,
			"payload": fakegithub.IssuesEdited(&repo, 1, e2eBot, human),
		})
		if err != nil {
			t.Fatal(err)
		}
		h.stub(ctx, t, http.MethodPost, "_stub/deliveries", body)
		h.scaleForwarder(ctx, t, 1)
	})

	t.Run("DeliveryAudit_RecordsFailures", func(t *testing.T) {
		eventually(t, 4*time.Minute, "the audit to count the failed delivery", func() bool {
			return h.workerMetric(ctx, t, "boopd_ingest_deliveries_failed_total", `event="issues"`) >= 1
		})
	})
}

// installIngest installs the chart with ingest on behind a Temporal
// forwarder, and port-forwards to the ingest pod.
func installIngest(ctx context.Context, t *testing.T) *ingestHarness {
	t.Helper()
	ih := &ingestHarness{gh: newDeliverer(t)}
	ih.chartHarness = installChart(ctx, t, func(ctx context.Context, t *testing.T, h *chartHarness, values map[string]any) {
		t.Helper()
		h.secret(ctx, t, "e2e-webhook", map[string][]byte{"secret": []byte(webhookSecret)})
		_, port, err := net.SplitHostPort(h.srv.Config.Address)
		if err != nil {
			t.Fatal(err)
		}
		deployForwarder(ctx, t, h, "host.k3d.internal:"+port)
		values["temporal"] = map[string]any{"address": fmt.Sprintf("%s.%s.svc:7233", forwarder, h.env.Namespace)}
		values["ingest"] = map[string]any{"enabled": true, "replicaCount": 1}
		boopd := values["boopd"].(map[string]any)
		boopd["ingest"] = map[string]any{"settle": "2s", "minInterval": "1s", "auditEvery": "1m"}
		app := boopd["apps"].(map[string]any)["e2e"].(map[string]any)
		app["webhook"] = map[string]any{"secretRef": map[string]any{"name": "e2e-webhook", "key": "secret"}}
	})
	ih.repos = []fakegithub.Repo{
		{
			ID: ih.repoID, NodeID: "R_on", Slug: runSlug, DefaultBranch: "main",
			HasConfig: true, ConfigPath: ".github/renovate.json5", Config: `{extends: ["config:recommended",],}`,
		},
		{ID: ih.repoID + 1, NodeID: "R_off", Slug: "boop-e2e/no-config", DefaultBranch: "main"},
	}
	ih.url = ih.portForward(ctx, t) + "/webhooks/github"
	return ih
}

// newDeliverer is a fakegithub used only to sign and send deliveries as
// app 1 with the e2e webhook secret.
func newDeliverer(t *testing.T) *fakegithub.Server {
	t.Helper()
	gh, err := fakegithub.NewFake()
	if err != nil {
		t.Fatal(err)
	}
	gh.SetAppID(1)
	gh.SetWebhookSecret(webhookSecret)
	return gh
}

// deliver sends a signed delivery and checks ingest's answer.
func (h *ingestHarness) deliver(t *testing.T, want int, event string, payload map[string]any) fakegithub.Delivery {
	t.Helper()
	d := h.gh.Deliver(t, h.url, event, payload)
	if d.StatusCode != want {
		t.Fatalf("%s delivery = %d, want %d", event, d.StatusCode, want)
	}
	return d
}

// onboard runs discovery once and waits for the main repository's first
// run.
func (h *ingestHarness) onboard(ctx context.Context, t *testing.T) {
	t.Helper()
	handle := h.srv.Client.ScheduleClient().GetHandle(ctx, workflows.DiscoveryScheduleID("e2e"))
	eventually(t, 2*time.Minute, "the discovery schedule", func() bool {
		_, err := handle.Describe(ctx)
		return err == nil
	})
	if err := handle.Trigger(ctx, client.ScheduleTriggerOptions{}); err != nil {
		t.Fatalf("trigger schedule: %v", err)
	}
	h.waitRun(ctx, t, h.repoID, time.Time{}, 4*time.Minute)
}

func (h *ingestHarness) query(ctx context.Context, id int64) (workflows.RepoState, bool) {
	var s workflows.RepoState
	v, err := h.srv.Client.QueryWorkflow(ctx, workflows.RepoWorkflowID(id), "", workflows.StateQuery)
	return s, err == nil && v.Get(&s) == nil
}

func (h *ingestHarness) state(ctx context.Context, t *testing.T, id int64) workflows.RepoState {
	t.Helper()
	s, ok := h.query(ctx, id)
	if !ok || s.LastRun == nil {
		t.Fatalf("repo %d has no state or no run: %+v", id, s)
	}
	return s
}

// waitRun waits for a run of repository id that started after since.
func (h *ingestHarness) waitRun(ctx context.Context, t *testing.T, id int64, since time.Time, d time.Duration) workflows.RepoState {
	t.Helper()
	var s workflows.RepoState
	eventually(t, d, fmt.Sprintf("a run of repo %d", id), func() bool {
		var ok bool
		s, ok = h.query(ctx, id)
		return ok && s.LastRun != nil && !s.LastRun.FinishedAt.Before(since) && s.LastRun.StartedAt.After(since)
	})
	return s
}

// waitClosed waits for workflow id's latest run to complete.
func (h *ingestHarness) waitClosed(ctx context.Context, t *testing.T, id string) {
	t.Helper()
	eventually(t, 3*time.Minute, id+" to complete", func() bool {
		d, err := h.srv.Client.DescribeWorkflowExecution(ctx, id, "")
		return err == nil && d.GetWorkflowExecutionInfo().GetStatus() == enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED
	})
}

func (h *ingestHarness) setRepos(ctx context.Context, t *testing.T, repos []fakegithub.Repo) {
	t.Helper()
	b, err := json.Marshal(repos)
	if err != nil {
		t.Fatal(err)
	}
	h.stub(ctx, t, http.MethodPost, "_stub/repos", b)
}

// stub calls a stub-github control endpoint through the service proxy.
func (h *ingestHarness) stub(ctx context.Context, t *testing.T, method, path string, body []byte) {
	t.Helper()
	req := h.env.Clientset.CoreV1().RESTClient().Verb(method).Namespace(h.env.Namespace).
		Resource("services").Name("https:stub-github:8443").SubResource("proxy").Suffix(path)
	if body != nil {
		req = req.Body(body)
	}
	if _, err := req.DoRaw(ctx); err != nil {
		t.Fatalf("stub %s %s: %v", method, path, err)
	}
}

// workerMetric sums the worker's samples of name whose labels contain
// match.
func (h *ingestHarness) workerMetric(ctx context.Context, t *testing.T, name, match string) float64 {
	t.Helper()
	raw, err := h.env.Clientset.CoreV1().Services(h.env.Namespace).
		ProxyGet("http", "boopd", "9090", "metrics", nil).DoRaw(ctx)
	if err != nil {
		t.Logf("worker metrics: %v", err)
		return 0
	}
	total := 0.0
	sc := bufio.NewScanner(strings.NewReader(string(raw)))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, name+"{") || !strings.Contains(line, match) {
			continue
		}
		if v, err := strconv.ParseFloat(line[strings.LastIndex(line, " ")+1:], 64); err == nil {
			total += v
		}
	}
	return total
}

// portForward forwards a local port to the ingest pod's webhook port and
// returns its base URL.
func (h *ingestHarness) portForward(ctx context.Context, t *testing.T) string {
	t.Helper()
	cs, ns := h.env.Clientset, h.env.Namespace
	pods, err := cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=boopd-ingest,app.kubernetes.io/instance=boopd",
		FieldSelector: "status.phase=Running",
	})
	if err != nil || len(pods.Items) == 0 {
		t.Fatalf("no running ingest pod: %v", err)
	}
	transport, upgrader, err := spdy.RoundTripperFor(h.env.Config)
	if err != nil {
		t.Fatal(err)
	}
	url := cs.CoreV1().RESTClient().Post().Namespace(ns).Resource("pods").
		Name(pods.Items[0].Name).SubResource("portforward").URL()
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, url)
	stop, ready := make(chan struct{}), make(chan struct{})
	fw, err := portforward.NewOnAddresses(dialer, []string{"127.0.0.1"}, []string{"0:8080"}, stop, ready, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	errc := make(chan error, 1)
	go func() { errc <- fw.ForwardPorts() }()
	t.Cleanup(func() { close(stop) })
	select {
	case <-ready:
	case err := <-errc:
		t.Fatalf("port-forward: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("port-forward not ready")
	}
	ports, err := fw.GetPorts()
	if err != nil || len(ports) == 0 {
		t.Fatalf("port-forward ports: %v", err)
	}
	return fmt.Sprintf("http://127.0.0.1:%d", ports[0].Local)
}

// deployForwarder runs the stub-github image as a TCP forwarder to the
// host's dev server at temporal-forwarder.<ns>.svc:7233, so a scenario
// can take Temporal away by scaling it to zero.
func deployForwarder(ctx context.Context, t *testing.T, h *chartHarness, to string) {
	t.Helper()
	cs, ns := h.env.Clientset, h.env.Namespace
	labels := map[string]string{"app": forwarder}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: forwarder},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](1),
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					TerminationGracePeriodSeconds: ptr.To[int64](0),
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   ptr.To(true),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{{
						Name:            forwarder,
						Image:           stubGitHubImage,
						ImagePullPolicy: corev1.PullNever,
						Env:             []corev1.EnvVar{{Name: "STUB_FORWARD_TO", Value: to}},
						Ports:           []corev1.ContainerPort{{Name: "grpc", ContainerPort: 7233}},
						ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
							TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromString("grpc")},
						}},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(false),
							ReadOnlyRootFilesystem:   ptr.To(true),
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						},
					}},
				},
			},
		},
	}
	if _, err := cs.AppsV1().Deployments(ns).Create(ctx, dep, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create forwarder: %v", err)
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: forwarder},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports:    []corev1.ServicePort{{Name: "grpc", Port: 7233, TargetPort: intstr.FromString("grpc")}},
		},
	}
	if _, err := cs.CoreV1().Services(ns).Create(ctx, svc, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create forwarder service: %v", err)
	}
	waitReplicas(ctx, t, h, 1)
}

func (h *ingestHarness) scaleForwarder(ctx context.Context, t *testing.T, n int32) {
	t.Helper()
	deps := h.env.Clientset.AppsV1().Deployments(h.env.Namespace)
	scale, err := deps.GetScale(ctx, forwarder, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("forwarder scale: %v", err)
	}
	scale.Spec.Replicas = n
	if _, err := deps.UpdateScale(ctx, forwarder, scale, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("scale forwarder to %d: %v", n, err)
	}
	waitReplicas(ctx, t, h.chartHarness, n)
}

// waitReplicas waits for the forwarder to have exactly n ready pods and
// no others.
func waitReplicas(ctx context.Context, t *testing.T, h *chartHarness, n int32) {
	t.Helper()
	cs, ns := h.env.Clientset, h.env.Namespace
	eventually(t, 2*time.Minute, fmt.Sprintf("%d forwarder pods", n), func() bool {
		d, err := cs.AppsV1().Deployments(ns).Get(ctx, forwarder, metav1.GetOptions{})
		if err != nil || d.Status.ReadyReplicas != n {
			return false
		}
		pods, err := cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: "app=" + forwarder})
		return err == nil && int32(len(pods.Items)) == n
	})
}
