package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"tailscale.com/envknob"
)

var overrideListenAddr = envknob.String("HOMEWIZARD_EXPORTER_LISTEN_ADDR")

// p1Metrics maps each exported gauge to the reading it reports.
var p1Metrics = []struct {
	name, help string
	value      func(P1) float64
}{
	{"homewizard_wifi_strength_decibels", "strength of WIFI signal for homewizard in decibels", func(p P1) float64 { return p.WifiStrength }},
	{"homewizard_active_power_watts", "current (total) usage of power meassured in watts (W)", func(p P1) float64 { return p.ActivePowerW }},
	{"homewizard_active_power_l1_watts", "current (L1) usage of power meassured in watts (w)", func(p P1) float64 { return p.ActivePowerL1W }},
	{"homewizard_active_power_l2_watts", "current (L2) usage of power meassured in watts (w)", func(p P1) float64 { return p.ActivePowerL2W }},
	{"homewizard_active_power_l3_watts", "current (L3) usage of power meassured in watts (w)", func(p P1) float64 { return p.ActivePowerL3W }},
	{"homewizard_any_power_fail_count", "number of power failures meassured by P1", func(p P1) float64 { return p.AnyPowerFailCount }},
	{"homewizard_long_power_fail_count", "number of long power failures meassured by P1", func(p P1) float64 { return p.LongPowerFailCount }},
	{"homewizard_gas_m3_total", "total usage of gas reported by the gas meter in m3", func(p P1) float64 { return p.TotalGasM3 }},
}

func main() {
	http.HandleFunc("/health", healthHandler)
	http.HandleFunc("/probe", homewizardHandler)

	listenAddr := ":9090"
	if overrideListenAddr != "" {
		listenAddr = overrideListenAddr
	}

	log.Printf("starting homewizard exporter on %s", listenAddr)
	err := http.ListenAndServe(listenAddr, nil)
	if errors.Is(err, http.ErrServerClosed) {
		log.Printf("server closed")
	} else if err != nil {
		log.Fatalf("error starting server: %s", err)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, "ok")
}

func homewizardHandler(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("target")
	if target == "" {
		http.Error(w, "Target parameter is missing", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	start := time.Now()
	p1, err := probeHomewizard(ctx, target)
	duration := time.Since(start).Seconds()

	// A registry per request: probes of different targets run concurrently
	// and must neither share readings nor inherit them from a previous probe.
	registry := prometheus.NewRegistry()
	addGauge(registry, "probe_duration_seconds", "Returns how long the probe took to complete in seconds", duration)

	if err != nil {
		log.Printf("%s: probe failed, duration: %fs: %s", target, duration, err)
		addGauge(registry, "probe_success", "Displays whether or not the probe was a success", 0)
	} else {
		log.Printf("%s: probe succeeded, duration: %fs", target, duration)
		addGauge(registry, "probe_success", "Displays whether or not the probe was a success", 1)
		for _, m := range p1Metrics {
			addGauge(registry, m.name, m.help, m.value(p1))
		}
	}

	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(w, r)
}

func addGauge(registry *prometheus.Registry, name, help string, value float64) {
	g := prometheus.NewGauge(prometheus.GaugeOpts{Name: name, Help: help})
	g.Set(value)
	registry.MustRegister(g)
}

func probeHomewizard(ctx context.Context, target string) (P1, error) {
	url := fmt.Sprintf("http://%s/api/v1/data", target)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return P1{}, fmt.Errorf("building request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return P1{}, fmt.Errorf("querying target: %w", err)
	}
	// Close before inspecting the status: the non-200 path used to return
	// without ever touching the body, leaking a connection, an fd and a
	// readLoop goroutine on every scrape. Go 1.27's Close drains the unread
	// remainder itself, so a bare Close now suffices to keep the connection
	// reusable - no io.Copy(io.Discard, ...) needed.
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return P1{}, fmt.Errorf("target returned %s", resp.Status)
	}

	var p1 P1
	if err := json.NewDecoder(resp.Body).Decode(&p1); err != nil {
		return P1{}, fmt.Errorf("decoding response: %w", err)
	}

	return p1, nil
}

type P1 struct {
	WifiSSID              string  `json:"wifi_ssid"`
	WifiStrength          float64 `json:"wifi_strength"`
	SmrVersion            float64 `json:"smr_version"`
	MeterModel            string  `json:"meter_model"`
	UniqueID              string  `json:"unique_id"`
	ActiveTariff          float64 `json:"active_tariff"`
	TotalPowerImportKwh   float64 `json:"total_power_import_kwh"`
	TotalPowerImportT1Kwh float64 `json:"total_power_import_t1_kwh"`
	TotalPowerImportT2Kwh float64 `json:"total_power_import_t2_kwh"`
	TotalPowerExportKwh   float64 `json:"total_power_export_kwh"`
	TotalPowerExportT1Kwh float64 `json:"total_power_export_t1_kwh"`
	TotalPowerExportT2Kwh float64 `json:"total_power_export_t2_kwh"`
	ActivePowerW          float64 `json:"active_power_w"`
	ActivePowerL1W        float64 `json:"active_power_l1_w"`
	ActivePowerL2W        float64 `json:"active_power_l2_w"`
	ActivePowerL3W        float64 `json:"active_power_l3_w"`
	ActiveVoltageL1V      float64 `json:"active_voltage_l1_v"`
	ActiveVoltageL2V      float64 `json:"active_voltage_l2_v"`
	ActiveVoltageL3V      float64 `json:"active_voltage_l3_v"`
	ActiveCurrentL1A      float64 `json:"active_current_l1_a"`
	ActiveCurrentL2A      float64 `json:"active_current_l2_a"`
	ActiveCurrentL3A      float64 `json:"active_current_l3_a"`
	VoltageSagL1Count     float64 `json:"voltage_sag_l1_count"`
	VoltageSagL2Count     float64 `json:"voltage_sag_l2_count"`
	VoltageSagL3Count     float64 `json:"voltage_sag_l3_count"`
	VoltageSwellL1Count   float64 `json:"voltage_swell_l1_count"`
	VoltageSwellL2Count   float64 `json:"voltage_swell_l2_count"`
	VoltageSwellL3Count   float64 `json:"voltage_swell_l3_count"`
	AnyPowerFailCount     float64 `json:"any_power_fail_count"`
	LongPowerFailCount    float64 `json:"long_power_fail_count"`
	TotalGasM3            float64 `json:"total_gas_m3"`
	GasTimestamp          int64   `json:"gas_timestamp"`
	GasUniqueID           string  `json:"gas_unique_id"`
	External              []struct {
		UniqueID  string  `json:"unique_id"`
		Type      string  `json:"type"`
		Timestamp int64   `json:"timestamp"`
		Value     float64 `json:"value"`
		Unit      string  `json:"unit"`
	} `json:"external"`
}
