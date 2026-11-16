package fuzzing

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"io"
	"maps"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/bep/imagemeta"
	"github.com/gohugoio/hugo/common/hmaps"
	"github.com/gohugoio/hugo/common/loggers"
	"github.com/gohugoio/hugo/common/paths"
	"github.com/gohugoio/hugo/config"
	"github.com/gohugoio/hugo/config/allconfig"
	"github.com/gohugoio/hugo/config/testconfig"
	"github.com/gohugoio/hugo/hugofs/files"
	"github.com/gohugoio/hugo/hugolib/sitesmatrix"
	"github.com/gohugoio/hugo/markup/converter"
	"github.com/gohugoio/hugo/markup/highlight"
	"github.com/gohugoio/hugo/markup/markup_config"
	"github.com/gohugoio/hugo/markup/org"
	"github.com/gohugoio/hugo/media"
	"github.com/gohugoio/hugo/minifiers"
	"github.com/gohugoio/hugo/output"
	"github.com/gohugoio/hugo/parser/metadecoders"
	"github.com/gohugoio/hugo/parser/pageparser"
	"github.com/gohugoio/hugo/resources/images"
	"github.com/gohugoio/hugo/resources/images/meta"
	"github.com/gohugoio/hugo/resources/kinds"
	"github.com/gohugoio/hugo/resources/page"
	"github.com/gohugoio/hugo/resources/page/pagemeta"
	"github.com/spf13/afero"
	"github.com/spf13/cast"
)

// Markdownify renders the input through the `markdownify` template function
// (goldmark with the site's markup configuration).
func Markdownify(data []byte) int {
	if _, err := namespace().Markdownify(context.Background(), string(data)); err != nil {
		return -1
	}
	return 0
}

// PageParse lexes a content page: front matter detection, shortcode lexing,
// and decoding of the front matter in its own format.
func PageParse(data []byte) int {
	_, err1 := pageparser.ParseBytes(data, pageparser.Config{NoFrontMatter: true})
	_, err2 := pageparser.ParseFrontMatterAndContent(bytes.NewReader(data))
	if err1 != nil && err2 != nil {
		return -1
	}
	return 0
}

var dataFormats = []metadecoders.Format{
	metadecoders.JSON, metadecoders.TOML, metadecoders.YAML,
	metadecoders.XML, metadecoders.CSV, metadecoders.ORG,
}

// Decode decodes a data file the way `transform.Unmarshal` and the `data/`
// directory do. The first byte selects the format (0: detect it from the
// content), the rest is the document.
func Decode(data []byte) int {
	if len(data) == 0 {
		return -1
	}
	sel, doc := int(data[0])%(len(dataFormats)+1), data[1:]
	f := metadecoders.Format("")
	if sel == 0 {
		f = metadecoders.Default.FormatFromContentString(string(doc))
		if f == "" {
			return -1
		}
	} else {
		f = dataFormats[sel-1]
	}
	if _, err := metadecoders.Default.Unmarshal(doc, f); err != nil {
		return -1
	}
	return 0
}

var configExts = []string{"toml", "yaml", "json"}

var noExecOnce sync.Once

// Config loads a site configuration file (hugo.toml/yaml/json) from an
// in-memory file system. The first byte selects the format.
//
// A config can import modules (`module.imports`, `theme`), and Hugo resolves
// those by running `go mod download` / `go list`, which would fork a process,
// reach the network and write into GOMODCACHE for an input. PATH is emptied
// once, before the first load, so Hugo finds no `go` binary and fails the
// module load with an error instead of exec'ing; the loader's own
// config-parsing and merging code is unaffected. Nothing else in this process
// execs, and PATH is not changed again, so every input still sees the same
// environment.
func Config(data []byte) int {
	if len(data) == 0 {
		return -1
	}
	noExecOnce.Do(func() {
		if err := os.Setenv("PATH", ""); err != nil {
			panic(err)
		}
	})
	ext, body := configExts[int(data[0])%len(configExts)], data[1:]
	fs := afero.NewMemMapFs()
	name := "hugo." + ext
	if err := afero.WriteFile(fs, name, body, 0o644); err != nil {
		panic(err)
	}
	if _, err := allconfig.LoadConfig(allconfig.ConfigSourceDescriptor{
		Fs:       fs,
		Filename: name,
		Environ:  []string{"none"},
		Logger:   loggers.New(loggers.Options{StdOut: io.Discard, StdErr: io.Discard}),
	}); err != nil {
		// LoadConfig wraps every error it returns with %w but turns a recovered
		// panic into an unwrapped fmt.Errorf("failed to load config: %v", r);
		// re-raise those so the fuzzer reports them as crashes.
		if errors.Unwrap(err) == nil {
			panic(err)
		}
		return -1
	}
	return 0
}

var imageFormats = []imagemeta.ImageFormat{
	imagemeta.JPEG, imagemeta.PNG, imagemeta.TIFF, imagemeta.WebP,
	imagemeta.AVIF, imagemeta.HEIF, imagemeta.GIF,
}

// ImageMeta decodes EXIF, IPTC and XMP metadata and the image config the way
// `Resource.Meta` does. The first byte selects the image format.
func ImageMeta(data []byte) int {
	if len(data) == 0 {
		return -1
	}
	format, img := imageFormats[int(data[0])%len(imageFormats)], data[1:]
	d, err := meta.NewDecoder(meta.WithSources("exif", "iptc", "xmp"))
	if err != nil {
		panic(err)
	}
	_, err1 := d.DecodeMeta("fuzz", format, bytes.NewReader(img))
	exif, err := meta.NewDecoder()
	if err != nil {
		panic(err)
	}
	_, err2 := exif.Decode("fuzz", format, bytes.NewReader(img))
	if err1 != nil && err2 != nil {
		return -1
	}
	return 0
}

// highlightLangs are the languages Transform's highlight selector picks from.
// An arbitrary language string sends almost every input into chroma's
// lexers.Analyse content guessing (every lexer's analyser run over the code),
// which ran at about 8 exec/s and held the whole target to 79; a known language
// reaches Hugo's option parsing and that language's lexer and formatter.
var highlightLangs = []string{
	"go", "python", "html", "javascript", "json", "yaml", "c", "rust",
	"sql", "bash", "css", "xml", "diff", "markdown", "toml", "java",
}

// Transform runs the string template functions that take content as input.
// The first byte selects the function.
func Transform(data []byte) int {
	if len(data) == 0 {
		return -1
	}
	sel, s := data[0]%5, string(data[1:])
	ns := namespace()
	var err error
	switch sel {
	case 0:
		_, err = ns.HTMLToMarkdown(context.Background(), s)
	case 1:
		_, err = ns.Plainify(s)
	case 2:
		_, err = ns.Emojify(s)
	case 3:
		// Highlight: the byte after the selector picks the language, the first line
		// of the rest is the options string, the remainder is the code.
		if len(s) == 0 {
			return -1
		}
		lang := highlightLangs[int(s[0])%len(highlightLangs)]
		opts, code, _ := strings.Cut(s[1:], "\n")
		_, err = ns.Highlight(code, lang, opts)
	case 4:
		_, err = ns.XMLEscape(s)
	}
	if err != nil {
		return -1
	}
	return 0
}

var pathParser = &paths.PathParser{
	LanguageIndex: map[string]int{"no": 0, "en": 1, "fr": 2},
	IsContentExt:  func(ext string) bool { return ext == "md" },
	IsOutputFormat: func(name, ext string) bool {
		switch name {
		case "html", "amp", "csv", "rss":
			return true
		}
		return false
	},
	ConfiguredDimensions: sitesmatrix.NewTestingDimensions(
		[]string{"en", "no", "fr"}, []string{"v1", "v2", "v3", "v1.0.0"}, []string{"admin", "editor", "viewer", "guest"}),
}

// Path parses a content/layout file path (language, version, role and output
// format identifiers in the name) and calls every argument-less accessor of
// the result. The first byte selects the component folder, the rest is the path.
// The parser is read-only after construction.
func Path(data []byte) int {
	if len(data) == 0 {
		return -1
	}
	component, s := files.ComponentFolders[int(data[0])%len(files.ComponentFolders)], string(data[1:])
	p := pathParser.Parse(component, s)
	if p == nil {
		return -1
	}
	v := reflect.ValueOf(p)
	for i := 0; i < v.NumMethod(); i++ {
		if m := v.Type().Method(i); m.Type.NumIn() == 1 {
			m.Func.Call([]reflect.Value{v})
		}
	}
	return 0
}

var minifyTypes = []media.Type{
	media.Builtin.CSSType, media.Builtin.JavascriptType, media.Builtin.HTMLType,
	media.Builtin.SVGType, media.Builtin.JSONType, media.Builtin.XMLType,
}

var (
	minifyOnce   sync.Once
	minifyClient minifiers.Client
)

// Minify runs user asset content through the minifiers Hugo uses for
// `resources.Minify` and `--minify` output. The first byte selects the media
// type (CSS, JS, HTML, SVG, JSON, XML), the rest is the document. The client
// is built once with the default config and is stateless afterwards.
func Minify(data []byte) int {
	if len(data) == 0 {
		return -1
	}
	minifyOnce.Do(func() {
		c, err := minifiers.New(media.DefaultTypes, output.DefaultFormats,
			testconfig.GetTestConfig(afero.NewMemMapFs(), nil))
		if err != nil {
			panic(err)
		}
		minifyClient = c
	})
	mt := minifyTypes[int(data[0])%len(minifyTypes)]
	if err := minifyClient.Minify(mt, io.Discard, bytes.NewReader(data[1:])); err != nil {
		return -1
	}
	return 0
}

// Strings drives the hand-written text scanners and sanitizers that take
// content or front matter values: `strings.Truncate` (plain text and HTML,
// with its HTML tag scanner), and the PathSpec URL/path functions behind
// `urlize`, `absURL`, `relURL` and file name sanitizing (URLEscape is not
// called directly: URLize reaches it after sanitizing, but the sanitizer lets
// a `//user@host`-shaped string through and URLEscape panics on it). The first byte
// selects the function; for Truncate the second byte is the length.
func Strings(data []byte) int {
	if len(data) == 0 {
		return -1
	}
	d := SiteDeps()
	sel, rest := int(data[0])%6, data[1:]
	switch sel {
	case 0, 1:
		if len(rest) == 0 {
			return -1
		}
		length, text := int(rest[0]), string(rest[1:])
		var in any = text
		if sel == 1 {
			in = template.HTML(text)
		}
		if _, err := stringsNamespace().Truncate(length, in); err != nil {
			return -1
		}
	case 2:
		d.PathSpec.URLize(string(rest))
	case 3:
		d.PathSpec.MakePathSanitized(string(rest))
	case 4:
		d.PathSpec.AbsURL(string(rest), len(rest)%2 == 0)
	case 5:
		d.PathSpec.RelURL(string(rest), len(rest)%2 == 0)
	}
	return 0
}

var (
	orgOnce sync.Once
	orgConv converter.Provider
)

// Org renders a `.org` content file body through Hugo's Org content converter
// (markup/org, go-org to HTML), the way a site build converts content files.
// The provider is built once from the shared site's configuration, with its
// default highlighter and an empty in-memory content file system (so
// `#+INCLUDE` finds nothing and no host file is read); each Convert call
// builds its own go-org parser state.
func Org(data []byte) int {
	orgOnce.Do(func() {
		conf := SiteDeps().Conf
		p, err := org.Provider.New(converter.ProviderConfig{
			Conf:        conf,
			ContentFs:   afero.NewMemMapFs(),
			Logger:      loggers.New(loggers.Options{StdOut: io.Discard, StdErr: io.Discard}),
			Highlighter: highlight.New(conf.GetConfigSection("markup").(markup_config.Config).Highlight),
		})
		if err != nil {
			panic(err)
		}
		orgConv = p
	})
	conv, err := orgConv.New(converter.DocumentContext{DocumentName: "fuzz.org"})
	if err != nil {
		panic(err)
	}
	if _, err := conv.Convert(converter.RenderContext{Src: data}); err != nil {
		return -1
	}
	return 0
}

var decodeFormats = []images.Format{
	images.JPEG, images.PNG, images.GIF, images.TIFF, images.BMP, images.WEBP, images.AVIF,
}

// noSeek hides the Seek method of a reader, so Codec.DecodeConfig takes its
// read-everything-into-memory path.
type noSeek struct{ io.Reader }

// ImageDecode decodes image pixel data and the image config through Hugo's own
// image codec (resources/images.Codec), the way `resources.Get` and the image
// methods do. The low 7 bits of the first byte select the format (JPEG, PNG, GIF,
// TIFF, BMP, WebP, AVIF) and the top bit is `firstFrameOnly`; the rest is the file.
//
// DecodeConfig runs for every format (WebP and AVIF headers are read by
// imagemeta, not by the WASM codecs). DecodeFormat runs for the formats Hugo
// decodes in Go. WebP and AVIF pixels are decoded by a long-lived WASM module
// whose linear memory and trap state persist across calls, so an input's result
// would depend on earlier ones; they are not decoded here.
func ImageDecode(data []byte) int {
	if len(data) == 0 {
		return -1
	}
	f := decodeFormats[int(data[0]&0x7f)%len(decodeFormats)]
	firstFrameOnly := data[0]&0x80 != 0
	img := data[1:]
	codec := SiteDeps().ResourceSpec.Imaging.Codec

	_, _, err1 := codec.DecodeConfig(f, bytes.NewReader(img))
	_, _, err2 := codec.DecodeConfig(f, noSeek{bytes.NewReader(img)})
	err3 := errors.New("not decoded")
	if f != images.WEBP && f != images.AVIF {
		_, err3 = codec.DecodeFormat(f, bytes.NewReader(img), firstFrameOnly)
	}
	if err1 != nil && err2 != nil && err3 != nil {
		return -1
	}
	return 0
}

var frontMatterFormats = []metadecoders.Format{metadecoders.TOML, metadecoders.YAML, metadecoders.JSON}

const frontMatterSiteConfig = `
defaultContentLanguage = "en"
[frontmatter]
date = [":filename", ":default"]
lastmod = [":default"]
[languages]
[languages.en]
weight = 1
[languages.nn]
weight = 2
[versions]
[versions."v1.0.0"]
[versions."v2.0.0"]
[roles]
[roles.guest]
[roles.member]
`

var (
	frontMatterOnce    sync.Once
	frontMatterConf    config.AllProvider
	frontMatterHandler pagemeta.FrontMatterHandler
	frontMatterLogger  = loggers.New(loggers.Options{StdOut: io.Discard, StdErr: io.Discard})
)

// FrontMatterPage applies a decoded front matter document the way Hugo does
// for a content file and for a content adapter page: it sets the early page
// config, compiles it against the site's languages/versions/roles together with
// the page's `cascade`, handles the dates (including dates in the file name,
// taken from the `slug` field), and decodes `build`, `sitemap` and, for the
// adapter flavor, the whole map into the late page config (slug, url, aliases,
// outputs, ...). It builds fresh per-input objects; the site configuration and
// the date handler are built once and only read afterwards. The error is the
// first one Hugo reported; the late config is returned even then.
func FrontMatterPage(f metadecoders.Format, doc []byte) (*pagemeta.PageConfigEarly, *pagemeta.PageConfigLate, error) {
	frontMatterOnce.Do(func() {
		cfg, err := config.FromConfigString(frontMatterSiteConfig, "toml")
		if err != nil {
			panic(err)
		}
		frontMatterConf = testconfig.GetTestConfig(afero.NewMemMapFs(), cfg)
		frontMatterHandler, err = pagemeta.NewFrontmatterHandler(frontMatterLogger,
			frontMatterConf.GetConfigSection("frontmatter").(pagemeta.FrontmatterConfig))
		if err != nil {
			panic(err)
		}
	})
	conf := frontMatterConf
	dims := conf.ConfiguredDimensions()

	m, err := metadecoders.Default.UnmarshalToMap(doc, f)
	if err != nil {
		return nil, nil, err
	}
	// The content adapter flavor decodes its own copy of the raw map.
	adapterData := maps.Clone(m)

	early := &pagemeta.PageConfigEarly{Kind: kinds.KindPage}
	if err := early.SetMetaPreFromMap("md", m, frontMatterLogger, conf); err != nil {
		return nil, nil, err
	}
	if err := early.Init(false); err != nil {
		return nil, nil, err
	}

	var inherited *page.PageMatcherParamsConfigs
	if cv, found := early.Frontmatter["cascade"]; found {
		if inherited, err = page.DecodeCascadeConfig(cv); err != nil {
			return nil, nil, err
		}
		if err := inherited.InitConfig(frontMatterLogger, dims.CommonSitesMatrix.DefaultSite, dims); err != nil {
			return nil, nil, err
		}
	}
	pi := conf.PathParser().Parse(files.ComponentFolderContent, "/fuzz/index.md")
	if err := early.CompileEarly(pi, inherited, conf, nil, nil, false); err != nil {
		return nil, nil, err
	}
	if cv, found := early.Frontmatter["cascade"]; found {
		own, err := page.DecodeCascadeConfig(cv)
		if err != nil {
			return nil, nil, err
		}
		if err := own.InitConfig(frontMatterLogger, early.SitesMatrix, dims); err != nil {
			return nil, nil, err
		}
	}

	late := &pagemeta.PageConfigLate{Params: maps.Clone(early.Frontmatter)}
	var firstErr error
	if err := frontMatterHandler.HandleDates(&pagemeta.FrontMatterDescriptor{
		PageConfigEarly: early,
		PageConfigLate:  late,
		BaseFilename:    cast.ToString(early.Frontmatter["slug"]),
		PathOrTitle:     "fuzz.md",
		Location:        time.UTC,
	}); err != nil {
		firstErr = err
	}
	if _, err := pagemeta.DecodeBuildConfig(late.Params["build"]); err != nil && firstErr == nil {
		firstErr = err
	}
	if _, err := config.DecodeSitemap(config.SitemapConfig{}, hmaps.ToStringMap(late.Params["sitemap"])); err != nil && firstErr == nil {
		firstErr = err
	}

	adapterEarly := &pagemeta.PageConfigEarly{Kind: kinds.KindPage, IsFromContentAdapter: true}
	adapterLate := &pagemeta.PageConfigLate{ContentAdapterData: adapterData}
	if err := adapterLate.Compile(adapterEarly, frontMatterLogger, output.DefaultFormats); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := frontMatterHandler.HandleDates(&pagemeta.FrontMatterDescriptor{
		PageConfigEarly: adapterEarly,
		PageConfigLate:  adapterLate,
		Location:        time.UTC,
	}); err != nil && firstErr == nil {
		firstErr = err
	}
	return early, late, firstErr
}

// FrontMatter turns a front matter document into page config (see
// FrontMatterPage). The first byte selects the format (TOML, YAML, JSON).
func FrontMatter(data []byte) int {
	if len(data) == 0 {
		return -1
	}
	if _, _, err := FrontMatterPage(frontMatterFormats[int(data[0])%len(frontMatterFormats)], data[1:]); err != nil {
		return -1
	}
	return 0
}
