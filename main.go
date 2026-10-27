// Command tfmodelgen generates Terraform Plugin Framework models, schemas and
// conversion helpers from the response of an OpenAPI 3.x operation.
//
// Usage:
//
//	tfmodelgen -spec openapi.json -path /projects/{id} [flags]
//	tfmodelgen -spec openapi.json            # lists the GET operations
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/adejoux/tfmodelgen/internal/gen"
	"github.com/adejoux/tfmodelgen/internal/spec"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tfmodelgen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, `Usage: tfmodelgen -spec FILE -path PATH [flags]

Generates, from the JSON response of an OpenAPI 3.x operation:
  - Go structs decoding the API response, with nil-safe getters and enum
    constants,
  - request helpers building the path and the query (<Name>Path, <Name>Query),
  - a Terraform Plugin Framework model, embeddable in a resource model, with
    its schema attributes and a FromAPI conversion. -attrs restricts it to
    the properties the provider exposes, such as status.

Without -path, lists the operations of -method.

Flags:
`)
		fs.PrintDefaults()
	}
	var (
		specPath  = fs.String("spec", "", "OpenAPI 3.x document, JSON (required)")
		path      = fs.String("path", "", "operation path, as written in the document, e.g. /projects/{id}")
		method    = fs.String("method", "GET", "HTTP method of the operation")
		status    = fs.String("status", "", "response status to use (default: 200, then the first 2xx, then default)")
		name      = fs.String("name", "", "Go base name of the generated types (default: derived from operationId)")
		pkg       = fs.String("package", "provider", "Go package of the generated file")
		out       = fs.String("out", ".", "output directory, or - for standard output")
		file      = fs.String("file", "", "output file name (default: <name>_model_gen.go)")
		schema    = fs.String("schema", "resource", "schema attributes to generate: resource, datasource, both or none")
		attrs     = fs.String("attrs", "", "comma-separated root response properties exposed as Terraform attributes, e.g. status (default: all)")
		itemsAttr = fs.String("items-attr", "items", "attribute holding a response that is not an object, e.g. an array")
	)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *specPath == "" {
		fmt.Fprintln(stderr, "tfmodelgen: -spec is required")
		fs.Usage()
		return 2
	}

	doc, err := spec.Load(*specPath)
	if err != nil {
		fmt.Fprintf(stderr, "tfmodelgen: %v\n", err)
		return 1
	}

	if *path == "" {
		ops := doc.Operations(*method)
		if len(ops) == 0 {
			fmt.Fprintf(stderr, "tfmodelgen: no %s operation in %s\n", strings.ToUpper(*method), *specPath)
			return 1
		}
		for _, op := range ops {
			line := fmt.Sprintf("%s %s", op.Method, op.Path)
			if op.Operation.OperationID != "" {
				line += "  (" + op.Operation.OperationID + ")"
			}
			if op.Operation.Summary != "" {
				line += "  " + op.Operation.Summary
			}
			fmt.Fprintln(stdout, line)
		}
		return 0
	}

	ep, err := gen.Build(doc, gen.Options{
		Path:      *path,
		Method:    *method,
		Status:    *status,
		Name:      *name,
		ItemsAttr: *itemsAttr,
		Attrs:     strings.Split(*attrs, ","),
	})
	if err != nil {
		fmt.Fprintf(stderr, "tfmodelgen: %v\n", err)
		return 1
	}
	for _, w := range ep.Warnings {
		fmt.Fprintf(stderr, "tfmodelgen: warning: %s\n", w)
	}

	src, err := gen.Emit(ep, gen.EmitOptions{
		Package:  *pkg,
		SpecName: filepath.Base(*specPath),
		Schema:   gen.SchemaKind(*schema),
	})
	if err != nil {
		fmt.Fprintf(stderr, "tfmodelgen: %v\n", err)
		return 1
	}

	if *out == "-" {
		_, err := stdout.Write(src)
		if err != nil {
			fmt.Fprintf(stderr, "tfmodelgen: %v\n", err)
			return 1
		}
		return 0
	}
	if *file == "" {
		*file = gen.FileName(ep.Root.Name) + "_model_gen.go"
	}
	target := filepath.Join(*out, *file)
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fmt.Fprintf(stderr, "tfmodelgen: %v\n", err)
		return 1
	}
	if err := os.WriteFile(target, src, 0o644); err != nil {
		fmt.Fprintf(stderr, "tfmodelgen: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s %s -> %s\n", ep.Method, ep.Path, target)
	return 0
}
