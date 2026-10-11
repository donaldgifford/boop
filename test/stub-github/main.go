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

// Command stub-github serves test/fakegithub over TLS, so a boopd
// worker running in a cluster has a GitHub to talk to (IMPL-0001 task
// 7.8). It is an e2e image only and is never published.
//
// Environment:
//
//	STUB_GITHUB_ADDR      listen address (default :8443)
//	STUB_GITHUB_TLS_CERT  PEM certificate file (required)
//	STUB_GITHUB_TLS_KEY   PEM key file (required)
//	STUB_GITHUB_FIXTURE   JSON file: {"installations": [...], "repos": [...]}
//	                      in fakegithub's Installation and Repo shapes
//
// GET /_stub/state returns the recorded mints, revokes, probe queries
// and rate reads for assertions.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/donaldgifford/boop/test/fakegithub"
)

// fixture is what STUB_GITHUB_FIXTURE holds.
type fixture struct {
	Installations []fakegithub.Installation `json:"installations"`
	Repos         []fakegithub.Repo         `json:"repos"`
}

// state is the /_stub/state body.
type state struct {
	Mints        []fakegithub.Mint `json:"mints"`
	Revokes      []string          `json:"revokes"`
	ProbeQueries int               `json:"probeQueries"`
	RateReads    int               `json:"rateReads"`
}

func main() {
	if err := run(); err != nil {
		slog.Error("stub-github", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cert, key := os.Getenv("STUB_GITHUB_TLS_CERT"), os.Getenv("STUB_GITHUB_TLS_KEY")
	if cert == "" || key == "" {
		return errors.New("STUB_GITHUB_TLS_CERT and STUB_GITHUB_TLS_KEY are required")
	}
	gh, err := fakegithub.NewFake()
	if err != nil {
		return err
	}
	if path := os.Getenv("STUB_GITHUB_FIXTURE"); path != "" {
		if err := load(gh, path); err != nil {
			return err
		}
	}
	addr := os.Getenv("STUB_GITHUB_ADDR")
	if addr == "" {
		addr = ":8443"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /_stub/state", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(state{
			Mints: gh.Mints(), Revokes: gh.Revokes(), ProbeQueries: gh.ProbeQueries(), RateReads: gh.RateReads(),
		})
		if err != nil {
			slog.Error("write state", "error", err)
		}
	})
	mux.HandleFunc("GET /_stub/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.Handle("/", gh.Handler())

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	slog.Info("stub-github listening", "addr", addr)
	return srv.ListenAndServeTLS(cert, key)
}

func load(gh *fakegithub.Server, path string) error {
	b, err := os.ReadFile(path) //nolint:gosec // G304: the fixture path is the operator's
	if err != nil {
		return fmt.Errorf("read fixture: %w", err)
	}
	var f fixture
	if err := json.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("decode fixture %s: %w", path, err)
	}
	if len(f.Installations) > 0 {
		gh.SetInstallations(f.Installations...)
	}
	gh.SetRepos(f.Repos...)
	return nil
}
