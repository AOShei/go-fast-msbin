// Command gofastmsbin extracts Word, Excel and PowerPoint files in the
// binary formats of Office 97 to 2003 (.doc, .xls, .ppt) into the
// shared document model of the go-fast libraries and prints them as JSON,
// Markdown or plain text.
//
//	gofastmsbin [flags] file-or-directory...
//
// A directory stands for the Office files in it and below it. With one
// input the JSON is indented; with several, each document is one line.
// JSON is written a part at a time, as the parts are read, so that what
// is held is about one part (a sheet, a slide, a Word document's body);
// the bytes are those of the document read whole. Should reading stop
// partway through a file (an interrupt), what was written of it is cut
// short and the command says so. Only the documents go to standard
// output: everything said about them goes to standard error, and the exit
// status is 1 if any input could not be read.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

// The extensions of the files this command picks out of a directory.
var extensions = map[string]bool{
	".doc": true, ".dot": true,
	".xls": true, ".xlt": true, ".xla": true,
	".ppt": true, ".pps": true, ".pot": true,
}

// run is the command: it reads args and writes documents to stdout and
// messages to stderr, and returns the exit status.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gofastmsbin", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var (
		format   = flags.String("format", "json", "what to print: json, markdown or text")
		output   = flags.String("o", "", "write to this file instead of standard output")
		parts    = flags.String("parts", "", "parts to read, by number: 3, or 2-5 (the body of a Word document is part 1; sheets and slides count from 1)")
		images   = flags.String("images", "meta", "pictures: meta (a block for each, with its place, size and alternative text), data (their bytes too) or none")
		cells    = flags.Bool("cells", false, "report what the cells of a workbook hold besides what they show: formulas, types and stored values")
		spans    = flags.Bool("spans", false, "report bold, italic and the like, and links, as spans")
		workers  = flags.Int("workers", 0, "sheets or slides read at a time (0: one for each CPU)")
		password = flags.String("password", "", "password of an encrypted file, and of encrypted files embedded in it")
		maxCells = flags.Int64("max-cells", 0, "cells kept from a workbook (0: the default, 20 million; -1: no limit)")
		maxBytes = flags.Int64("max-decoded-bytes", 0, "bytes a file and the files embedded in it may be expanded to: macro code, compressed pictures, embedded files written out (0: the default, 2 GiB; -1: no limit)")
		maxText  = flags.Int64("max-text-bytes", 0, "bytes of text a document and the files embedded in it may hold (0: the default, 256 MiB; -1: no limit)")
		quiet    = flags.Bool("quiet", false, "say nothing about files that were read; failures are still reported")
		version  = flags.Bool("version", false, "print the version and exit")
	)
	// The render flags shared by every go-fast command, defined by core.
	renderFlags := gofastcore.AddRenderFlags(flags)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "usage: gofastmsbin [flags] file-or-directory...")
		fmt.Fprintln(stderr, "Extracts Word, Excel and PowerPoint files in the binary formats (.doc, .xls, .ppt). A directory stands for such files in it and below it.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *version {
		fmt.Fprintln(stdout, "gofastmsbin", moduleVersion())
		return 0
	}
	fail := func(format string, args ...any) int {
		fmt.Fprintf(stderr, "gofastmsbin: "+format+"\n", args...)
		return 2
	}
	if flags.NArg() == 0 {
		flags.Usage()
		return 2
	}
	if *format != "json" && *format != "markdown" && *format != "text" {
		return fail("unknown format %q: json, markdown or text", *format)
	}
	opts := gofastcore.Options{Workers: *workers, Spans: *spans, Cells: *cells, Password: *password}
	opts.Limits.MaxDecodedBytes, opts.Limits.MaxTextBytes = *maxBytes, *maxText
	switch *images {
	case "none":
		opts.Images = gofastcore.ImagesNone
	case "meta":
		opts.Images = gofastcore.ImagesMeta
	case "data":
		opts.Images = gofastcore.ImagesData
	default:
		return fail("unknown value %q for -images: none, meta or data", *images)
	}
	if *parts != "" {
		first, last, ok := strings.Cut(*parts, "-")
		if !ok {
			last = first
		}
		a, err1 := strconv.Atoi(strings.TrimSpace(first))
		b, err2 := strconv.Atoi(strings.TrimSpace(last))
		if err1 != nil || err2 != nil || a < 1 || b < a {
			return fail("-parts wants a number or a range such as 2-5, not %q", *parts)
		}
		opts.Parts = gofastcore.PartRange{First: a, Last: b}
	}
	extractor := &gofastmsbin.Extractor{MaxCells: *maxCells}
	// A file embedded in a document is read too if it is of these formats.
	opts.Child = func(ctx context.Context, name, mime string, r io.ReaderAt, size int64, o *gofastcore.Options) (*gofastcore.Document, error) {
		return extractor.Extract(ctx, r, size, o)
	}

	// The inputs: the files named, and the Office files in the
	// directories named.
	var inputs []string
	status := 0
	for _, arg := range flags.Args() {
		info, err := os.Stat(arg)
		if err != nil {
			fmt.Fprintf(stderr, "gofastmsbin: %v\n", err)
			status = 1
			continue
		}
		if !info.IsDir() {
			inputs = append(inputs, arg)
			continue
		}
		var found []string
		err = filepath.WalkDir(arg, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				fmt.Fprintf(stderr, "gofastmsbin: %v\n", err)
				status = 1
				return nil
			}
			// Office keeps a lock file named "~$..." beside an open file.
			if !d.IsDir() && extensions[strings.ToLower(filepath.Ext(path))] && !strings.HasPrefix(d.Name(), "~$") {
				found = append(found, path)
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(stderr, "gofastmsbin: %v\n", err)
			status = 1
		}
		sort.Strings(found)
		inputs = append(inputs, found...)
	}

	out := stdout
	if *output != "" {
		file, err := os.Create(*output)
		if err != nil {
			fmt.Fprintf(stderr, "gofastmsbin: %v\n", err)
			return 1
		}
		defer file.Close()
		out = file
	}
	w := bufio.NewWriter(out)
	defer w.Flush()
	render := &gofastcore.RenderOptions{}
	renderFlags.Apply(render)

	written := 0
	for _, path := range inputs {
		if ctx.Err() != nil {
			fmt.Fprintln(stderr, "gofastmsbin: interrupted")
			return 1
		}
		o := opts
		o.Name = filepath.Base(path)
		if *format == "json" {
			// JSON is written a part at a time, as the parts are read.
			warnings, started, err := streamFile(ctx, extractor, path, &o, w, len(inputs) == 1)
			if err != nil {
				if started {
					// What was written of the document is cut short.
					w.Flush()
					fmt.Fprintf(stderr, "%s: %v; the output for it is cut short\n", path, err)
					return 1
				}
				fmt.Fprintf(stderr, "%s: %v\n", path, err)
				status = 1
				continue
			}
			if !*quiet {
				if note := remarks(warnings); note != "" {
					fmt.Fprintf(stderr, "%s: %s\n", path, note)
				}
			}
			if err := w.Flush(); err != nil {
				fmt.Fprintf(stderr, "gofastmsbin: writing %s: %v\n", path, err)
				return 1
			}
			written++
			continue
		}
		doc, err := extractor.ExtractFile(ctx, path, &o)
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", path, err)
			status = 1
			continue
		}
		if !*quiet {
			var all []gofastcore.Warning
			for _, w := range doc.AllWarnings() {
				all = append(all, w)
			}
			if note := remarks(all); note != "" {
				fmt.Fprintf(stderr, "%s: %s\n", path, note)
			}
		}
		switch *format {
		case "markdown":
			if len(inputs) > 1 {
				if written > 0 {
					fmt.Fprintln(w)
				}
				fmt.Fprintf(w, "<!-- %s -->\n\n", strings.ReplaceAll(path, "--", "- -"))
			}
			if err = doc.WriteMarkdown(w, render); err == nil {
				_, err = fmt.Fprintln(w)
			}
		default:
			if len(inputs) > 1 {
				if written > 0 {
					fmt.Fprintln(w)
				}
				fmt.Fprintf(w, "==> %s <==\n", path)
			}
			if err = doc.WriteText(w, render); err == nil {
				_, err = fmt.Fprintln(w)
			}
		}
		if err == nil {
			err = w.Flush()
		}
		if err != nil {
			fmt.Fprintf(stderr, "gofastmsbin: writing %s: %v\n", path, err)
			return 1
		}
		written++
	}
	return status
}

// streamFile opens the file at path and writes it as JSON a part at a
// time (streamJSON).
func streamFile(ctx context.Context, e *gofastmsbin.Extractor, path string, o *gofastcore.Options, w *bufio.Writer, indent bool) ([]gofastcore.Warning, bool, error) {
	f, err := e.Open(path, o)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	return streamJSON(ctx, f, w, indent)
}

// remarks sums up what a document's warnings, its own and its parts',
// say that a person running the command would want to know without
// reading the JSON: anything of severity error.
func remarks(warnings []gofastcore.Warning) string {
	counts := map[string]int{}
	for _, w := range warnings {
		if w.Severity == gofastcore.SeverityError {
			counts[w.Code]++
		}
	}
	if len(counts) == 0 {
		return ""
	}
	codes := make([]string, 0, len(counts))
	for code := range counts {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for i, code := range codes {
		if n := counts[code]; n > 1 {
			codes[i] = fmt.Sprintf("%s (%d)", code, n)
		}
	}
	return "warnings: " + strings.Join(codes, ", ")
}

// moduleVersion is the version this command was built from.
func moduleVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}
