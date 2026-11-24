package midia

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/webp"
)

// Limites de entrada (decisões do produto, ADR-0028).
const (
	MaxUploadBytes = 5 << 20    // 5 MiB por envio
	MaxPixels      = 12_000_000 // 12 megapixels
	MaxSide        = 8192       // por eixo
)

// Finalidades (enum MediaPurpose no contrato).
const (
	PurposeAvatar            = "avatar"
	PurposeProfessionalPhoto = "professional_photo"
	PurposeLogo              = "logo"
	PurposeProductPhoto      = "product_photo"
)

// Formatos aceitos: JPEG, PNG e WebP estáticos. SVG, GIF, animações, HEIC e desconhecidos não.
const (
	formatJPEG = "jpeg"
	formatPNG  = "png"
	formatWebP = "webp"
)

// Erros de validação da imagem (viram failure_reason ou resposta 415/422).
var (
	errUnsupported = errors.New("formato não aceito")
	errInvalid     = errors.New("imagem inválida")
	errDimensions  = errors.New("dimensões acima do limite")
)

// sizesFor devolve os lados (px) das variantes de cada finalidade.
func sizesFor(purpose string) []int {
	if purpose == PurposeLogo {
		return []int{128, 256, 512}
	}
	return []int{64, 128, 256}
}

// sniff identifica o formato real pela assinatura (nunca pelo Content-Type ou nome do arquivo).
func sniff(b []byte) (string, error) {
	switch {
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return formatJPEG, nil
	case len(b) >= 8 && bytes.Equal(b[:8], []byte("\x89PNG\r\n\x1a\n")):
		if pngAnimated(b) {
			return "", errUnsupported
		}
		return formatPNG, nil
	case len(b) >= 16 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		if webpAnimated(b) {
			return "", errUnsupported
		}
		return formatWebP, nil
	}
	return "", errUnsupported
}

// pngAnimated procura o bloco acTL (APNG), que vem antes do primeiro IDAT.
func pngAnimated(b []byte) bool {
	i := 8
	for i+8 <= len(b) {
		n := int(binary.BigEndian.Uint32(b[i:]))
		typ := string(b[i+4 : i+8])
		if typ == "acTL" {
			return true
		}
		if typ == "IDAT" || n < 0 || n > len(b) {
			return false
		}
		i += 12 + n
	}
	return false
}

// webpAnimated confere a marca de animação do bloco VP8X e a presença de ANIM/ANMF.
func webpAnimated(b []byte) bool {
	if len(b) >= 21 && string(b[12:16]) == "VP8X" && b[20]&0x02 != 0 {
		return true
	}
	// Percorre os blocos RIFF (fourcc + tamanho little-endian, com preenchimento par).
	for i, n := 12, 0; i+8 <= len(b) && n < 1024; n++ {
		typ := string(b[i : i+4])
		size := int(binary.LittleEndian.Uint32(b[i+4:]))
		if typ == "ANIM" || typ == "ANMF" {
			return true
		}
		if size < 0 || size > len(b) {
			return false
		}
		i += 8 + size + size%2
	}
	return false
}

// Probe confere formato e dimensões sem decodificar os pixels (usado já no envio).
func Probe(b []byte) (format string, w, h int, err error) {
	format, err = sniff(b)
	if err != nil {
		return "", 0, 0, err
	}
	var cfg image.Config
	switch format {
	case formatJPEG:
		cfg, err = jpeg.DecodeConfig(bytes.NewReader(b))
	case formatPNG:
		cfg, err = png.DecodeConfig(bytes.NewReader(b))
	case formatWebP:
		cfg, err = webp.DecodeConfig(bytes.NewReader(b))
	}
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return "", 0, 0, errInvalid
	}
	if cfg.Width > MaxSide || cfg.Height > MaxSide || cfg.Width*cfg.Height > MaxPixels {
		return "", 0, 0, errDimensions
	}
	return format, cfg.Width, cfg.Height, nil
}

// Crop é o recorte quadrado normalizado (0..1) sobre a imagem já orientada: X e Y relativos à
// largura e à altura; Size relativo ao menor lado.
type Crop struct {
	X, Y, Size float64
}

// encoded é uma variante pronta.
type encoded struct {
	Size   int
	Width  int
	Height int
	Mime   string
	Ext    string
	Data   []byte
}

// process decodifica com limites, orienta, recorta, reduz e recodifica. Nunca devolve o original.
func process(b []byte, purpose string, crop *Crop) ([]encoded, error) {
	format, _, _, err := Probe(b)
	if err != nil {
		return nil, err
	}
	var img image.Image
	switch format {
	case formatJPEG:
		img, err = jpeg.Decode(bytes.NewReader(b))
	case formatPNG:
		img, err = png.Decode(bytes.NewReader(b))
	case formatWebP:
		img, err = webp.Decode(bytes.NewReader(b))
	}
	if err != nil {
		return nil, errInvalid
	}
	o := 1
	if format == formatJPEG {
		o = jpegOrientation(b)
	}
	// O recorte é pedido sobre a imagem "em pé" (como o navegador mostra). Em vez de girar a foto
	// inteira (12 MP = dezenas de MiB a mais), o recorte é levado para as coordenadas do original,
	// reduzido ali, e só a variante pequena é girada.
	ob := img.Bounds()
	ow, oh := ob.Dx(), ob.Dy()
	if o >= 5 {
		ow, oh = oh, ow
	}
	upright := cropRect(image.Rect(0, 0, ow, oh), purpose, crop)
	src := sourceRect(upright, o, ob.Dx(), ob.Dy()).Add(ob.Min)
	logo := purpose == PurposeLogo
	opaque := !logo || isOpaque(img, src)
	sizes := sizesFor(purpose)
	longest := max(upright.Dx(), upright.Dy())
	// Sem ampliar: a menor variante existe sempre (no tamanho original, se for menor).
	keep := sizes[:1]
	for _, s := range sizes[1:] {
		if longest >= s {
			keep = append(keep, s)
		}
	}
	// A maior variante sai do original; as menores, da maior (uma única passada pela foto grande).
	out := make([]encoded, len(keep))
	var from image.Image = img
	fromRect := src
	for i := len(keep) - 1; i >= 0; i-- {
		w, h := fit(upright.Dx(), upright.Dy(), keep[i])
		sw, sh := w, h
		if o >= 5 {
			sw, sh = h, w
		}
		dst := image.NewNRGBA(image.Rect(0, 0, sw, sh))
		if !logo || opaque {
			// Avatar/foto: fundo branco sob transparência (JPEG não tem alfa).
			draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
			xdraw.CatmullRom.Scale(dst, dst.Bounds(), from, fromRect, draw.Over, nil)
		} else {
			xdraw.CatmullRom.Scale(dst, dst.Bounds(), from, fromRect, draw.Src, nil)
		}
		from, fromRect = dst, dst.Bounds()
		final, _ := orient(dst, o).(*image.NRGBA)
		if final == nil {
			final = dst
		}
		e, err := encode(final, keep[i], logo, opaque)
		if err != nil {
			return nil, err
		}
		out[i] = e
	}
	return out, nil
}

// sourceRect leva um retângulo da imagem "em pé" (orientação EXIF o aplicada) para as coordenadas
// da imagem original de largura w e altura h (inverso das transformações de orient).
func sourceRect(r image.Rectangle, o, w, h int) image.Rectangle {
	x0, y0, x1, y1 := r.Min.X, r.Min.Y, r.Max.X, r.Max.Y
	switch o {
	case 2:
		return image.Rect(w-x1, y0, w-x0, y1)
	case 3:
		return image.Rect(w-x1, h-y1, w-x0, h-y0)
	case 4:
		return image.Rect(x0, h-y1, x1, h-y0)
	case 5:
		return image.Rect(y0, x0, y1, x1)
	case 6:
		return image.Rect(y0, h-x1, y1, h-x0)
	case 7:
		return image.Rect(w-y1, h-x1, w-y0, h-x0)
	case 8:
		return image.Rect(w-y1, x0, w-y0, x1)
	}
	return r
}

// fit reduz (sem ampliar) para caber num quadrado de lado s, mantendo a proporção.
func fit(w, h, s int) (int, int) {
	if w <= s && h <= s {
		return w, h
	}
	if w >= h {
		return s, max(1, h*s/w)
	}
	return max(1, w*s/h), s
}

// cropRect: avatar e foto profissional são quadrados (recorte pedido ou o maior quadrado central);
// a logo é enquadrada inteira por padrão (sem cortar o nome da marca), com recorte opcional.
func cropRect(b image.Rectangle, purpose string, c *Crop) image.Rectangle {
	w, h := b.Dx(), b.Dy()
	side := min(w, h)
	if c == nil {
		if purpose == PurposeLogo {
			return b
		}
		x0 := b.Min.X + (w-side)/2
		y0 := b.Min.Y + (h-side)/2
		return image.Rect(x0, y0, x0+side, y0+side)
	}
	s := max(1, int(c.Size*float64(side)))
	x0 := min(max(0, int(c.X*float64(w))), w-s)
	y0 := min(max(0, int(c.Y*float64(h))), h-s)
	return image.Rect(b.Min.X+x0, b.Min.Y+y0, b.Min.X+x0+s, b.Min.Y+y0+s)
}

// isOpaque informa se a região não tem nenhum pixel transparente.
func isOpaque(img image.Image, r image.Rectangle) bool {
	if o, ok := img.(interface{ Opaque() bool }); ok && o.Opaque() {
		return true
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a != 0xffff {
				return false
			}
		}
	}
	return true
}

// logoBudget é o orçamento da maior variante de logo (ADR-0028); PNG acima dele, se opaco, vira JPEG.
const logoBudget = 120 << 10

func encode(img *image.NRGBA, size int, logo, opaque bool) (encoded, error) {
	b := img.Bounds()
	e := encoded{Size: size, Width: b.Dx(), Height: b.Dy()}
	var buf bytes.Buffer
	if logo {
		// Logo: PNG preserva transparência e bordas nítidas de texto.
		enc := png.Encoder{CompressionLevel: png.BestCompression}
		if err := enc.Encode(&buf, img); err != nil {
			return e, err
		}
		if !opaque || buf.Len() <= logoBudget {
			e.Mime, e.Ext, e.Data = "image/png", "png", buf.Bytes()
			return e, nil
		}
		buf.Reset()
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 88}); err != nil {
			return e, err
		}
		e.Mime, e.Ext, e.Data = "image/jpeg", "jpg", buf.Bytes()
		return e, nil
	}
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 82}); err != nil {
		return e, err
	}
	e.Mime, e.Ext, e.Data = "image/jpeg", "jpg", buf.Bytes()
	return e, nil
}

// ---------------------------------------------------------------------------------------------
// Orientação EXIF (só JPEG). Leitura limitada e defensiva: qualquer coisa fora do esperado = 1.

// jpegOrientation lê a tag 0x0112 do primeiro segmento APP1 Exif, se houver.
func jpegOrientation(b []byte) int {
	i := 2
	for n := 0; i+4 <= len(b) && n < 64; n++ {
		if b[i] != 0xFF {
			return 1
		}
		m := b[i+1]
		if m == 0xD8 || m == 0x01 || (m >= 0xD0 && m <= 0xD7) {
			i += 2
			continue
		}
		if m == 0xDA || m == 0xD9 {
			return 1
		}
		l := int(b[i+2])<<8 | int(b[i+3])
		if l < 2 || i+2+l > len(b) {
			return 1
		}
		if m == 0xE1 {
			if o := exifOrientation(b[i+4 : i+2+l]); o != 0 {
				return o
			}
		}
		i += 2 + l
	}
	return 1
}

func exifOrientation(seg []byte) int {
	if len(seg) < 14 || string(seg[:6]) != "Exif\x00\x00" {
		return 0
	}
	t := seg[6:]
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0
	}
	if bo.Uint16(t[2:4]) != 42 {
		return 0
	}
	off := int(bo.Uint32(t[4:8]))
	if off < 8 || off+2 > len(t) {
		return 0
	}
	n := int(bo.Uint16(t[off:]))
	for k := 0; k < n && k < 256; k++ {
		e := off + 2 + k*12
		if e+12 > len(t) {
			return 0
		}
		if bo.Uint16(t[e:]) == 0x0112 {
			if bo.Uint16(t[e+2:]) != 3 { // SHORT
				return 0
			}
			if v := int(bo.Uint16(t[e+8:])); v >= 1 && v <= 8 {
				return v
			}
			return 0
		}
	}
	return 0
}

// orient aplica a orientação EXIF (2..8) gerando uma imagem nova "em pé".
func orient(src image.Image, o int) image.Image {
	if o < 2 || o > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch o {
			case 2:
				dx, dy = w-1-x, y
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dx, dy = x, h-1-y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			}
			dst.Set(dx, dy, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}
