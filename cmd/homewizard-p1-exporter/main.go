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

func main() {
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

func homewizardHandler(w http.ResponseWriter, r *http.Request) {
	probeSuccessGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "probe_success",
		Help: "Displays whether or not the probe was a success",
	})
	probeDurationGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "probe_duration_seconds",
		Help: "Returns how long the probe took to complete in seconds",
	})

	params := r.URL.Query()

	target := params.Get("target")
	if target == "" {
		http.Error(w, "Target parameter is missing", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	r = r.WithContext(ctx)

	start := time.Now()
	registry := prometheus.NewRegistry()
	registry.MustRegister(probeSuccessGauge)
	registry.MustRegister(probeDurationGauge)
	success := probeHomewizard(ctx, target, registry)
	duration := time.Since(start).Seconds()
	probeDurationGauge.Set(duration)
	if success {
		probeSuccessGauge.Set(1)
		log.Printf("%s: probe succeeded, duration: %fs", target, duration)
	} else {
		log.Printf("%s: probe failed, duration: %fs", target, duration)
	}

	h := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	h.ServeHTTP(w, r)
}

func probeHomewizard(
	ctx context.Context,
	target string,
	registry *prometheus.Registry,
) (success bool) {
	wifiStrengthGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "homewizard_wifi_strength_decibels",
		Help: "strength of WIFI signal for homewizard in decibels",
	})
	activePowerWattGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "homewizard_active_power_watts",
		Help: "current (total) usage of power meassured in watts (W)",
	})
	activePowerL1WattGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "homewizard_active_power_l1_watts",
		Help: "current (L1) usage of power meassured in watts (w)",
	})
	activePowerL2WattGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "homewizard_active_power_l2_watts",
		Help: "current (L2) usage of power meassured in watts (w)",
	})
	activePowerL3WattGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "homewizard_active_power_l3_watts",
		Help: "current (L3) usage of power meassured in watts (w)",
	})
	anyFailedGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "homewizard_any_power_fail_count",
		Help: "number of power failures meassured by P1",
	})
	longFailedGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "homewizard_long_power_fail_count",
		Help: "number of long power failures meassured by P1",
	})
	totalGasGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "homewizard_gas_m3_total",
		Help: "total usage of gas reported by the gas meter in m3",
	})

	registry.MustRegister(wifiStrengthGauge)
	registry.MustRegister(activePowerWattGauge)
	registry.MustRegister(activePowerL1WattGauge)
	registry.MustRegister(activePowerL2WattGauge)
	registry.MustRegister(activePowerL3WattGauge)
	registry.MustRegister(anyFailedGauge)
	registry.MustRegister(longFailedGauge)
	registry.MustRegister(totalGasGauge)

	client := http.Client{
		Timeout: 5 * time.Second,
	}

	resp, err := client.Get(fmt.Sprintf("http://%s/api/v1/data", target))
	if err != nil || resp.StatusCode != http.StatusOK {
		log.Printf("failed to query homewizard target (%s): %s, resp: %v", target, err, resp)
		return false
	}

	var p1 P1
	err = json.NewDecoder(resp.Body).Decode(&p1)
	if err != nil {
		log.Printf("failed to unmarshall data from homewizard target (%s): %s", target, err)
		return false
	}

	wifiStrengthGauge.Set(p1.WifiStrength)
	activePowerWattGauge.Set(p1.ActivePowerW)
	activePowerL1WattGauge.Set(p1.ActivePowerL1W)
	activePowerL2WattGauge.Set(p1.ActivePowerL2W)
	activePowerL3WattGauge.Set(p1.ActivePowerL3W)
	anyFailedGauge.Set(p1.AnyPowerFailCount)
	longFailedGauge.Set(p1.LongPowerFailCount)
	totalGasGauge.Set(p1.TotalGasM3)

	return true
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
