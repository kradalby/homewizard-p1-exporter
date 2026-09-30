package main

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

const p1Body = `{
  "wifi_ssid": "test",
  "wifi_strength": 84,
  "active_power_w": 1234.5,
  "active_power_l1_w": 400,
  "active_power_l2_w": 411,
  "active_power_l3_w": 423.5,
  "any_power_fail_count": 3,
  "long_power_fail_count": 1,
  "total_gas_m3": 987.654
}`

// hostOf strips the scheme so the result can be fed to probeHomewizard, which
// builds "http://<target>/api/v1/data" itself.
func hostOf(t *testing.T, raw string) string {
	t.Helper()

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parsing test server URL %q: %s", raw, err)
	}

	return u.Host
}

// newTargetServer starts a fake HomeWizard that always responds with handler,
// and reports how many distinct connections it accepted. A connection count
// that grows with the number of probes means response bodies are being leaked
// instead of returned to the pool.
func newTargetServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, func() int) {
	t.Helper()

	var mu sync.Mutex
	conns := 0

	srv := httptest.NewUnstartedServer(handler)
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			mu.Lock()
			conns++
			mu.Unlock()
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)

	return srv, func() int {
		mu.Lock()
		defer mu.Unlock()

		return conns
	}
}

// scrape runs one /probe request and returns the exported samples by name.
// Errorf, not Fatalf: it is also called from non-test goroutines.
func scrape(t *testing.T, target string) map[string]string {
	t.Helper()

	rec := httptest.NewRecorder()
	homewizardHandler(rec, httptest.NewRequest(http.MethodGet, "/probe?target="+url.QueryEscape(target), nil))

	if rec.Code != http.StatusOK {
		t.Errorf("scrape of %q: status %d, want 200", target, rec.Code)
	}

	samples := map[string]string{}
	for line := range strings.Lines(rec.Body.String()) {
		if strings.HasPrefix(line, "#") {
			continue
		}
		if name, value, ok := strings.Cut(strings.TrimSpace(line), " "); ok {
			samples[name] = value
		}
	}

	return samples
}

func TestHandlerExportsReadings(t *testing.T) {
	srv, _ := newTargetServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, p1Body)
	})

	got := scrape(t, hostOf(t, srv.URL))

	for metric, want := range map[string]string{
		"probe_success":                     "1",
		"homewizard_wifi_strength_decibels": "84",
		"homewizard_active_power_watts":     "1234.5",
		"homewizard_active_power_l1_watts":  "400",
		"homewizard_active_power_l2_watts":  "411",
		"homewizard_active_power_l3_watts":  "423.5",
		"homewizard_any_power_fail_count":   "3",
		"homewizard_long_power_fail_count":  "1",
		"homewizard_gas_m3_total":           "987.654",
	} {
		if got[metric] != want {
			t.Errorf("%s = %q, want %s", metric, got[metric], want)
		}
	}
	if _, ok := got["probe_duration_seconds"]; !ok {
		t.Error("probe_duration_seconds not exported")
	}
}

func TestProbeHomewizardMalformedJSON(t *testing.T) {
	srv, _ := newTargetServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "{not json")
	})

	if _, err := probeHomewizard(t.Context(), hostOf(t, srv.URL)); err == nil {
		t.Error("probeHomewizard succeeded on a malformed body")
	}
}

// A non-200 response used to return without closing the body, so every scrape
// against a failing device burned a connection, an fd and a readLoop goroutine
// that were never reclaimed. Closing the body lets the connection be pooled and
// reused, so a run of probes should need only a handful of connections.
func TestProbeHomewizardNon200DoesNotLeakConnections(t *testing.T) {
	const probes = 20

	srv, connCount := newTargetServer(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})

	target := hostOf(t, srv.URL)
	for range probes {
		if _, err := probeHomewizard(t.Context(), target); err == nil {
			t.Fatal("probeHomewizard succeeded on a 500 response")
		}
	}

	// Pooling is not required to be perfect, but it must not scale with the
	// number of probes. Pre-fix this was exactly `probes`.
	if got := connCount(); got > probes/2 {
		t.Errorf("target saw %d connections for %d probes; response bodies are leaking", got, probes)
	}
}

// A failed probe must report probe_success 0 and no readings, least of all
// those of a previously probed target.
func TestHandlerFailureExportsNoReadings(t *testing.T) {
	srv, _ := newTargetServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, p1Body)
	})

	if got := scrape(t, hostOf(t, srv.URL)); got["probe_success"] != "1" {
		t.Fatalf("healthy target: probe_success = %q, want 1", got["probe_success"])
	}

	// 127.0.0.1:1 is reserved and never listening, so this fails immediately
	// rather than waiting out the client timeout.
	got := scrape(t, "127.0.0.1:1")
	if got["probe_success"] != "0" {
		t.Errorf("probe_success = %q after a failed probe, want 0", got["probe_success"])
	}
	for metric, value := range got {
		if strings.HasPrefix(metric, "homewizard_") {
			t.Errorf("failed probe exported %s %s", metric, value)
		}
	}
}

func TestConcurrentProbesKeepTargetsApart(t *testing.T) {
	targets := map[string]string{}
	for _, watts := range []string{"111", "222"} {
		srv, _ := newTargetServer(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprintf(w, `{"active_power_w": %s}`, watts)
		})
		targets[hostOf(t, srv.URL)] = watts
	}

	var wg sync.WaitGroup
	for range 100 {
		for target, want := range targets {
			wg.Go(func() {
				if got := scrape(t, target)["homewizard_active_power_watts"]; got != want {
					t.Errorf("probe of %s: homewizard_active_power_watts = %q, want %s", target, got, want)
				}
			})
		}
	}
	wg.Wait()
}

func TestHandlerRejectsMissingTarget(t *testing.T) {
	rec := httptest.NewRecorder()
	homewizardHandler(rec, httptest.NewRequest(http.MethodGet, "/probe", nil))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d for a missing target, want %d", rec.Code, http.StatusBadRequest)
	}
}
