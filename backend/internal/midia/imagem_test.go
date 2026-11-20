package midia

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"testing"
)

// photo gera uma imagem com ruído (comprime mal, como uma foto real) e um canto marcado.
func photo(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewPCG(1, 2))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{uint8(x * 255 / w), uint8(y * 255 / h), uint8(r.IntN(256)), 255})
		}
	}
	return img
}

func encJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 92}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func encPNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// pngChunk monta um bloco PNG com CRC correto.
func pngChunk(typ string, data []byte) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.BigEndian, uint32(len(data)))
	b.WriteString(typ)
	b.Write(data)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(append([]byte(typ), data...)))
	return b.Bytes()
}

// pngHeader devolve só assinatura + IHDR com as dimensões pedidas (sem pixels).
func pngHeader(w, h uint32, extra ...[]byte) []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = 8, 6 // 8 bits, RGBA
	out := append([]byte("\x89PNG\r\n\x1a\n"), pngChunk("IHDR", ihdr)...)
	for _, e := range extra {
		out = append(out, e...)
	}
	return out
}

// withExif insere um segmento APP1 Exif com a orientação dada logo depois do SOI.
func withExif(jpg []byte, orientation uint16, gps bool) []byte {
	var t bytes.Buffer
	t.WriteString("MM")
	_ = binary.Write(&t, binary.BigEndian, uint16(42))
	_ = binary.Write(&t, binary.BigEndian, uint32(8))
	n := uint16(1)
	if gps {
		n = 2
	}
	_ = binary.Write(&t, binary.BigEndian, n)
	// entrada: tag, tipo SHORT (3), contagem 1, valor
	_ = binary.Write(&t, binary.BigEndian, []uint16{0x0112, 3})
	_ = binary.Write(&t, binary.BigEndian, uint32(1))
	_ = binary.Write(&t, binary.BigEndian, []uint16{orientation, 0})
	if gps {
		_ = binary.Write(&t, binary.BigEndian, []uint16{0x8825, 4})
		_ = binary.Write(&t, binary.BigEndian, uint32(1))
		_ = binary.Write(&t, binary.BigEndian, uint32(0x4C4F43)) // "LOC": marcador procurado na saída
	}
	_ = binary.Write(&t, binary.BigEndian, uint32(0))
	seg := append([]byte("Exif\x00\x00"), t.Bytes()...)
	app1 := []byte{0xFF, 0xE1, byte((len(seg) + 2) >> 8), byte(len(seg) + 2)}
	out := append([]byte{}, jpg[:2]...)
	out = append(out, app1...)
	out = append(out, seg...)
	return append(out, jpg[2:]...)
}

func TestSniffRejectsActiveAndUnknownFormats(t *testing.T) {
	var g bytes.Buffer
	_ = gif.Encode(&g, image.NewPaletted(image.Rect(0, 0, 4, 4), color.Palette{color.Black, color.White}), nil)
	cases := map[string][]byte{
		"svg":         []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"svg com bom": []byte("\xef\xbb\xbf<?xml version=\"1.0\"?><svg/>"),
		"gif":         g.Bytes(),
		"heic":        append([]byte{0, 0, 0, 0x18}, []byte("ftypheic")...),
		"html":        []byte("<html><body>oi</body></html>"),
		"vazio":       {},
		"apng":        pngHeader(4, 4, pngChunk("acTL", make([]byte, 8))),
		"webp animado": append(append([]byte("RIFF\x00\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00"),
			0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0), []byte("ANIM\x06\x00\x00\x00\x00\x00\x00\x00\x00\x00")...),
	}
	for name, b := range cases {
		if _, _, _, err := Probe(b); err != errUnsupported {
			t.Errorf("%s: esperava formato recusado, veio %v", name, err)
		}
	}
}

func TestProbeLimits(t *testing.T) {
	if _, _, _, err := Probe(pngHeader(MaxSide+1, 10)); err != errDimensions {
		t.Fatalf("lado acima do limite: %v", err)
	}
	if _, _, _, err := Probe(pngHeader(4000, 4000)); err != errDimensions { // 16 MP
		t.Fatalf("megapixels acima do limite: %v", err)
	}
	if _, _, _, err := Probe([]byte("\xFF\xD8\xFF\xE0lixo")); err != errInvalid {
		t.Fatalf("JPEG quebrado: %v", err)
	}
	f, w, h, err := Probe(encJPEG(t, photo(300, 200)))
	if err != nil || f != formatJPEG || w != 300 || h != 200 {
		t.Fatalf("JPEG válido: %s %dx%d %v", f, w, h, err)
	}
}

func TestAvatarVariantsBudgetsAndNoMetadata(t *testing.T) {
	src := withExif(encJPEG(t, photo(1600, 1200)), 1, true)
	if !bytes.Contains(src, []byte("Exif")) {
		t.Fatal("fixture sem EXIF")
	}
	out, err := process(src, PurposeAvatar, nil)
	if err != nil {
		t.Fatal(err)
	}
	budgets := map[int]int{64: 12 << 10, 128: 25 << 10, 256: 60 << 10}
	if len(out) != 3 {
		t.Fatalf("variantes: %d", len(out))
	}
	for _, v := range out {
		if v.Width != v.Size || v.Height != v.Size {
			t.Errorf("avatar %d não é quadrado: %dx%d", v.Size, v.Width, v.Height)
		}
		if len(v.Data) > budgets[v.Size] {
			t.Errorf("avatar %d: %d bytes > orçamento %d", v.Size, len(v.Data), budgets[v.Size])
		}
		if bytes.Contains(v.Data, []byte("Exif")) || bytes.Contains(v.Data, []byte("LOC")) {
			t.Errorf("avatar %d manteve metadados", v.Size)
		}
		if v.Mime != "image/jpeg" {
			t.Errorf("avatar %d: %s", v.Size, v.Mime)
		}
		t.Logf("avatar %dpx: %d bytes", v.Size, len(v.Data))
	}
}

func TestNoUpscale(t *testing.T) {
	out, err := process(encPNG(t, photo(100, 100)), PurposeAvatar, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 100 px: só a variante de 64 (reduzida); 128 e 256 não são criadas por ampliação.
	if len(out) != 1 || out[0].Width != 64 {
		t.Fatalf("variantes: %+v", out)
	}
	tiny, err := process(encPNG(t, photo(40, 30)), PurposeAvatar, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tiny) != 1 || tiny[0].Width != 30 || tiny[0].Height != 30 {
		t.Fatalf("imagem menor que a menor variante deve ficar no tamanho real: %+v", tiny)
	}
}

func TestExifOrientationApplied(t *testing.T) {
	// 40x20: metade esquerda vermelha, direita azul. Orientação 6 = girar 90° no sentido horário:
	// a imagem em pé fica 20x40 com o vermelho em cima.
	img := image.NewNRGBA(image.Rect(0, 0, 40, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 40; x++ {
			c := color.NRGBA{255, 0, 0, 255}
			if x >= 20 {
				c = color.NRGBA{0, 0, 255, 255}
			}
			img.Set(x, y, c)
		}
	}
	src := withExif(encJPEG(t, img), 6, false)
	if o := jpegOrientation(src); o != 6 {
		t.Fatalf("orientação lida: %d", o)
	}
	up := orient(img, 6)
	if b := up.Bounds(); b.Dx() != 20 || b.Dy() != 40 {
		t.Fatalf("dimensões depois de orientar: %v", b)
	}
	r, _, bl, _ := up.At(10, 5).RGBA()
	if r < 0x8000 || bl > 0x8000 {
		t.Fatalf("topo deveria ser vermelho depois de girar")
	}
	// Segmentos estranhos nunca derrubam a leitura.
	for _, junk := range [][]byte{{0xFF, 0xD8}, {0xFF, 0xD8, 0xFF, 0xE1, 0xFF, 0xFF}, {0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x10, 'E', 'x', 'i', 'f', 0, 0, 'M', 'M', 0, 42, 0xFF, 0xFF, 0xFF, 0xFF}} {
		if o := jpegOrientation(junk); o != 1 {
			t.Fatalf("lixo: %d", o)
		}
	}
}

func TestCropAndLogoTransparency(t *testing.T) {
	out, err := process(encPNG(t, photo(400, 200)), PurposeAvatar, &Crop{X: 0.5, Y: 0, Size: 1})
	if err != nil || len(out) == 0 {
		t.Fatal(err)
	}
	// Logo com fundo transparente: PNG, sem corte (proporção mantida) e com alfa preservado.
	logo := image.NewNRGBA(image.Rect(0, 0, 600, 200))
	for x := 100; x < 500; x++ {
		for y := 50; y < 150; y++ {
			logo.Set(x, y, color.NRGBA{20, 20, 20, 255})
		}
	}
	lv, err := process(encPNG(t, logo), PurposeLogo, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(lv) != 3 {
		t.Fatalf("variantes de logo: %d", len(lv))
	}
	big := lv[2]
	if big.Mime != "image/png" || big.Width != 512 || big.Height != 170 {
		t.Fatalf("logo: %s %dx%d", big.Mime, big.Width, big.Height)
	}
	if len(big.Data) > logoBudget {
		t.Errorf("logo 512: %d bytes > %d", len(big.Data), logoBudget)
	}
	dec, err := png.Decode(bytes.NewReader(big.Data))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, a := dec.At(0, 0).RGBA(); a != 0 {
		t.Fatal("transparência da logo perdida")
	}
	t.Logf("logo 512px transparente: %d bytes", len(big.Data))
	// Logo opaca e fotográfica: acima do orçamento em PNG vira JPEG.
	ph, err := process(encPNG(t, photo(1024, 1024)), PurposeLogo, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ph[2].Mime != "image/jpeg" || len(ph[2].Data) > logoBudget {
		t.Errorf("logo fotográfica: %s %d bytes", ph[2].Mime, len(ph[2].Data))
	}
	t.Logf("logo 512px fotográfica: %s %d bytes", ph[2].Mime, len(ph[2].Data))
}

// A orientação é aplicada no resultado final (recorte calculado na imagem "em pé").
func TestProcessAppliesOrientationEveryCase(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 80, 40)) // esquerda vermelha, direita azul
	for y := 0; y < 40; y++ {
		for x := 0; x < 80; x++ {
			c := color.NRGBA{255, 0, 0, 255}
			if x >= 40 {
				c = color.NRGBA{0, 0, 255, 255}
			}
			img.Set(x, y, c)
		}
	}
	isRed := func(c color.Color) bool { r, _, b, _ := c.RGBA(); return r > 0xa000 && b < 0x6000 }
	for o := 1; o <= 8; o++ {
		src := withExif(encJPEG(t, img), uint16(o), false)
		out, err := process(src, PurposeLogo, nil) // logo: sem recorte, mantém a proporção
		if err != nil {
			t.Fatal(err)
		}
		dec, err := png.Decode(bytes.NewReader(out[0].Data))
		if err != nil {
			dec2, err2 := jpeg.Decode(bytes.NewReader(out[0].Data))
			if err2 != nil {
				t.Fatal(err, err2)
			}
			dec = dec2
		}
		want := orient(img, o)
		wb := want.Bounds()
		if dec.Bounds().Dx() != wb.Dx() || dec.Bounds().Dy() != wb.Dy() {
			t.Fatalf("orientação %d: %v, esperado %v", o, dec.Bounds(), wb)
		}
		for _, pt := range []image.Point{{wb.Dx() / 4, wb.Dy() / 4}, {wb.Dx() * 3 / 4, wb.Dy() * 3 / 4}} {
			if isRed(dec.At(pt.X, pt.Y)) != isRed(want.At(pt.X, pt.Y)) {
				t.Errorf("orientação %d: cor errada em %v", o, pt)
			}
		}
	}
}
