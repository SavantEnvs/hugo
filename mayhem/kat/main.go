// Command kat runs known-answer checks against the Hugo code the fuzz targets
// exercise and prints one "KAT <name> ok|FAIL" line per check. It is built
// with cgo so the binary is dynamically linked (mayhem/test.sh relies on that:
// the sabotage check LD_PRELOADs into dynamic executables only).
package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"image"
	"image/png"
	"os"
	"reflect"
	"time"

	"github.com/gohugoio/hugo/config/testconfig"
	"github.com/gohugoio/hugo/markup/converter"
	"github.com/gohugoio/hugo/markup/highlight"
	"github.com/gohugoio/hugo/markup/markup_config"
	"github.com/gohugoio/hugo/markup/org"
	"github.com/gohugoio/hugo/media"
	"github.com/gohugoio/hugo/resources/images"
	"golang.org/x/image/bmp"
	"github.com/gohugoio/hugo/minifiers"
	"github.com/gohugoio/hugo/output"
	tplstrings "github.com/gohugoio/hugo/tpl/strings"
	"github.com/spf13/afero"
	"github.com/gohugoio/hugo/mayhem/fuzzing"
	"github.com/gohugoio/hugo/parser/metadecoders"
	"github.com/gohugoio/hugo/parser/pageparser"
)

var failed int

func check(name string, got, want any) {
	if reflect.DeepEqual(got, want) {
		fmt.Printf("KAT %s ok\n", name)
		return
	}
	failed++
	fmt.Printf("KAT %s FAIL got=%#v want=%#v\n", name, got, want)
}

func must[T any](v T, err error) T {
	if err != nil {
		fmt.Println("KAT setup FAIL:", err)
		os.Exit(1)
	}
	return v
}

func must2[A, B any](a A, b B, err error) (A, B) {
	if err != nil {
		fmt.Println("KAT setup FAIL:", err)
		os.Exit(1)
	}
	return a, b
}

func main() {
	ns := fuzzing.Namespace()
	ctx := context.Background()

	check("markdownify-inline", fmt.Sprint(must(ns.Markdownify(ctx, "**hi** [a](http://x.org)"))),
		`<strong>hi</strong> <a href="http://x.org">a</a>`)
	check("plainify", must(ns.Plainify("<p>a <b>b</b></p>")), "a b\n")
	check("emojify", fmt.Sprint(must(ns.Emojify(":+1:"))), "👍")
	check("html-to-markdown", must(ns.HTMLToMarkdown(ctx, "<h1>T</h1><p>x <em>y</em></p>")), "# T\n\nx *y*")

	fm := must(pageparser.ParseFrontMatterAndContent(bytes.NewReader([]byte("---\ntitle: X\nweight: 3\n---\nbody\n"))))
	check("frontmatter-title", fm.FrontMatter["title"], "X")
	check("frontmatter-weight", fmt.Sprint(fm.FrontMatter["weight"]), "3")
	check("frontmatter-content", string(fm.Content), "body\n")

	check("toml-decode", fmt.Sprint(must(metadecoders.Default.Unmarshal([]byte("a = 1\n[t]\nb = \"s\"\n"), metadecoders.TOML))),
		"map[a:1 t:map[b:s]]")
	check("json-decode", fmt.Sprint(must(metadecoders.Default.Unmarshal([]byte(`{"k":[1,2]}`), metadecoders.JSON))),
		"map[k:[1 2]]")
	_, err := metadecoders.Default.Unmarshal([]byte("{broken"), metadecoders.JSON)
	check("json-decode-rejects-invalid", err != nil, true)

	min := must(minifiers.New(media.DefaultTypes, output.DefaultFormats, testconfig.GetTestConfig(afero.NewMemMapFs(), nil)))
	minify := func(mt media.Type, src string) string {
		var out bytes.Buffer
		if err := min.Minify(mt, &out, bytes.NewReader([]byte(src))); err != nil {
			return "error: " + err.Error()
		}
		return out.String()
	}
	check("minify-css", minify(media.Builtin.CSSType, "body { color: #ffffff; margin: 0px 0px; }\n"), "body{color:#fff;margin:0}")
	check("minify-json", minify(media.Builtin.JSONType, "{ \"a\": [1, 2] }\n"), `{"a":[1,2]}`)

	str := tplstrings.New(fuzzing.SiteDeps())
	check("truncate-text", fmt.Sprint(must(str.Truncate(5, "Hello, world"))), "Hello …")
	check("truncate-html", fmt.Sprint(must(str.Truncate(5, template.HTML("<p>Hello <b>world</b></p>")))), "<p>Hello …</p>")
	check("urlize", fuzzing.SiteDeps().PathSpec.URLize("Vim (text editor)"), "vim-text-editor")

	orgProv := must(org.Provider.New(converter.ProviderConfig{
		Conf:        fuzzing.SiteDeps().Conf,
		ContentFs:   afero.NewMemMapFs(),
		Logger:      fuzzing.SiteDeps().Log,
		Highlighter: highlight.New(fuzzing.SiteDeps().Conf.GetConfigSection("markup").(markup_config.Config).Highlight),
	}))
	orgConv := must(orgProv.New(converter.DocumentContext{DocumentName: "kat.org"}))
	orgRes := must(orgConv.Convert(converter.RenderContext{Src: []byte("* Title\nsome *bold* text\n")}))
	check("org-convert", string(orgRes.Bytes()), "<nav>\n<ul>\n<li><a href=\"#headline-1\">Title</a>\n</li>\n</ul>\n</nav>\n<div id=\"outline-container-headline-1\" class=\"outline-2\">\n<h2 id=\"headline-1\">\nTitle\n</h2>\n<div id=\"outline-text-headline-1\" class=\"outline-text-2\">\n<p>some <strong>bold</strong> text</p>\n</div>\n</div>\n")

	var pngBuf, bmpBuf bytes.Buffer
	src := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	must(0, png.Encode(&pngBuf, src))
	must(0, bmp.Encode(&bmpBuf, src))
	codec := fuzzing.SiteDeps().ResourceSpec.Imaging.Codec
	cfg, name := must2(codec.DecodeConfig(images.PNG, bytes.NewReader(pngBuf.Bytes())))
	check("image-config-png", fmt.Sprintf("%dx%d %s", cfg.Width, cfg.Height, name), "3x2 png")
	bmpImg := must(codec.DecodeFormat(images.BMP, bytes.NewReader(bmpBuf.Bytes()), false))
	check("image-decode-bmp", fmt.Sprintf("%dx%d", bmpImg.Bounds().Dx(), bmpImg.Bounds().Dy()), "3x2")

	_, late, err := fuzzing.FrontMatterPage(metadecoders.TOML, []byte("date = 2018-02-01T10:00:00Z\nlastmod = \"2019-03-04\"\n"))
	check("frontmatter-dates-ok", err == nil, true)
	check("frontmatter-dates", late.Dates.Date.Format(time.RFC3339)+" "+late.Dates.Lastmod.Format(time.RFC3339),
		"2018-02-01T10:00:00Z 2019-03-04T00:00:00Z")
	// Hugo logs an unparsable date and leaves the page date zero.
	_, late, _ = fuzzing.FrontMatterPage(metadecoders.TOML, []byte("date = \"not a date\"\n"))
	check("frontmatter-ignores-bad-date", late.Dates.Date.IsZero(), true)

	if failed > 0 {
		os.Exit(1)
	}
}
