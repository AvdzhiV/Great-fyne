package painter

import (
	"bytes"
	"image/color"
	"image/draw"
	"math"
	"strings"
	"sync"

	"github.com/go-text/render"
	"github.com/go-text/typesetting/di"
	"github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/fontscan"
	"github.com/go-text/typesetting/language"
	"github.com/go-text/typesetting/opentype/api/metadata"
	"github.com/go-text/typesetting/shaping"
	"golang.org/x/image/math/fixed"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/internal/cache"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/theme"
)

const (
	// DefaultTabWidth is the default width in spaces
	DefaultTabWidth = 4

	fontTabSpaceSize = 10
)

var (
	fm      *fontscan.FontMap
	mapLock = sync.Mutex{}
	load    sync.Once
)

func loadMap() {
	fm = fontscan.NewFontMap(noopLogger{})
	err := loadSystemFonts(fm)
	if err != nil {
		fm = nil // just don't fallback
	}
}

func lookupLangFont(family string, aspect metadata.Aspect) font.Face {
	mapLock.Lock()
	defer mapLock.Unlock()
	load.Do(loadMap)
	if fm == nil {
		return nil
	}

	fm.SetQuery(fontscan.Query{Families: []string{family}, Aspect: aspect})
	l, _ := fontscan.NewLangID(language.Language(lang.SystemLocale().LanguageString()))
	return fm.ResolveFaceForLang(l)
}

func lookupRuneFont(r rune, family string, aspect metadata.Aspect) font.Face {
	mapLock.Lock()
	defer mapLock.Unlock()
	load.Do(loadMap)
	if fm == nil {
		return nil
	}

	fm.SetQuery(fontscan.Query{Families: []string{family}, Aspect: aspect})
	return fm.ResolveFace(r)
}

func lookupFaces(theme, fallback fyne.Resource, family string, style fyne.TextStyle) (faces *dynamicFontMap) {
	f1 := loadMeasureFont(theme)
	if theme == fallback {
		faces = &dynamicFontMap{family: family, faces: []font.Face{f1}}
	} else {
		f2 := loadMeasureFont(fallback)
		faces = &dynamicFontMap{family: family, faces: []font.Face{f1, f2}}
	}

	aspect := metadata.Aspect{Style: metadata.StyleNormal}
	if style.Italic {
		aspect.Style = metadata.StyleItalic
	}
	if style.Bold {
		aspect.Weight = metadata.WeightBold
	}

	local := lookupLangFont(family, aspect)
	if local != nil {
		faces.addFace(local)
	}

	return faces
}

// CachedFontFace returns a Font face held in memory. These are loaded from the current theme.
func CachedFontFace(style fyne.TextStyle, fontDP float32, texScale float32) *FontCacheItem {
	val, ok := fontCache.Load(style)
	if !ok {
		var faces *dynamicFontMap
		switch {
		case style.Monospace:
			faces = lookupFaces(theme.TextMonospaceFont(), theme.DefaultTextMonospaceFont(), fontscan.Monospace, style)
		case style.Bold:
			if style.Italic {
				faces = lookupFaces(theme.TextBoldItalicFont(), theme.DefaultTextBoldItalicFont(), fontscan.SansSerif, style)
			} else {
				faces = lookupFaces(theme.TextBoldFont(), theme.DefaultTextBoldFont(), fontscan.SansSerif, style)
			}
		case style.Italic:
			faces = lookupFaces(theme.TextItalicFont(), theme.DefaultTextItalicFont(), fontscan.SansSerif, style)
		case style.Symbol:
			th := theme.SymbolFont()
			fallback := theme.DefaultSymbolFont()
			f1 := loadMeasureFont(th)

			if th == fallback {
				faces = &dynamicFontMap{family: fontscan.SansSerif, faces: []font.Face{f1}}
			} else {
				f2 := loadMeasureFont(fallback)
				faces = &dynamicFontMap{family: fontscan.SansSerif, faces: []font.Face{f1, f2}}
			}
		default:
			faces = lookupFaces(theme.TextFont(), theme.DefaultTextFont(), fontscan.SansSerif, style)
		}

		if emoji := theme.DefaultEmojiFont(); !style.Symbol && emoji != nil {
			faces.addFace(loadMeasureFont(emoji)) // TODO only one emoji - maybe others too
		}
		val = &FontCacheItem{Fonts: faces}
		fontCache.Store(style, val)
	}

	return val.(*FontCacheItem)
}

// ClearFontCache is used to remove cached fonts in the case that we wish to re-load Font faces
func ClearFontCache() {

	fontCache = &sync.Map{}
}

// DrawString draws a string into an image.
func DrawString(dst draw.Image, s string, color color.Color, f shaping.Fontmap, fontSize, scale float32, style fyne.TextStyle) {
	r := render.Renderer{
		FontSize: fontSize,
		PixScale: scale,
		Color:    color,
	}

	advance := float32(0)
	y := math.MinInt
	walkString(f, s, float32ToFixed266(fontSize), style, &advance, scale, func(run shaping.Output, x float32) {
		if y == math.MinInt {
			y = int(math.Ceil(float64(fixed266ToFloat32(run.LineBounds.Ascent) * r.PixScale)))
		}
		if len(run.Glyphs) == 1 {
			if run.Glyphs[0].GlyphID == 0 {
				r.DrawStringAt(string([]rune{0xfffd}), dst, int(x), y, f.ResolveFace(0xfffd))
				return
			}
		}

		r.DrawShapedRunAt(run, dst, int(x), y)
	})
}

func loadMeasureFont(data fyne.Resource) font.Face {
	loaded, err := font.ParseTTF(bytes.NewReader(data.Content()))
	if err != nil {
		fyne.LogError("font load error", err)
		return nil
	}

	return loaded
}

// MeasureString returns how far dot would advance by drawing s with f.
// Tabs are translated into a dot location change.
func MeasureString(f shaping.Fontmap, s string, textSize float32, style fyne.TextStyle) (size fyne.Size, advance float32) {
	return walkString(f, s, float32ToFixed266(textSize), style, &advance, 1, func(shaping.Output, float32) {})
}

// RenderedTextSize looks up how big a string would be if drawn on screen.
// It also returns the distance from top to the text baseline.
func RenderedTextSize(text string, fontSize float32, style fyne.TextStyle) (size fyne.Size, baseline float32) {
	size, base := cache.GetFontMetrics(text, fontSize, style)
	if base != 0 {
		return size, base
	}

	size, base = measureText(text, fontSize, style)
	cache.SetFontMetrics(text, fontSize, style, size, base)
	return size, base
}

func fixed266ToFloat32(i fixed.Int26_6) float32 {
	return float32(float64(i) / (1 << 6))
}

func float32ToFixed266(f float32) fixed.Int26_6 {
	return fixed.Int26_6(float64(f) * (1 << 6))
}

func measureText(text string, fontSize float32, style fyne.TextStyle) (fyne.Size, float32) {
	face := CachedFontFace(style, fontSize, 1)
	return MeasureString(face.Fonts, text, fontSize, style)
}

func tabStop(spacew, x float32, tabWidth int) float32 {
	if tabWidth <= 0 {
		tabWidth = DefaultTabWidth
	}

	tabw := spacew * float32(tabWidth)
	tabs, _ := math.Modf(float64((x + tabw) / tabw))
	return tabw * float32(tabs)
}

func walkString(faces shaping.Fontmap, s string, textSize fixed.Int26_6, style fyne.TextStyle, advance *float32, scale float32,
	cb func(run shaping.Output, x float32)) (size fyne.Size, base float32) {
	s = strings.ReplaceAll(s, "\r", "")

	runes := []rune(s)
	in := shaping.Input{
		Text:      []rune{' '},
		RunStart:  0,
		RunEnd:    1,
		Direction: di.DirectionLTR,
		Face:      faces.ResolveFace(' '),
		Size:      textSize,
	}
	shaper := &shaping.HarfbuzzShaper{}
	out := shaper.Shape(in)

	in.Text = runes
	in.RunStart = 0
	in.RunEnd = len(runes)

	x := float32(0)
	spacew := scale * fontTabSpaceSize
	if style.Monospace {
		spacew = scale * fixed266ToFloat32(out.Advance)
	}
	ins := shaping.SplitByFace(in, faces)
	for _, in := range ins {
		inEnd := in.RunEnd

		pending := false
		for i, r := range in.Text[in.RunStart:in.RunEnd] {
			if r == '\t' {
				if pending {
					in.RunEnd = i
					x = shapeCallback(shaper, in, x, scale, cb)
				}
				x = tabStop(spacew, x, style.TabWidth)

				in.RunStart = i + 1
				in.RunEnd = inEnd
				pending = false
			} else {
				pending = true
			}
		}

		x = shapeCallback(shaper, in, x, scale, cb)
	}

	*advance = x
	return fyne.NewSize(*advance, fixed266ToFloat32(out.LineBounds.LineThickness())),
		fixed266ToFloat32(out.LineBounds.Ascent)
}

func shapeCallback(shaper shaping.Shaper, in shaping.Input, x, scale float32, cb func(shaping.Output, float32)) float32 {
	out := shaper.Shape(in)
	glyphs := out.Glyphs
	start := 0
	pending := false
	adv := fixed.I(0)
	for i, g := range out.Glyphs {
		if g.GlyphID == 0 {
			if pending {
				out.Glyphs = glyphs[start:i]
				cb(out, x)
				x += fixed266ToFloat32(adv) * scale
				adv = 0
			}

			out.Glyphs = glyphs[i : i+1]
			cb(out, x)
			x += fixed266ToFloat32(glyphs[i].XAdvance) * scale
			adv = 0

			start = i + 1
			pending = false
		} else {
			pending = true
		}
		adv += g.XAdvance
	}

	if pending {
		out.Glyphs = glyphs[start:]
		cb(out, x)
		x += fixed266ToFloat32(adv) * scale
		adv = 0
	}
	return x + fixed266ToFloat32(adv)*scale
}

type FontCacheItem struct {
	Fonts shaping.Fontmap
}

var fontCache = &sync.Map{} // map[fyne.TextStyle]*FontCacheItem

type noopLogger struct{}

func (n noopLogger) Printf(string, ...interface{}) {}

type dynamicFontMap struct {
	faces  []font.Face
	family string
}

func (d *dynamicFontMap) ResolveFace(r rune) font.Face {

	for _, f := range d.faces {
		if _, ok := f.NominalGlyph(r); ok {
			return f
		}
	}

	toAdd := lookupRuneFont(r, d.family, metadata.Aspect{})
	if toAdd != nil {
		d.addFace(toAdd)
		return toAdd
	}

	return d.faces[0]
}

func (d *dynamicFontMap) addFace(f font.Face) {
	d.faces = append(d.faces, f)
}
