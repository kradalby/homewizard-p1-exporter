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

	"github.com/prometheus/client_golang/prometheus/testutil"
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

func TestProbeHomewizardSetsGauges(t *testing.T) {
	srv, _ := newTargetServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, p1Body)
	})

	if !probeHomewizard(t.Context(), hostOf(t, srv.URL)) {
		t.Fatal("probeHomewizard returned false for a healthy target")
	}

	for _, tc := range []struct {
		name string
		got  float64
		want float64
	}{
		{"active_power_w", testutil.ToFloat64(activePowerWattGauge), 1234.5},
		{"active_power_l1_w", testutil.ToFloat64(activePowerL1WattGauge), 400},
		{"wifi_strength", testutil.ToFloat64(wifiStrengthGauge), 84},
		{"total_gas_m3", testutil.ToFloat64(totalGasGauge), 987.654},
		{"long_power_fail_count", testutil.ToFloat64(longFailedGauge), 1},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}

func TestProbeHomewizardMalformedJSON(t *testing.T) {
	srv, _ := newTargetServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "{not json")
	})

	if probeHomewizard(t.Context(), hostOf(t, srv.URL)) {
		t.Error("probeHomewizard returned true for a malformed body")
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
		if probeHomewizard(t.Context(), target) {
			t.Fatal("probeHomewizard returned true for a 500 response")
		}
	}

	// Pooling is not required to be perfect, but it must not scale with the
	// number of probes. Pre-fix this was exactly `probes`.
	if got := connCount(); got > probes/2 {
		t.Errorf("target saw %d connections for %d probes; response bodies are leaking", got, probes)
	}
}

// probe_success is the metric the whole exporter exists to serve. It used to be
// set to 1 on success and simply left alone on failure, so it latched at 1
// forever after the first good scrape and could never signal an outage.
func TestHandlerResetsProbeSuccessOnFailure(t *testing.T) {
	srv, _ := newTargetServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, p1Body)
	})

	scrape := func(target string) string {
		t.Helper()

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/probe?target="+url.QueryEscape(target), nil)
		homewizardHandler(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("scrape of %q: status %d, want 200", target, rec.Code)
		}

		return rec.Body.String()
	}

	if body := scrape(hostOf(t, srv.URL)); !strings.Contains(body, "probe_success 1") {
		t.Fatalf("healthy target did not report probe_success 1:\n%s", body)
	}

	// 127.0.0.1:1 is reserved and never listening, so this fails immediately
	// rather than waiting out the client timeout.
	body := scrape("127.0.0.1:1")
	if got := testutil.ToFloat64(probeSuccessGauge); got != 0 {
		t.Errorf("probe_success = %v after a failed probe, want 0", got)
	}

	if !strings.Contains(body, "probe_success 0") {
		t.Errorf("failed probe did not export probe_success 0:\n%s", body)
	}
}

func TestHandlerRejectsMissingTarget(t *testing.T) {
	rec := httptest.NewRecorder()
	homewizardHandler(rec, httptest.NewRequest(http.MethodGet, "/probe", nil))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d for a missing target, want %d", rec.Code, http.StatusBadRequest)
	}
}
