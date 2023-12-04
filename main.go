package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/config/configopaque"
	"go.opentelemetry.io/collector/config/configtelemetry"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/exporter/otlphttpexporter"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	mnoop "go.opentelemetry.io/otel/metric/noop"
	tnoop "go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/zap"
)

func main() {

	otlpEndpoint, ok := os.LookupEnv("OTLP_ENDPOINT")
	if !ok {
		log.Fatal("Set up env var OTLP_ENDPOINT")
	}
	accessToken, ok := os.LookupEnv("ACCESS_TOKEN")
	if !ok {
		log.Fatal("Set up env var ACCESS_TOKEN")
	}
	prefix, ok := os.LookupEnv("PREFIX")
	if !ok {
		log.Fatal("Set up env var PREFIX")
	}

	if err := run(otlpEndpoint, accessToken, prefix); err != nil {
		log.Fatalf("error running otlp: %v", err)
	}
}

func run(otlpEndpoint string, accessToken string, prefix string) error {
	logger, err := zap.NewProduction()
	if err != nil {
		return err
	}
	f := otlphttpexporter.NewFactory()
	c := f.CreateDefaultConfig()
	cfg := c.(*otlphttpexporter.Config)
	cfg.MetricsEndpoint = otlpEndpoint
	cfg.Headers = map[string]configopaque.String{
		"X-SF-Token": configopaque.String(accessToken),
	}
	e, err := f.CreateMetricsExporter(context.Background(), exporter.CreateSettings{
		ID: component.NewID("otlp"),
		TelemetrySettings: component.TelemetrySettings{
			Logger:                logger,
			TracerProvider:        tnoop.NewTracerProvider(),
			MeterProvider:         mnoop.NewMeterProvider(),
			MetricsLevel:          configtelemetry.LevelNone,
			Resource:              pcommon.NewResource(),
			ReportComponentStatus: nil,
		},
		BuildInfo: component.BuildInfo{},
	}, cfg)
	if err != nil {
		return err
	}

	keepRunning := &atomic.Bool{}
	keepRunning.Store(true)
	shutdownChan := make(chan os.Signal)
	signal.Notify(shutdownChan, os.Interrupt, syscall.SIGTERM, syscall.SIGKILL)
	go func() {
		<-shutdownChan
		keepRunning.Store(false)
	}()

	err = e.Start(context.Background(), componenttest.NewNopHost())
	if err != nil {
		return err
	}
	defer func() {
		err := e.Shutdown(context.Background())
		if err != nil {
			logger.Error("error shutting down", zap.Error(err))
		}
	}()

	wg := &sync.WaitGroup{}
	wg.Add(1)
	runExports(wg, prefix, e, keepRunning, logger)
	wg.Done()

	wg.Wait()

	return nil
}

type testRun struct {
	name          string
	createMetrics func(prefix string) pmetric.Metrics
}

var testCases = []testRun{
	{
		name: "empty histogram",
		createMetrics: func(prefix string) pmetric.Metrics {
			metrics := pmetric.NewMetrics()
			metric := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
			metric.SetEmptyHistogram()
			metric.SetName(fmt.Sprintf("%s.histogram.empty", prefix))
			return metrics
		},
	},
	{
		name: "simple gauge",
		createMetrics: func(prefix string) pmetric.Metrics {
			metrics := pmetric.NewMetrics()
			metric := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
			g := metric.SetEmptyGauge()
			g.DataPoints().AppendEmpty().SetIntValue(1)
			metric.SetName(fmt.Sprintf("%s.gauge.check", prefix))
			return metrics
		},
	},
	{
		name: "working histogram",
		createMetrics: func(prefix string) pmetric.Metrics {
			metrics := pmetric.NewMetrics()
			metric := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
			h := metric.SetEmptyHistogram()
			dp := h.DataPoints().AppendEmpty()
			dp.SetMax(1)
			dp.SetMin(0)
			dp.SetSum(1)
			dp.SetCount(1)
			dp.SetTimestamp(pcommon.NewTimestampFromTime(time.Now()))
			dp.SetStartTimestamp(pcommon.NewTimestampFromTime(time.Now()))
			dp.BucketCounts().Append(1, 2, 3)
			dp.ExplicitBounds().Append(0.1, 0.2, 0.5)
			metric.SetName(fmt.Sprintf("%s.histogram.complete", prefix))
			return metrics
		},
	},
	{
		name: "histogram with empty data point",
		createMetrics: func(prefix string) pmetric.Metrics {
			metrics := pmetric.NewMetrics()
			metric := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
			h := metric.SetEmptyHistogram()
			h.DataPoints().AppendEmpty()
			metric.SetName(fmt.Sprintf("%s.histogram.dp.empty", prefix))
			return metrics
		},
	},
	{
		name: "histogram with no buckets",
		createMetrics: func(prefix string) pmetric.Metrics {
			metrics := pmetric.NewMetrics()
			metric := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
			h := metric.SetEmptyHistogram()
			metric.SetName(fmt.Sprintf("%s.histogram.nobuckets", prefix))
			dp := h.DataPoints().AppendEmpty()
			dp.SetTimestamp(pcommon.NewTimestampFromTime(time.Now()))
			dp.SetSum(1)
			dp.SetCount(2)
			dp.SetMin(2.0)
			dp.SetMax(2.0)
			dp.ExplicitBounds().Append(0.1, 0.2, 0.5)
			return metrics
		},
	},
	{
		name: "histogram with min > max",
		createMetrics: func(prefix string) pmetric.Metrics {
			metrics := pmetric.NewMetrics()
			metric := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
			h := metric.SetEmptyHistogram()
			metric.SetName(fmt.Sprintf("%s.histogram.minovermax", prefix))
			dp := h.DataPoints().AppendEmpty()
			dp.SetTimestamp(pcommon.NewTimestampFromTime(time.Now()))
			dp.SetSum(1)
			dp.SetCount(2)
			dp.SetMin(15.0)
			dp.SetMax(2.0)
			dp.BucketCounts().Append(1, 2)
			dp.ExplicitBounds().Append(0.1, 0.2, 0.5)
			return metrics
		},
	},
	{
		name: "no min histogram",
		createMetrics: func(prefix string) pmetric.Metrics {
			metrics := pmetric.NewMetrics()
			metric := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
			h := metric.SetEmptyHistogram()
			metric.SetName(fmt.Sprintf("%s.histogram.nomin", prefix))
			dp := h.DataPoints().AppendEmpty()
			dp.SetTimestamp(pcommon.NewTimestampFromTime(time.Now()))
			dp.SetSum(1)
			dp.SetCount(2)
			dp.SetMax(2.0)
			dp.BucketCounts().Append(1, 2, 3)
			dp.ExplicitBounds().Append(0.1, 0.2, 0.5)
			return metrics
		},
	},
	{
		name: "no max histogram",
		createMetrics: func(prefix string) pmetric.Metrics {
			metrics := pmetric.NewMetrics()
			metric := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
			h := metric.SetEmptyHistogram()
			metric.SetName(fmt.Sprintf("%s.histogram.nomax", prefix))
			dp := h.DataPoints().AppendEmpty()
			dp.SetTimestamp(pcommon.NewTimestampFromTime(time.Now()))
			dp.SetSum(1)
			dp.SetCount(2)
			dp.SetMin(2.0)
			dp.BucketCounts().Append(1, 2, 3)
			dp.ExplicitBounds().Append(0.1, 0.2, 0.5)
			return metrics
		},
	},
	{
		name: "33 buckets",
		createMetrics: func(prefix string) pmetric.Metrics {
			metrics := pmetric.NewMetrics()
			metric := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
			h := metric.SetEmptyHistogram()
			metric.SetName(fmt.Sprintf("%s.histogram.33buckets", prefix))
			dp := h.DataPoints().AppendEmpty()
			dp.SetTimestamp(pcommon.NewTimestampFromTime(time.Now()))
			dp.SetSum(1)
			dp.SetCount(2)
			dp.SetMin(2.0)
			dp.SetMax(3.0)
			var buckets []uint64
			for i := 0; i < 33; i++ {
				buckets = append(buckets, uint64(i))
			}
			dp.BucketCounts().Append(buckets...)
			dp.ExplicitBounds().Append(0.1, 0.2, 0.5)
			return metrics
		},
	},
	{
		name: "64 buckets",
		createMetrics: func(prefix string) pmetric.Metrics {
			metrics := pmetric.NewMetrics()
			metric := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
			h := metric.SetEmptyHistogram()
			metric.SetName(fmt.Sprintf("%s.histogram.33buckets", prefix))
			dp := h.DataPoints().AppendEmpty()
			dp.SetTimestamp(pcommon.NewTimestampFromTime(time.Now()))
			dp.SetSum(1)
			dp.SetCount(2)
			dp.SetMin(2.0)
			dp.SetMax(3.0)
			var buckets []uint64
			for i := 0; i < 64; i++ {
				buckets = append(buckets, uint64(i))
			}
			dp.BucketCounts().Append(buckets...)
			dp.ExplicitBounds().Append(0.1, 0.2, 0.5)
			return metrics
		},
	},
	{
		name: "start timestamp older than timestamp",
		createMetrics: func(prefix string) pmetric.Metrics {
			metrics := pmetric.NewMetrics()
			metric := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
			h := metric.SetEmptyHistogram()
			metric.SetName(fmt.Sprintf("%s.histogram.startolderthanend", prefix))
			dp := h.DataPoints().AppendEmpty()
			dp.SetTimestamp(pcommon.NewTimestampFromTime(time.Now().Add(-10 * time.Minute)))
			dp.SetStartTimestamp(pcommon.NewTimestampFromTime(time.Now()))
			dp.SetSum(1)
			dp.SetCount(2)
			dp.SetMin(1.0)
			dp.SetMax(2.0)
			dp.BucketCounts().Append(1, 2, 3)
			dp.ExplicitBounds().Append(0.1, 0.2, 0.5)
			return metrics
		},
	},
	{
		name: "histogram with attribute name longer than 255 characters",
		createMetrics: func(prefix string) pmetric.Metrics {
			metrics := pmetric.NewMetrics()
			metric := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
			h := metric.SetEmptyHistogram()
			metric.SetName(fmt.Sprintf("%s.histogram.longattributename", prefix))
			dp := h.DataPoints().AppendEmpty()
			dp.Attributes().PutStr("Lorem_ipsum_dolor_sit_amet,_consectetur_adipiscing_elit,_sed_do_eiusmod_tempor_incididunt_ut_labore_et_dolore_magna_aliqua._Ut_enim_ad_minim_veniam,_quis_nostrud_exercitation_ullamco_laboris_nisi_ut_aliquip_ex_ea_commodo_consequat._Duis_aute_irure_dolor_in_reprehenderit_in_voluptate_velit_esse_cillum_dolore_eu_fugiat_nulla_pariatur._Excepteur_sint_occaecat_cupidatat_non_proident,_sunt_in_culpa_qui_officia_deserunt_mollit_anim_id_est laborum.", "foo")
			dp.SetTimestamp(pcommon.NewTimestampFromTime(time.Now().Add(-10 * time.Minute)))
			dp.SetSum(1)
			dp.SetCount(2)
			dp.SetMin(1.0)
			dp.SetMax(2.0)
			dp.BucketCounts().Append(1, 2, 3)
			dp.ExplicitBounds().Append(0.1, 0.2, 0.5)

			return metrics
		},
	},
}

type testRunModifier struct {
	name           string
	createTestRuns func(prefix string, run testRun) ([]testRun, bool)
}

var modifiers = []testRunModifier{
	{
		name: "add histogram type: none, cumulative, delta",
		createTestRuns: func(prefix string, run testRun) ([]testRun, bool) {
			if run.createMetrics(prefix).ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Type() != pmetric.MetricTypeHistogram {
				return nil, false
			}
			return []testRun{
				{
					name: run.name + " with unspecified type",
					createMetrics: func(prefix string) pmetric.Metrics {
						metrics := run.createMetrics(prefix)
						histogram := metrics.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Histogram()
						histogram.SetAggregationTemporality(pmetric.AggregationTemporalityUnspecified)
						return metrics
					},
				},
				{
					name: run.name + " with cumulative type",
					createMetrics: func(prefix string) pmetric.Metrics {
						metrics := run.createMetrics(prefix)
						metric := metrics.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0)
						metric.SetName(metric.Name() + ".cumulative")
						histogram := metric.Histogram()
						histogram.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)

						return metrics
					},
				},
				{
					name: run.name + " with delta type",
					createMetrics: func(prefix string) pmetric.Metrics {
						metrics := run.createMetrics(prefix)
						metric := metrics.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0)
						metric.SetName(metric.Name() + ".delta")
						histogram := metric.Histogram()
						histogram.SetAggregationTemporality(pmetric.AggregationTemporalityDelta)
						return metrics
					},
				},
			}, true
		},
	},
	{
		name: "with and without exemplars",
		createTestRuns: func(prefix string, run testRun) ([]testRun, bool) {
			if run.createMetrics(prefix).ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Type() != pmetric.MetricTypeHistogram {
				return nil, false
			}
			return []testRun{
				{
					name: run.name + " with exemplar",
					createMetrics: func(prefix string) pmetric.Metrics {
						metrics := run.createMetrics(prefix)
						metric := metrics.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0)
						metric.SetName(metric.Name() + ".exemplar")
						histogram := metric.Histogram()
						dp := histogram.DataPoints().At(0)
						e := dp.Exemplars().AppendEmpty()
						e.SetTraceID([16]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15})
						e.SetSpanID([8]byte{0, 1, 2, 3, 4, 5, 6, 7})
						e.SetDoubleValue(42.0)
						e.SetIntValue(42)
						e.SetTimestamp(pcommon.NewTimestampFromTime(time.Now()))
						return metrics
					},
				},
				{
					name:          run.name + " without exemplar",
					createMetrics: run.createMetrics,
				},
			}, true
		},
	},
}

func runExports(wg *sync.WaitGroup, prefix string, exporter exporter.Metrics, keepRunning *atomic.Bool, logger *zap.Logger) {
	var start sync.WaitGroup

	var allTestCases []testRun
	for _, testCase := range testCases {
		for _, modifier := range modifiers {
			testRuns, ok := modifier.createTestRuns(prefix, testCase)
			if ok {
				allTestCases = append(allTestCases, testRuns...)
			} else {
				allTestCases = append(allTestCases, testCase)
			}
		}
	}

	start.Add(len(allTestCases))
	counter := 0

	for _, testCase := range allTestCases {
		go func() {
			start.Done()
			wg.Add(1)
			for keepRunning.Load() {
				time.Sleep(1 * time.Second)
				err := exporter.ConsumeMetrics(context.Background(), testCase.createMetrics(prefix))
				if err != nil {
					logger.Error("Error sending metrics", zap.String("testCase", testCase.name), zap.Error(err))
				} else {
					counter++
					if counter%100 == 0 {
						logger.Info("Histograms sent", zap.Int("count", counter))
					}
				}
			}
			wg.Done()
		}()
	}
	start.Wait()
}
