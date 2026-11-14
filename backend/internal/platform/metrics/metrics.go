// Package metrics expõe métricas no formato de texto do Prometheus (versão 0.0.4), sem
// dependências externas (ADR-0026).
//
// Regras:
//   - Rótulos só com valores de conjunto pequeno e conhecido (rota-modelo, método, código de status,
//     tipo de pendência). NUNCA ids, e-mails, nicks, IPs ou caminhos reais: métrica não carrega
//     dado pessoal e não pode explodir em quantidade de séries.
//   - O servidor de métricas fica num endereço separado (FILA_METRICS_ADDR), nunca publicado.
package metrics

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Registry guarda as métricas de um processo.
type Registry struct {
	mu      sync.Mutex
	metrics []collector
	names   map[string]bool
}

// NewRegistry cria um registro vazio.
func NewRegistry() *Registry { return &Registry{names: map[string]bool{}} }

type collector interface {
	name() string
	write(w io.Writer)
}

func (r *Registry) add(c collector) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.names[c.name()] {
		panic("métrica registrada duas vezes: " + c.name())
	}
	r.names[c.name()] = true
	r.metrics = append(r.metrics, c)
}

// WriteTo escreve todas as métricas no formato de texto (ordem alfabética por nome).
func (r *Registry) Write(w io.Writer) {
	r.mu.Lock()
	list := append([]collector(nil), r.metrics...)
	r.mu.Unlock()
	sort.Slice(list, func(i, j int) bool { return list[i].name() < list[j].name() })
	for _, c := range list {
		c.write(w)
	}
}

// Handler serve GET /metrics.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		r.Write(w)
	})
}

// Serve atende só /metrics em addr até ctx terminar (servidor separado da API).
func Serve(ctx context.Context, addr string, reg *Registry, log *slog.Logger) error {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", reg.Handler())
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 8 << 10}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("métricas: não foi possível abrir %s: %w", addr, err)
	}
	log.Info("métricas ouvindo (não publicar na internet)", "addr", ln.Addr().String())
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------------------------
// Séries com rótulos

type series struct {
	labels []string
	value  float64
}

type family struct {
	mu     sync.Mutex
	n      string
	help   string
	kind   string
	labels []string
	series map[string]*series
}

func newFamily(n, help, kind string, labels []string) *family {
	return &family{n: n, help: help, kind: kind, labels: labels, series: map[string]*series{}}
}

func (f *family) name() string { return f.n }

func (f *family) get(values []string) *series {
	if len(values) != len(f.labels) {
		panic(fmt.Sprintf("métrica %s: esperava %d rótulos, recebeu %d", f.n, len(f.labels), len(values)))
	}
	key := strings.Join(values, "\x00")
	s, ok := f.series[key]
	if !ok {
		s = &series{labels: append([]string(nil), values...)}
		f.series[key] = s
	}
	return s
}

func (f *family) write(w io.Writer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	header(w, f.n, f.help, f.kind)
	keys := make([]string, 0, len(f.series))
	for k := range f.series {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s := f.series[k]
		fmt.Fprintf(w, "%s%s %s\n", f.n, labelText(f.labels, s.labels, "", ""), num(s.value))
	}
}

// Counter só cresce.
type Counter struct{ f *family }

// NewCounter registra um contador.
func (r *Registry) NewCounter(name, help string, labels ...string) *Counter {
	c := &Counter{f: newFamily(name, help, "counter", labels)}
	r.add(c.f)
	return c
}

// Add soma delta (>= 0) à série dos rótulos.
func (c *Counter) Add(delta float64, labels ...string) {
	if delta < 0 {
		panic("contador não diminui")
	}
	c.f.mu.Lock()
	c.f.get(labels).value += delta
	c.f.mu.Unlock()
}

// Inc soma 1.
func (c *Counter) Inc(labels ...string) { c.Add(1, labels...) }

// Value lê a série (testes).
func (c *Counter) Value(labels ...string) float64 {
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	return c.f.get(labels).value
}

// Gauge sobe e desce.
type Gauge struct{ f *family }

// NewGauge registra um medidor.
func (r *Registry) NewGauge(name, help string, labels ...string) *Gauge {
	g := &Gauge{f: newFamily(name, help, "gauge", labels)}
	r.add(g.f)
	return g
}

// Set define o valor da série.
func (g *Gauge) Set(v float64, labels ...string) {
	g.f.mu.Lock()
	g.f.get(labels).value = v
	g.f.mu.Unlock()
}

// Add soma delta (pode ser negativo).
func (g *Gauge) Add(delta float64, labels ...string) {
	g.f.mu.Lock()
	g.f.get(labels).value += delta
	g.f.mu.Unlock()
}

// GaugeFunc é lido na hora da coleta.
type gaugeFunc struct {
	n, help string
	fn      func() float64
}

func (g *gaugeFunc) name() string { return g.n }
func (g *gaugeFunc) write(w io.Writer) {
	header(w, g.n, g.help, "gauge")
	fmt.Fprintf(w, "%s %s\n", g.n, num(g.fn()))
}

// NewGaugeFunc registra um medidor calculado na coleta (deve ser rápido e sem I/O lento).
func (r *Registry) NewGaugeFunc(name, help string, fn func() float64) {
	r.add(&gaugeFunc{n: name, help: help, fn: fn})
}

// ---------------------------------------------------------------------------------------------
// Histograma

// DefaultBuckets cobre de 5 ms a 10 s (latência de requisição).
var DefaultBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type hseries struct {
	labels []string
	counts []uint64 // por limite (não acumulado)
	sum    float64
	count  uint64
}

// Histogram conta observações em faixas.
type Histogram struct {
	mu      sync.Mutex
	n, help string
	labels  []string
	buckets []float64
	series  map[string]*hseries
}

// NewHistogram registra um histograma.
func (r *Registry) NewHistogram(name, help string, buckets []float64, labels ...string) *Histogram {
	h := &Histogram{n: name, help: help, labels: labels, buckets: buckets, series: map[string]*hseries{}}
	r.add(h)
	return h
}

func (h *Histogram) name() string { return h.n }

// Observe registra um valor.
func (h *Histogram) Observe(v float64, labels ...string) {
	if len(labels) != len(h.labels) {
		panic("histograma " + h.n + ": rótulos errados")
	}
	key := strings.Join(labels, "\x00")
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.series[key]
	if !ok {
		s = &hseries{labels: append([]string(nil), labels...), counts: make([]uint64, len(h.buckets))}
		h.series[key] = s
	}
	for i, b := range h.buckets {
		if v <= b {
			s.counts[i]++
			break
		}
	}
	s.sum += v
	s.count++
}

func (h *Histogram) write(w io.Writer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	header(w, h.n, h.help, "histogram")
	keys := make([]string, 0, len(h.series))
	for k := range h.series {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s := h.series[k]
		var cum uint64
		for i, b := range h.buckets {
			cum += s.counts[i]
			fmt.Fprintf(w, "%s_bucket%s %d\n", h.n, labelText(h.labels, s.labels, "le", num(b)), cum)
		}
		fmt.Fprintf(w, "%s_bucket%s %d\n", h.n, labelText(h.labels, s.labels, "le", "+Inf"), s.count)
		fmt.Fprintf(w, "%s_sum%s %s\n", h.n, labelText(h.labels, s.labels, "", ""), num(s.sum))
		fmt.Fprintf(w, "%s_count%s %d\n", h.n, labelText(h.labels, s.labels, "", ""), s.count)
	}
}

// ---------------------------------------------------------------------------------------------

func header(w io.Writer, name, help, kind string) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", name, escapeHelp(help), name, kind)
}

func labelText(names, values []string, extraName, extraValue string) string {
	if len(names) == 0 && extraName == "" {
		return ""
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, n := range names {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(n)
		b.WriteString(`="`)
		b.WriteString(escapeLabel(values[i]))
		b.WriteByte('"')
	}
	if extraName != "" {
		if len(names) > 0 {
			b.WriteByte(',')
		}
		b.WriteString(extraName)
		b.WriteString(`="`)
		b.WriteString(extraValue)
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

func escapeLabel(s string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`).Replace(s)
}

func escapeHelp(s string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(s)
}

func num(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case math.IsNaN(v):
		return "NaN"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}
