//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.temporal.io/sdk/client"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/yaml"

	"github.com/donaldgifford/boop/internal/jobspec"
	"github.com/donaldgifford/boop/internal/temporal/temporaltest"
	"github.com/donaldgifford/boop/internal/workflows"
	"github.com/donaldgifford/boop/test/fakegithub"
)

// Images the chart e2e runs; `just e2e` and the E2E CI job build and
// import them.
const (
	boopdImageRepo  = "ghcr.io/donaldgifford/boopd"
	stubGitHubImage = "ghcr.io/donaldgifford/boopd-stub-github:dev"
)

// stubState is the stub GitHub's /_stub/state.
type stubState struct {
	Mints   []fakegithub.Mint `json:"mints"`
	Revokes []string          `json:"revokes"`
}

// chartHarness is one helm release of charts/boopd in a test namespace,
// against the stub GitHub and a dev server on the host.
type chartHarness struct {
	env    *Env
	srv    *temporaltest.Server
	repoID int64
}

// TestChart_WorkerRunsUnderChartRBAC (IMPL-0001 task 7.8, the chart
// gate): helm installs the chart with the stub images, the redis
// subchart and Temporal from the harness; the worker becomes ready under
// the chart's Role, one discovery pass starts a RepoWorkflow for the
// repository with the config file only, and one Job completes with its
// log forwarded, its token revoked and no Secret left behind.
func TestChart_WorkerRunsUnderChartRBAC(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	h := installChart(ctx, t)

	handle := h.srv.Client.ScheduleClient().GetHandle(ctx, workflows.DiscoveryScheduleID("e2e"))
	eventually(t, 2*time.Minute, "the discovery schedule", func() bool {
		_, err := handle.Describe(ctx)
		return err == nil
	})
	if err := handle.Trigger(ctx, client.ScheduleTriggerOptions{}); err != nil {
		t.Fatalf("trigger schedule: %v", err)
	}

	var s workflows.RepoState
	eventually(t, 4*time.Minute, "the first run", func() bool {
		v, err := h.srv.Client.QueryWorkflow(ctx, workflows.RepoWorkflowID(h.repoID), "", workflows.StateQuery)
		return err == nil && v.Get(&s) == nil && s.LastRun != nil
	})
	if s.LastRun.Outcome != workflows.OutcomeSucceeded {
		t.Errorf("last run = %+v, want Succeeded", s.LastRun)
	}
	if _, err := h.srv.Client.QueryWorkflow(ctx, workflows.RepoWorkflowID(h.repoID+1), "", workflows.StateQuery); err == nil {
		t.Error("a RepoWorkflow started for the repository without the config file")
	}

	logs := h.workerLogs(ctx, t)
	if strings.Contains(strings.ToLower(logs), "forbidden") {
		t.Errorf("the worker log has a forbidden error under the chart's Role:\n%s", logs)
	}
	if rc := records(logs, "run_complete"); len(rc) != 1 || rc[0]["outcome"] != "Succeeded" {
		t.Errorf("run_complete = %v, want one Succeeded", rc)
	}
	fwd := records(logs, "renovate")
	if len(fwd) == 0 {
		t.Fatalf("no Renovate lines forwarded:\n%s", logs)
	}
	for _, k := range []string{"repo", "workflow_id", "run_id", "attempt", "job", "profile"} {
		if _, ok := fwd[0][k]; !ok {
			t.Errorf("forwarded line lacks %q: %v", k, fwd[0])
		}
	}

	st := h.stubState(ctx, t)
	mints := 0
	for _, m := range st.Mints {
		if len(m.RepoIDs) > 0 {
			mints++
		}
	}
	if mints != 1 || len(st.Revokes) != 1 {
		t.Errorf("run mints %d revokes %d, want 1 1", mints, len(st.Revokes))
	}

	cs, ns := h.env.Clientset, h.env.Namespace
	eventually(t, time.Minute, "the Job and its token Secret to go", func() bool {
		jobs, err := cs.BatchV1().Jobs(ns).List(ctx, metav1.ListOptions{})
		if err != nil || len(jobs.Items) != 0 {
			return false
		}
		secrets, err := cs.CoreV1().Secrets(ns).List(ctx, metav1.ListOptions{LabelSelector: jobspec.LabelRepoID})
		return err == nil && len(secrets.Items) == 0
	})
}

// installChart deploys the stub GitHub and the chart and waits for the
// worker to be ready.
func installChart(ctx context.Context, t *testing.T) *chartHarness {
	t.Helper()
	env := Setup(t) //nolint:contextcheck // the namespace cleanup outlives ctx
	srv := temporaltest.Start(t, temporaltest.ListenOnAllInterfaces())
	h := &chartHarness{env: env, srv: srv, repoID: quickRepoID(t, 10*time.Minute, 10*time.Second)}

	h.deployStubGitHub(ctx, t)
	gh, err := fakegithub.NewFake()
	if err != nil {
		t.Fatal(err)
	}
	h.secret(ctx, t, "e2e-app", map[string][]byte{"private-key.pem": gh.PEM})

	_, port, err := net.SplitHostPort(srv.Config.Address)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]any{
		"image":        map[string]any{"repository": boopdImageRepo, "tag": "dev", "pullPolicy": "Never"},
		"replicaCount": 1,
		"worker":       map[string]any{"buildId": "e2e-" + env.Namespace},
		"temporal":     map[string]any{"address": "host.k3d.internal:" + port},
		"extraEnv":     []any{map[string]any{"name": "SSL_CERT_FILE", "value": "/etc/boopd/e2e-ca/ca.crt"}},
		"extraVolumes": []any{map[string]any{"name": "e2e-ca", "secret": map[string]any{"secretName": "e2e-ca"}}},
		"extraVolumeMounts": []any{
			map[string]any{"name": "e2e-ca", "mountPath": "/etc/boopd/e2e-ca", "readOnly": true},
		},
		"boopd": map[string]any{
			"renovate": map[string]any{"image": stubImageDigest(ctx, t, env.StubImage)},
			"runs":     map[string]any{"pendingTimeout": "2m"},
			"profiles": map[string]any{
				"baseline": map[string]any{"pod": map[string]any{"resources": map[string]any{
					"requests": map[string]any{"cpu": "50m", "memory": "64Mi"},
					"limits":   map[string]any{"cpu": "500m", "memory": "256Mi"},
				}}},
				"python": map[string]any{"pod": map[string]any{"resources": map[string]any{
					"limits": map[string]any{"memory": "256Mi"},
				}}},
			},
			"apps": map[string]any{"e2e": map[string]any{
				"endpoint":            fmt.Sprintf("https://stub-github.%s.svc:8443/", env.Namespace),
				"appId":               1,
				"cadence":             "10m",
				"privateKeySecretRef": map[string]any{"name": "e2e-app", "key": "private-key.pem"},
				"discovery":           map[string]any{"every": "1h", "probe": "graphql"},
				"budget":              map[string]any{"maxConcurrentRuns": 2},
			}},
		},
	}
	b, err := yaml.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(file, b, 0o600); err != nil {
		t.Fatal(err)
	}
	helm(ctx, t, "upgrade", "--install", "boopd", filepath.Join("..", "..", "charts", "boopd"),
		"--namespace", env.Namespace, "--values", file, "--wait", "--timeout", "4m")
	t.Cleanup(func() { //nolint:contextcheck // cleanup runs after ctx is canceled
		if t.Failed() {
			t.Logf("worker log:\n%s", h.workerLogs(context.Background(), t))
		}
		helm(context.Background(), t, "uninstall", "boopd", "--namespace", env.Namespace, "--wait=false")
	})
	return h
}

// deployStubGitHub serves the fake GitHub over TLS at
// stub-github.<ns>.svc:8443 with a certificate from a CA the worker
// trusts through SSL_CERT_FILE.
func (h *chartHarness) deployStubGitHub(ctx context.Context, t *testing.T) {
	t.Helper()
	ns := h.env.Namespace
	caPEM, certPEM, keyPEM := issue(t, "stub-github", "stub-github."+ns+".svc")
	h.secret(ctx, t, "e2e-ca", map[string][]byte{"ca.crt": caPEM})
	h.secret(ctx, t, "stub-github-tls", map[string][]byte{"tls.crt": certPEM, "tls.key": keyPEM})

	fixture, err := json.Marshal(map[string]any{
		"installations": []fakegithub.Installation{{ID: 1, Account: "acme"}},
		"repos": []fakegithub.Repo{
			{
				ID: h.repoID, NodeID: "R_on", Slug: runSlug, DefaultBranch: "main",
				HasConfig: true, Config: `{"extends":["config:recommended"]}`,
			},
			{ID: h.repoID + 1, NodeID: "R_off", Slug: "boop-e2e/no-config", DefaultBranch: "main"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	cs := h.env.Clientset
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "stub-github-fixture"},
		Data:       map[string]string{"fixture.json": string(fixture)},
	}
	if _, err := cs.CoreV1().ConfigMaps(ns).Create(ctx, cm, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create fixture: %v", err)
	}

	labels := map[string]string{"app": "stub-github"}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "stub-github"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   ptr.To(true),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{{
						Name:            "stub-github",
						Image:           stubGitHubImage,
						ImagePullPolicy: corev1.PullNever,
						Env: []corev1.EnvVar{
							{Name: "STUB_GITHUB_TLS_CERT", Value: "/tls/tls.crt"},
							{Name: "STUB_GITHUB_TLS_KEY", Value: "/tls/tls.key"},
							{Name: "STUB_GITHUB_FIXTURE", Value: "/fixture/fixture.json"},
						},
						Ports: []corev1.ContainerPort{{Name: "https", ContainerPort: 8443}},
						ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
							Path: "/_stub/healthz", Port: intstr.FromString("https"), Scheme: corev1.URISchemeHTTPS,
						}}},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(false),
							ReadOnlyRootFilesystem:   ptr.To(true),
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "tls", MountPath: "/tls", ReadOnly: true},
							{Name: "fixture", MountPath: "/fixture", ReadOnly: true},
						},
					}},
					Volumes: []corev1.Volume{
						{Name: "tls", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
							SecretName: "stub-github-tls", DefaultMode: ptr.To[int32](0o444),
						}}},
						{Name: "fixture", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: "stub-github-fixture"},
						}}},
					},
				},
			},
		},
	}
	if _, err := cs.AppsV1().Deployments(ns).Create(ctx, dep, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create stub-github: %v", err)
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "stub-github"},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports:    []corev1.ServicePort{{Name: "https", Port: 8443, TargetPort: intstr.FromString("https")}},
		},
	}
	if _, err := cs.CoreV1().Services(ns).Create(ctx, svc, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create stub-github service: %v", err)
	}
	eventually(t, 2*time.Minute, "stub-github to be ready", func() bool {
		d, err := cs.AppsV1().Deployments(ns).Get(ctx, "stub-github", metav1.GetOptions{})
		return err == nil && d.Status.ReadyReplicas == 1
	})
}

func (h *chartHarness) secret(ctx context.Context, t *testing.T, name string, data map[string][]byte) {
	t.Helper()
	s := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name}, Data: data}
	if _, err := h.env.Clientset.CoreV1().Secrets(h.env.Namespace).Create(ctx, s, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create secret %s: %v", name, err)
	}
}

// stubState reads the stub's recorded mints and revokes through the API
// server's service proxy.
func (h *chartHarness) stubState(ctx context.Context, t *testing.T) stubState {
	t.Helper()
	raw, err := h.env.Clientset.CoreV1().Services(h.env.Namespace).
		ProxyGet("https", "stub-github", "8443", "_stub/state", nil).DoRaw(ctx)
	if err != nil {
		t.Fatalf("stub state: %v", err)
	}
	var st stubState
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("decode stub state %s: %v", raw, err)
	}
	return st
}

// workerLogs is the worker pod's log so far.
func (h *chartHarness) workerLogs(ctx context.Context, t *testing.T) string {
	t.Helper()
	cs, ns := h.env.Clientset, h.env.Namespace
	pods, err := cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=boopd,app.kubernetes.io/instance=boopd",
	})
	if err != nil || len(pods.Items) == 0 {
		return fmt.Sprintf("(no worker pod: %v)", err)
	}
	raw, err := cs.CoreV1().Pods(ns).GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{}).DoRaw(ctx)
	if err != nil {
		return fmt.Sprintf("(worker log: %v)", err)
	}
	return string(raw)
}

// records returns the JSON log records in logs whose msg is msg.
func records(logs, msg string) []map[string]any {
	var out []map[string]any
	for _, l := range strings.Split(logs, "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(l), &rec) == nil && rec["msg"] == msg {
			out = append(out, rec)
		}
	}
	return out
}

func helm(ctx context.Context, t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, "helm", args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("helm %s: %v\n%s", strings.Join(args, " "), err, out.String())
	}
}

// stubImageDigest pins image by the digest containerd in the k3d node
// holds for it: boopd's config refuses an image without one. An imported
// image has no repo digest (it was never pulled), so the kubelet could
// not find it by digest; tagging it with its digested name adds one.
// BOOPD_E2E_STUB_DIGEST overrides the lookup.
func stubImageDigest(ctx context.Context, t *testing.T, image string) string {
	t.Helper()
	if d := os.Getenv("BOOPD_E2E_STUB_DIGEST"); d != "" {
		return image + "@" + d
	}
	cluster := os.Getenv("BOOPD_E2E_CLUSTER")
	if cluster == "" {
		cluster = "boop"
	}
	ctr := func(args ...string) string {
		t.Helper()
		full := append([]string{"exec", "k3d-" + cluster + "-server-0", "ctr", "-n", "k8s.io", "images"}, args...)
		out, err := exec.CommandContext(ctx, "docker", full...).CombinedOutput()
		if err != nil {
			t.Fatalf("ctr images %s in the k3d node: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	out := ctr("ls", "name=="+image)
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) > 2 && f[0] == image && strings.HasPrefix(f[2], "sha256:") {
			repo := image
			if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
				repo = image[:i]
			}
			ctr("tag", "--force", image, repo+"@"+f[2])
			return image + "@" + f[2]
		}
	}
	t.Fatalf("no digest for %s in the k3d node:\n%s", image, out)
	return ""
}

// issue returns a fresh CA and a serving certificate for names, PEM.
func issue(t *testing.T, names ...string) (caPEM, certPEM, keyPEM []byte) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "boopd e2e CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}
