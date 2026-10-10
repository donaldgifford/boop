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

// Command stub-renovate stands in for the Renovate image in boopd's e2e
// tests (IMPL-0001 task 4.6). It replays a fixture log to stdout line by
// line, as Renovate would with LOG_FORMAT=json, and is configured by
// environment variables:
//
//	STUB_FIXTURE       embedded fixture name (live, dryrun) or a file path; default live
//	STUB_DELAY         pause between lines, a Go duration; default 0
//	STUB_EXIT_CODE     exit code after the last line; default 0
//	STUB_REPORT_BYTES  pad the "Printing report" line to at least this many bytes
//	STUB_HANG_AFTER    stop after this many lines and hang until killed; -1 (default) never
//	STUB_MARKER_DIRS   comma-separated directories: report which markers already
//	                   exist there, then write one, for the isolation scenario
//
// The fixture's repository is replaced by RENOVATE_REPOSITORIES when set.
// It is never published.
package main

import (
	"bufio"
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

//go:embed fixtures/*.log
var fixtures embed.FS

const fixtureSlug = "boop-bot/scratch"

type config struct {
	fixture     string
	delay       time.Duration
	exitCode    int
	reportBytes int
	hangAfter   int
	markerDirs  []string
	slug        string
}

func main() {
	cfg, err := configFromEnv(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "stub-renovate:", err)
		os.Exit(2)
	}
	if err := run(cfg, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "stub-renovate:", err)
		os.Exit(2)
	}
	os.Exit(cfg.exitCode)
}

func configFromEnv(getenv func(string) string) (*config, error) {
	cfg := &config{fixture: "live", hangAfter: -1, slug: getenv("RENOVATE_REPOSITORIES")}
	if v := getenv("STUB_FIXTURE"); v != "" {
		cfg.fixture = v
	}
	var err error
	if v := getenv("STUB_DELAY"); v != "" {
		if cfg.delay, err = time.ParseDuration(v); err != nil {
			return nil, fmt.Errorf("STUB_DELAY: %w", err)
		}
	}
	for name, dst := range map[string]*int{
		"STUB_EXIT_CODE":    &cfg.exitCode,
		"STUB_REPORT_BYTES": &cfg.reportBytes,
		"STUB_HANG_AFTER":   &cfg.hangAfter,
	} {
		if v := getenv(name); v != "" {
			if *dst, err = strconv.Atoi(v); err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	if v := getenv("STUB_MARKER_DIRS"); v != "" {
		cfg.markerDirs = strings.Split(v, ",")
	}
	return cfg, nil
}

func run(cfg *config, out io.Writer) error {
	src, err := readFixture(cfg.fixture)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(out)
	emit := func(line string) error {
		if _, err := w.WriteString(line + "\n"); err != nil {
			return err
		}
		return w.Flush()
	}
	if err := markers(cfg.markerDirs, emit); err != nil {
		return err
	}
	sc := bufio.NewScanner(bytes.NewReader(src))
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for n := 0; sc.Scan(); n++ {
		if cfg.hangAfter >= 0 && n >= cfg.hangAfter {
			hang()
		}
		line := sc.Text()
		if cfg.slug != "" {
			line = strings.ReplaceAll(line, fixtureSlug, cfg.slug)
		}
		if cfg.reportBytes > 0 && strings.Contains(line, `"msg":"Printing report"`) {
			if line, err = pad(line, cfg.reportBytes); err != nil {
				return err
			}
		}
		if err := emit(line); err != nil {
			return err
		}
		if cfg.delay > 0 {
			time.Sleep(cfg.delay)
		}
	}
	return sc.Err()
}

// hang blocks until the process is killed. A bare select{} would not:
// with no other goroutine the runtime reports a deadlock and exits 2.
func hang() {
	for {
		time.Sleep(time.Hour)
	}
}

func readFixture(name string) ([]byte, error) {
	if b, err := fixtures.ReadFile("fixtures/" + name + ".log"); err == nil {
		return b, nil
	}
	return os.ReadFile(name)
}

// pad grows the report line to at least size bytes with a padding field
// inside the report, keeping it valid JSON.
func pad(line string, size int) (string, error) {
	var l map[string]any
	if err := json.Unmarshal([]byte(line), &l); err != nil {
		return "", fmt.Errorf("report line: %w", err)
	}
	report, ok := l["report"].(map[string]any)
	if !ok {
		return "", errors.New("report line has no report object")
	}
	if missing := size - len(line); missing > 0 {
		report["padding"] = strings.Repeat("x", missing)
	}
	b, err := json.Marshal(l)
	return string(b), err
}

// markers logs which markers a previous run left in each directory, then
// writes this run's. The e2e isolation scenario asserts it sees none.
func markers(dirs []string, emit func(string) error) error {
	host, err := os.Hostname()
	if err != nil {
		host = strconv.Itoa(os.Getpid())
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read %s: %w", dir, err)
		}
		var found []string
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "stub-marker-") {
				found = append(found, e.Name())
			}
		}
		line, err := json.Marshal(map[string]any{"msg": "stub markers", "dir": dir, "found": found})
		if err != nil {
			return err
		}
		if err := emit(string(line)); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "stub-marker-"+host), []byte(host), 0o600); err != nil {
			return fmt.Errorf("write marker in %s: %w", dir, err)
		}
	}
	return nil
}
