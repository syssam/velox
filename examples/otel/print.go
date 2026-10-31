package main

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// printSpans prints the span tree, indented two spaces a level, children in
// the order they started. A transaction's line has its attributes; a
// statement's has its fingerprint and call site, and the next line the SQL
// sent. It prints no span ID, time or duration, which change between runs.
func printSpans(w io.Writer, spans tracetest.SpanStubs) {
	ids := map[trace.SpanID]bool{}
	for _, s := range spans {
		ids[s.SpanContext.SpanID()] = true
	}
	children := map[trace.SpanID][]tracetest.SpanStub{}
	var roots []tracetest.SpanStub
	for _, s := range spans {
		if p := s.Parent.SpanID(); ids[p] {
			children[p] = append(children[p], s)
		} else {
			roots = append(roots, s)
		}
	}
	byStart := func(a, b tracetest.SpanStub) int { return a.StartTime.Compare(b.StartTime) }
	var walk func(s tracetest.SpanStub, indent string)
	walk = func(s tracetest.SpanStub, indent string) {
		attrs := attribute.NewSet(s.Attributes...)
		get := func(key string) string {
			v, _ := attrs.Value(attribute.Key(key))
			return v.String()
		}
		switch {
		case s.Name == "velox.tx":
			_, _ = fmt.Fprintf(w, "%s%s isolation=%s read_only=%s attempts=%s outcome=%s\n", indent, s.Name,
				get("velox.tx.isolation"), get("velox.tx.read_only"), get("velox.tx.attempts"), get("velox.tx.outcome"))
		case attrs.HasValue("velox.fingerprint"):
			_, _ = fmt.Fprintf(w, "%s%s fingerprint=%s caller=%s:%s\n", indent, s.Name,
				get("velox.fingerprint"), get("code.file.path"), get("code.line.number"))
			_, _ = fmt.Fprintf(w, "%s  %s\n", indent, get("db.query.text"))
		default:
			_, _ = fmt.Fprintf(w, "%s%s\n", indent, s.Name)
		}
		kids := children[s.SpanContext.SpanID()]
		slices.SortStableFunc(kids, byStart)
		for _, k := range kids {
			walk(k, indent+"  ")
		}
	}
	slices.SortStableFunc(roots, byStart)
	for _, r := range roots {
		walk(r, "")
	}
}

// printMetrics prints each instrument, in name order, with its unit, then a
// line per data point: its attributes in key order and its count, and for
// the returned-rows histogram its sum. It prints no duration.
func printMetrics(w io.Writer, rm metricdata.ResourceMetrics) {
	var ms []metricdata.Metrics
	for _, sm := range rm.ScopeMetrics {
		ms = append(ms, sm.Metrics...)
	}
	slices.SortFunc(ms, func(a, b metricdata.Metrics) int { return strings.Compare(a.Name, b.Name) })
	for _, m := range ms {
		_, _ = fmt.Fprintf(w, "%s (%s)\n", m.Name, m.Unit)
		switch d := m.Data.(type) {
		case metricdata.Histogram[float64]: // durations: the sum is a time
			printPoints(w, d.DataPoints, false)
		case metricdata.Histogram[int64]:
			printPoints(w, d.DataPoints, true)
		}
	}
}

// printPoints prints points sorted by their attribute sets, with their sums
// when sum is set.
func printPoints[N int64 | float64](w io.Writer, points []metricdata.HistogramDataPoint[N], sum bool) {
	enc := attribute.DefaultEncoder()
	slices.SortFunc(points, func(a, b metricdata.HistogramDataPoint[N]) int {
		return strings.Compare(a.Attributes.Encoded(enc), b.Attributes.Encoded(enc))
	})
	for _, p := range points {
		var b strings.Builder
		b.WriteString(" ")
		for _, kv := range p.Attributes.ToSlice() {
			_, _ = fmt.Fprintf(&b, " %s=%s", kv.Key, kv.Value.String())
		}
		_, _ = fmt.Fprintf(&b, " count=%d", p.Count)
		if sum {
			_, _ = fmt.Fprintf(&b, " sum=%v", p.Sum)
		}
		_, _ = fmt.Fprintln(w, b.String())
	}
}
