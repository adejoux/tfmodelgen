package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/adejoux/tfmodelgen/internal/prior"
)

// runPrior implements tfmodelgen prior.
func runPrior(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tfmodelgen prior", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, `Usage: tfmodelgen prior -schema-json FILE -resource TYPE [flags]

Generates, from the schema of a released provider binary (the output of
terraform providers schema -json), for the schema version of one resource:
  - <Name>SchemaV<n>(), the frozen schema to use as PriorSchema,
  - <Name>ModelV<n> and its nested models, decoding a state stored with it,
  - <Name>StateUpgraderV<n>(convert), a resource.StateUpgrader that decodes
    the prior state and stores what convert returns.

The schema JSON only knows "number": -number and -types choose the type of
number attributes in the models (number, int64 or float64).

Without -resource, lists the resources and their schema versions.

Flags:
`)
		fs.PrintDefaults()
	}
	var (
		schemaPath = fs.String("schema-json", "", "output of terraform providers schema -json, or the schema of one resource (required)")
		resource   = fs.String("resource", "", "resource type, e.g. monprov_ma_resource")
		provider   = fs.String("provider", "", "provider source address or name, when several providers define the resource")
		name       = fs.String("name", "", "Go base name of the generated identifiers (default: resource type without the provider prefix)")
		number     = fs.String("number", prior.NumberNumber, "type of number attributes: number, int64 or float64")
		typesFlag  = fs.String("types", "", "number types by attribute path, comma-separated, e.g. ratio=float64,settings.max_items=int64")
		pkg        = fs.String("package", "provider", "Go package of the generated file")
		out        = fs.String("out", ".", "output directory, or - for standard output")
		file       = fs.String("file", "", "output file name (default: <name>_schema_v<n>_gen.go)")
	)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *schemaPath == "" {
		fmt.Fprintln(stderr, "tfmodelgen prior: -schema-json is required")
		fs.Usage()
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintf(stderr, "tfmodelgen prior: %v\n", err)
		return 1
	}

	data, err := os.ReadFile(*schemaPath)
	if err != nil {
		return fail(err)
	}
	resources, err := prior.Load(data)
	if err != nil {
		return fail(err)
	}
	single := len(resources) == 1 && resources[0].Type == ""
	if *resource == "" && !single {
		if len(resources) == 0 {
			return fail(fmt.Errorf("no resource in %s", *schemaPath))
		}
		for _, r := range resources {
			fmt.Fprintf(stdout, "%s  version %d  (%s)\n", r.Type, r.Schema.Version, r.Provider)
		}
		return 0
	}
	r, err := prior.Find(resources, *resource, *provider)
	if err != nil {
		return fail(err)
	}

	types := map[string]string{}
	for _, kv := range strings.Split(*typesFlag, ",") {
		if kv = strings.TrimSpace(kv); kv == "" {
			continue
		}
		path, kind, ok := strings.Cut(kv, "=")
		if !ok {
			return fail(fmt.Errorf("-types: %q is not path=type", kv))
		}
		types[strings.TrimSpace(path)] = strings.TrimSpace(kind)
	}

	opts := prior.Options{Package: *pkg, Name: *name, Source: filepath.Base(*schemaPath), Number: *number, Types: types}
	src, err := prior.Generate(r, opts)
	if err != nil {
		return fail(err)
	}
	if *out == "-" {
		if _, err := stdout.Write(src); err != nil {
			return fail(err)
		}
		return 0
	}
	if *file == "" {
		n := *name
		if n == "" {
			n = prior.DefaultName(r)
		}
		*file = prior.FileName(n, r.Schema.Version)
	}
	target := filepath.Join(*out, *file)
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return fail(err)
	}
	if err := os.WriteFile(target, src, 0o644); err != nil {
		return fail(err)
	}
	fmt.Fprintf(stdout, "%s v%d -> %s\n", r.Type, r.Schema.Version, target)
	return 0
}
