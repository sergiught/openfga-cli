package logo

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	_ "image/png"
	"math"
	"strings"
	"sync"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/sergiught/openfga-cli/internal/style"
)

// The real OpenFGA app-icon mark, pre-rasterized to one pixel per half-block
// cell side (22x22 px -> 22 cols x 11 rows). Regenerate with assets/gen-mark.sh.
//
//go:embed mark_22.png
var markPNG []byte

const (
	markCols = 22
	markRows = 11
	// alphaCull drops resize-artifact pixels: smooth downsampling bleeds tiny
	// alphas (<12%) next to the rounded corners, while genuine anti-aliased
	// edges start near 50%. Anything below the cull renders transparent.
	alphaCull = 0.15
)

var (
	markOnce sync.Once
	markImg  image.Image
)

func markImage() image.Image {
	markOnce.Do(func() {
		if img, _, err := image.Decode(bytes.NewReader(markPNG)); err == nil {
			markImg = img
		}
	})
	return markImg
}

// MarkSize returns the mark's cell dimensions (columns, rows).
func MarkSize() (int, int) { return markCols, markRows }

// Mark renders the OpenFGA mark as truecolor half-block cells. Mono themes
// and decode failures fall back to the slab wordmark.
func Mark() string { return markRender(-1) }

// MarkShimmer is Mark with the entrance highlight band at the given phase
// (0..1 sweeps the band across the mark's diagonal).
func MarkShimmer(phase float64) string { return markRender(phase) }

func markRender(phase float64) string {
	if style.Active.Name == "mono" {
		return Word("ofga")
	}
	img := markImage()
	if img == nil {
		return style.GradientBlock(Word("ofga"))
	}
	min := img.Bounds().Min
	var b strings.Builder
	for row := 0; row < markRows; row++ {
		for x := 0; x < markCols; x++ {
			top := pixelHex(img, min.X+x, min.Y+row*2, x, row, phase)
			bot := pixelHex(img, min.X+x, min.Y+row*2+1, x, row, phase)
			b.WriteString(cell(top, bot))
		}
		if row < markRows-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// pixelHex resolves one pixel to a hex color string, alpha-blended onto the
// theme base (premultiplied-over) and brightened by the shimmer band when a
// phase >= 0 is given. Returns "" for fully transparent pixels.
func pixelHex(img image.Image, px, py, cx, cy int, phase float64) string {
	r, g, bl, a := img.At(px, py).RGBA()
	af := float64(a) / 0xffff
	if af < alphaCull {
		return ""
	}
	br, bg, bb, _ := style.BgBase.RGBA()
	mix := func(fg, bgc uint32) float64 {
		return (float64(fg) + float64(bgc)*(1-af)) / 0xffff * 255
	}
	rf, gf, bf := mix(r, br), mix(g, bg), mix(bl, bb)
	if phase >= 0 {
		// Highlight band on the normalized x+y diagonal — the same band math
		// as style's retired block-shimmer helper, applied per pixel cell.
		d := math.Abs(float64(cx+cy)/float64(markCols+markRows-2) - phase)
		if d < 0.18 {
			k := (0.18 - d) / 0.18 * 0.6
			rf += (255 - rf) * k
			gf += (255 - gf) * k
			bf += (255 - bf) * k
		}
	}
	return fmt.Sprintf("#%02X%02X%02X", int(math.Round(rf)), int(math.Round(gf)), int(math.Round(bf)))
}

// cell emits one half-block cell from the top/bottom pixel colors ("" =
// transparent side).
func cell(top, bot string) string {
	switch {
	case top == "" && bot == "":
		return " "
	case top != "" && bot != "":
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(top)).Background(lipgloss.Color(bot)).
			Render("▀")
	case top != "":
		return lipgloss.NewStyle().Foreground(lipgloss.Color(top)).Render("▀")
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color(bot)).Render("▄")
	}
}
