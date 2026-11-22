//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	"github.com/donaldgifford/boop/test/fakegithub"
)

// The store e2e (IMPL-0005 14.1, OQ10): a pinned Postgres in the test
// namespace with the four roles, the mock OIDC issuer as a pod, and the
// chart with store, api and ingest on.
const (
	postgresImage = "postgres:17.12-alpine" // internal/store/storetest's
	stubOIDCImage = "ghcr.io/donaldgifford/boopd-stub-oidc:dev"
	pgSuperuser   = "postgres"
	pgDatabase    = "boopd"
	apiAudience   = "boopd-api"
	// The authz groups: the operator's sees every owner and may recheck;
	// the viewer's sees an owner the stub has no installation for.
	operatorGroup = "platform"
	viewerGroup   = "team-web"
)

// The four Postgres roles; each password is the role name.
var storeRoles = map[string]string{
	"owner-dsn":  "boopd_owner",
	"worker-dsn": "boopd_worker",
	"ingest-dsn": "boopd_ingest",
	"ro-dsn":     "boopd_ro",
}

// storeHarness is the ingest harness with the store and the API on.
type storeHarness struct {
	*ingestHarness
	db     *pgxpool.Pool // as the superuser, through a port-forward
	issuer string        // the mock issuer's URL as the cluster names it
}

// TestStore runs IMPL-0005 14.2's scenarios in order against one install:
// later cases read what earlier ones recorded, and the rebuild runs last
// over all of it.
//
//nolint:tparallel,contextcheck // ordered subtests; Deliver uses t.Context
func TestStore(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 14*time.Minute)
	defer cancel()
	h := installStore(ctx, t)
	repo := h.repos[0]
	repo.HasConfig = true

	t.Run("RunRecorded", func(t *testing.T) {
		h.onboard(ctx, t)
		eventually(t, time.Minute, "run.finished in the store", func() bool {
			return h.count(ctx, t, "events", fmt.Sprintf("kind = 'run.finished' AND repo_id = %d", repo.ID)) >= 1
		})
		if n := h.count(ctx, t, "branch_updates", fmt.Sprintf("repo_id = %d", repo.ID)); n == 0 {
			t.Error("no branch_updates for the run")
		}
		if n := h.count(ctx, t, "dependencies", fmt.Sprintf("repo_id = %d AND purl IS NOT NULL", repo.ID)); n == 0 {
			t.Error("no dependencies with a purl for the run")
		}
	})

	t.Run("PROpenedByBot", func(t *testing.T) {
		pr := fakegithub.PullRequest("opened", &repo, 1, 12, e2eBot, e2eBot)
		h.deliver(t, http.StatusAccepted, "pull_request", pr)
		eventually(t, time.Minute, "the bot's pull request row", func() bool {
			return h.count(ctx, t, "pull_requests", fmt.Sprintf("repo_id = %d AND number = 12", repo.ID)) == 1
		})
		human := fakegithub.PullRequest("opened", &repo, 1, 99, human, human)
		h.deliver(t, http.StatusAccepted, "pull_request", human)
		time.Sleep(3 * time.Second)
		if n := h.count(ctx, t, "pull_requests", fmt.Sprintf("repo_id = %d AND number = 99", repo.ID)); n != 0 {
			t.Errorf("a human's pull request was recorded (%d rows)", n)
		}
	})

	t.Run("DashboardWaiting", func(t *testing.T) {
		const box = "<!-- approve-branch=renovate/react-19.x -->chore(deps): update react to v19 (`react`)"
		unticked := dashboardBody("- [ ] " + box)
		ticked := dashboardBody("- [x] " + box)
		h.deliver(t, http.StatusAccepted, "issues", dashboardEdit(&repo, unticked, "", e2eBot))
		eventually(t, time.Minute, "the dashboard's waiting item", func() bool {
			return h.count(ctx, t, "dashboard_items",
				fmt.Sprintf("repo_id = %d AND section = 'pending-approval' AND branch = 'renovate/react-19.x'", repo.ID)) == 1
		})
		h.deliver(t, http.StatusAccepted, "issues", dashboardEdit(&repo, ticked, unticked, human))
		eventually(t, time.Minute, "dashboard.ticked by the human", func() bool {
			return h.count(ctx, t, "events",
				fmt.Sprintf("kind = 'dashboard.ticked' AND repo_id = %d AND actor_login = '%s'", repo.ID, human)) == 1
		})
	})

	api := h.apiClient(ctx, t)
	t.Run("API_RecheckAudited", func(t *testing.T) { apiRecheckAudited(ctx, t, h, api, repo.ID) })
	t.Run("API_Visibility", func(t *testing.T) { apiVisibility(ctx, t, api, repo.ID) })
	t.Run("API_StatusAggregateOnly", func(t *testing.T) { apiStatusAggregateOnly(ctx, t, api, repo.Slug) })

	t.Run("Rebuild", func(t *testing.T) {
		live := h.projections(ctx, t)
		h.rebuild(ctx, t)
		rebuilt := h.projections(ctx, t)
		for table, want := range live {
			if got := rebuilt[table]; got != want {
				t.Errorf("%s after rebuild differs:\n got %s\nwant %s", table, got, want)
			}
		}
	})
}

// installStore deploys Postgres and the mock issuer, then the chart with
// ingest, the store and the API on, and connects to Postgres. opts run
// after its own values.
func installStore(ctx context.Context, t *testing.T, opts ...chartOption) *storeHarness {
	t.Helper()
	sh := &storeHarness{}
	sh.ingestHarness = installIngest(ctx, t, func(ctx context.Context, t *testing.T, h *chartHarness, values map[string]any) {
		t.Helper()
		deployPostgres(ctx, t, h)
		sh.issuer = deployMockOIDC(ctx, t, h)
		values["store"] = map[string]any{"enabled": true, "existingSecret": "boopd-store"}
		values["api"] = map[string]any{
			"enabled": true, "replicaCount": 1,
			"authz": map[string]any{
				"owners":    map[string]any{operatorGroup: []string{"*"}, viewerGroup: []string{"someone-else"}},
				"operators": []string{operatorGroup},
			},
		}
		boopd := values["boopd"].(map[string]any)
		boopd["api"] = map[string]any{"auth": map[string]any{"issuer": sh.issuer, "audience": apiAudience}}
		for _, opt := range opts {
			opt(ctx, t, h, values)
		}
	})
	port := forward(ctx, t, sh.env, "app=postgres", 5432)
	pool, err := pgxpool.New(ctx, fmt.Sprintf("postgres://%s:%s@127.0.0.1:%d/%s?sslmode=disable", pgSuperuser, pgSuperuser, port, pgDatabase))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	sh.db = pool
	return sh
}

// deployPostgres runs the pinned Postgres with the four roles, the
// database owned by boopd_owner as the store expects, and writes the
// boopd-store Secret with one DSN per role.
func deployPostgres(ctx context.Context, t *testing.T, h *chartHarness) {
	t.Helper()
	cs, ns := h.env.Clientset, h.env.Namespace
	var sql strings.Builder
	for _, role := range storeRoles {
		fmt.Fprintf(&sql, "CREATE ROLE %s LOGIN PASSWORD '%s';\n", role, role)
	}
	fmt.Fprintf(&sql, "ALTER DATABASE %s OWNER TO boopd_owner;\n", pgDatabase)
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "postgres-init"}, Data: map[string]string{"roles.sql": sql.String()}}
	if _, err := cs.CoreV1().ConfigMaps(ns).Create(ctx, cm, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create postgres-init: %v", err)
	}
	host := "postgres." + ns + ".svc:5432"
	dsns := make(map[string][]byte, len(storeRoles))
	for key, role := range storeRoles {
		dsns[key] = fmt.Appendf(nil, "postgres://%s:%s@%s/%s?sslmode=disable", role, role, host, pgDatabase)
	}
	h.secret(ctx, t, "boopd-store", dsns)

	labels := map[string]string{"app": "postgres"}
	emptyDir := corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "postgres"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					// The alpine image's postgres user, so the entrypoint
					// runs without root (the namespace is restricted).
					SecurityContext: restrictedPod(70),
					Containers: []corev1.Container{{
						Name: "postgres", Image: postgresImage, ImagePullPolicy: corev1.PullIfNotPresent,
						Env: []corev1.EnvVar{
							{Name: "POSTGRES_PASSWORD", Value: pgSuperuser},
							{Name: "POSTGRES_DB", Value: pgDatabase},
							{Name: "PGDATA", Value: "/var/lib/postgresql/data/pgdata"},
						},
						Ports: []corev1.ContainerPort{{Name: "postgres", ContainerPort: 5432}},
						ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{
							Command: []string{"pg_isready", "-U", pgSuperuser, "-d", pgDatabase, "-h", "127.0.0.1"},
						}}, PeriodSeconds: 2},
						SecurityContext: restrictedContainer(false),
						VolumeMounts: []corev1.VolumeMount{
							{Name: "data", MountPath: "/var/lib/postgresql/data"},
							{Name: "run", MountPath: "/var/run/postgresql"},
							{Name: "init", MountPath: "/docker-entrypoint-initdb.d", ReadOnly: true},
						},
					}},
					Volumes: []corev1.Volume{
						{Name: "data", VolumeSource: emptyDir},
						{Name: "run", VolumeSource: emptyDir},
						{Name: "init", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: "postgres-init"},
						}}},
					},
				},
			},
		},
	}
	createAndWait(ctx, t, h, dep, 5432)
}

// deployMockOIDC runs the mock issuer over TLS at
// https://mock-oidc.<ns>.svc:9443 with an operator and a viewer, and
// returns that URL.
func deployMockOIDC(ctx context.Context, t *testing.T, h *chartHarness) string {
	t.Helper()
	url := fmt.Sprintf("https://mock-oidc.%s.svc:9443", h.env.Namespace)
	users, err := json.Marshal(map[string]any{
		"operator": map[string]any{"sub": "operator-1", "name": "Ops", "groups": []string{operatorGroup}},
		"viewer":   map[string]any{"sub": "viewer-1", "name": "Viewer", "groups": []string{viewerGroup}},
	})
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{"app": "mock-oidc"}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "mock-oidc"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					SecurityContext: restrictedPod(65532),
					Containers: []corev1.Container{{
						Name: "mock-oidc", Image: stubOIDCImage, ImagePullPolicy: corev1.PullNever,
						Env: []corev1.EnvVar{
							{Name: "ISSUER_PUBLIC_URL", Value: url},
							{Name: "ISSUER_TLS_CERT", Value: "/tls/tls.crt"},
							{Name: "ISSUER_TLS_KEY", Value: "/tls/tls.key"},
							{Name: "ISSUER_API_AUDIENCE", Value: apiAudience},
							{Name: "ISSUER_GROUPS", Value: operatorGroup},
							{Name: "ISSUER_USERS", Value: string(users)},
						},
						Ports: []corev1.ContainerPort{{Name: "https", ContainerPort: 9443}},
						ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
							Path: "/.well-known/openid-configuration", Port: intstr.FromString("https"), Scheme: corev1.URISchemeHTTPS,
						}}, PeriodSeconds: 2},
						SecurityContext: restrictedContainer(true),
						VolumeMounts: []corev1.VolumeMount{
							{Name: "tls", MountPath: "/tls", ReadOnly: true},
							{Name: "tmp", MountPath: "/tmp"},
						},
					}},
					Volumes: []corev1.Volume{
						{Name: "tls", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
							SecretName: "stub-github-tls", DefaultMode: ptr.To[int32](0o444),
						}}},
						{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
					},
				},
			},
		},
	}
	createAndWait(ctx, t, h, dep, 9443)
	return url
}

// createAndWait creates dep and a same-named Service on port, and waits
// for one ready replica.
func createAndWait(ctx context.Context, t *testing.T, h *chartHarness, dep *appsv1.Deployment, port int32) {
	t.Helper()
	cs, ns := h.env.Clientset, h.env.Namespace
	if _, err := cs.AppsV1().Deployments(ns).Create(ctx, dep, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create %s: %v", dep.Name, err)
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: dep.Name},
		Spec: corev1.ServiceSpec{
			Selector: dep.Spec.Selector.MatchLabels,
			Ports:    []corev1.ServicePort{{Port: port, TargetPort: intstr.FromInt32(port)}},
		},
	}
	if _, err := cs.CoreV1().Services(ns).Create(ctx, svc, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create %s service: %v", dep.Name, err)
	}
	eventually(t, 2*time.Minute, dep.Name+" to be ready", func() bool {
		d, err := cs.AppsV1().Deployments(ns).Get(ctx, dep.Name, metav1.GetOptions{})
		return err == nil && d.Status.ReadyReplicas == 1
	})
}

func restrictedPod(uid int64) *corev1.PodSecurityContext {
	return &corev1.PodSecurityContext{
		RunAsNonRoot: ptr.To(true), RunAsUser: ptr.To(uid), RunAsGroup: ptr.To(uid), FSGroup: ptr.To(uid),
		SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}
}

func restrictedContainer(readOnlyRoot bool) *corev1.SecurityContext {
	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: ptr.To(false),
		ReadOnlyRootFilesystem:   ptr.To(readOnlyRoot),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
	}
}

// count is how many rows of table match where.
func (h *storeHarness) count(ctx context.Context, t *testing.T, table, where string) int {
	t.Helper()
	var n int
	if err := h.db.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE "+where).Scan(&n); err != nil {
		t.Fatalf("count %s where %s: %v", table, where, err)
	}
	return n
}

// projections snapshots every projection Rebuild rewrites, as
// internal/store's rebuild test does.
func (h *storeHarness) projections(ctx context.Context, t *testing.T) map[string]string {
	t.Helper()
	tables := []string{
		"installations", "repositories", "runs", "branch_updates", "dependencies",
		"pull_requests", "dashboard_items", "webhook_deliveries", "discovery_passes", "dashboards",
	}
	out := make(map[string]string, len(tables))
	for _, table := range tables {
		var s string
		q := fmt.Sprintf("SELECT coalesce(json_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text), '[]')::text FROM %s t", table)
		if err := h.db.QueryRow(ctx, q).Scan(&s); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		out[table] = s
	}
	return out
}

// rebuild runs `boopd store rebuild` as a Job with the rendered config
// and the owner DSN, the way an operator would, and waits for it.
func (h *storeHarness) rebuild(ctx context.Context, t *testing.T) {
	t.Helper()
	cs, ns := h.env.Clientset, h.env.Namespace
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "store-rebuild"},
		Spec: batchv1.JobSpec{
			BackoffLimit: ptr.To[int32](0),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyNever,
					AutomountServiceAccountToken: ptr.To(false),
					SecurityContext:              restrictedPod(65532),
					Containers: []corev1.Container{{
						Name: "rebuild", Image: boopdImageRepo + ":dev", ImagePullPolicy: corev1.PullNever,
						Args:            []string{"store", "rebuild", "--config", "/etc/boopd/boopd.hcl"},
						SecurityContext: restrictedContainer(true),
						VolumeMounts: []corev1.VolumeMount{
							{Name: "config", MountPath: "/etc/boopd/boopd.hcl", SubPath: "boopd.hcl", ReadOnly: true},
							{Name: "dsn", MountPath: "/etc/boopd/secrets/boopd-store", ReadOnly: true},
						},
					}},
					Volumes: []corev1.Volume{
						{Name: "config", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: "boopd-config"},
						}}},
						{Name: "dsn", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
							SecretName: "boopd-store", Items: []corev1.KeyToPath{{Key: "owner-dsn", Path: "owner-dsn"}},
						}}},
					},
				},
			},
		},
	}
	if _, err := cs.BatchV1().Jobs(ns).Create(ctx, job, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create rebuild job: %v", err)
	}
	eventually(t, 3*time.Minute, "the rebuild to finish", func() bool {
		j, err := cs.BatchV1().Jobs(ns).Get(ctx, job.Name, metav1.GetOptions{})
		if err != nil {
			return false
		}
		if j.Status.Failed > 0 {
			t.Fatalf("rebuild job failed: %+v", j.Status)
		}
		return j.Status.Succeeded == 1
	})
}

// dashboardBody is a Dependency Dashboard with one Pending Approval box.
func dashboardBody(box string) string {
	return "This issue lists Renovate updates and detected dependencies.\n\n" +
		"## Pending Approval\n\nThe following branches are pending approval. To create them, click on a checkbox below.\n\n" +
		" " + box + "\n"
}

// dashboardEdit is the bot's dashboard edited: by the bot (a rewrite,
// from empty) or by a human (a tick, from the previous body).
func dashboardEdit(repo *fakegithub.Repo, body, from, sender string) map[string]any {
	p := fakegithub.IssuesEdited(repo, 1, e2eBot, sender)
	issue := p["issue"].(map[string]any)
	issue["body"] = body
	issue["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	if from == "" {
		delete(p, "changes")
	} else {
		p["changes"] = map[string]any{"body": map[string]any{"from": from}}
	}
	return p
}
