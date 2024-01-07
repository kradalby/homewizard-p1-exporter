# homewizard-p1-exporter

A [Prometheus](prometheus.io) exporter that translates between Prometheus and [Homewizard P1](https://www.homewizard.com/p1-meter/) power tracker (plugs into your electrical meter).

It is implemented in a very similar way to [blackbox_exporter](https://github.com/prometheus/blackbox_exporter) which allows you to run it
as a "proxy" querying the power meter on the go and returns specific metrics for that power meter using targets and relabeling.

## Configuration

homewizard-p1-exporter does not need any configuration itself, and the seperation of power meter are fully hosted in the Prometheus
scrape config.

Just run the binary somewhere where it can reach the power meter over IP and from Prometheus.
By default, port `9090` will be used, but this can be changed `HOMEWIZARD_EXPORTER_LISTEN_ADDR` on
the format `:9111`.

In Prometheus, add a new scrape target:

```yaml
scrape_configs:
  - job_name: homewizard
    metrics_path: /probe
    static_configs:
      - targets:
          # add the address of your meter(s))here
          - p1-power.local
    relabel_configs:
      - source_labels: [__address__]
        target_label: __param_target
      - source_labels: [__param_target]
        target_label: instance
      - target_label: __address__
        replacement: 127.0.0.1:9090 # address of exporter
```

I recommend to have DNS names assigned to your meter so the instance name will be human readable.
