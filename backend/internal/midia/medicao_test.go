package midia

import (
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Medição (não é limite de aprovação rígido): tempo e pico de memória para processar uma foto no
// limite de 12 MP. Com MEDIA_FOTO=/caminho/foto.jpg usa uma foto externa; senão gera uma.
func TestProcessAtLimitMeasured(t *testing.T) {
	if testing.Short() {
		t.Skip("medição demorada")
	}
	src := []byte(nil)
	if p := os.Getenv("MEDIA_FOTO"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		src = b
	} else {
		src = encJPEG(t, photo(4000, 3000))
	}
	_, w, h, err := Probe(src)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	var peak atomic.Uint64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		var m runtime.MemStats
		for {
			runtime.ReadMemStats(&m)
			if m.HeapInuse > peak.Load() {
				peak.Store(m.HeapInuse)
			}
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	})
	start := time.Now()
	out, err := process(src, PurposeAvatar, nil)
	d := time.Since(start)
	close(stop)
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, v := range out {
		total += len(v.Data)
	}
	t.Logf("MEDIÇÃO %dx%d (%.1f MP, %d bytes de entrada): %s, pico de heap ≈ %.0f MiB acima da base, variantes somam %d bytes",
		w, h, float64(w*h)/1e6, len(src), d.Round(time.Millisecond), float64(peak.Load()-min(peak.Load(), base.HeapInuse))/(1<<20), total)
	if d > processTimeout {
		t.Fatalf("processamento acima do tempo-limite (%s)", processTimeout)
	}
}
