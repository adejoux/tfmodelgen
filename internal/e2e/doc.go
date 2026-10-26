// Package e2e checks that generated code compiles against the Terraform
// Plugin Framework and behaves as expected. The *_model_gen.go files are
// generated from ../../testdata/openapi.json and compared with the generator
// output by the golden test in internal/gen.
package e2e

//go:generate go run ../.. -spec ../../testdata/openapi.json -path /projects/{id} -name Project -package e2e -schema both
//go:generate go run ../.. -spec ../../testdata/openapi.json -path /projects -name Projects -package e2e -schema both
