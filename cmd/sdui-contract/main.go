// Command sdui-contract generates contracts/sdui/contract.json from the Go
// types in internal/mobilebff/sdui — never the other way round. Run through
// `make sdui-contract`; it is not part of the binary served in production.
//
// The generated file is committed on purpose: contract_test.go carries a drift
// test that regenerates and compares byte for byte against what is in the repo,
// so a changed component field without running `make sdui-contract` fails
// `go test ./internal/mobilebff/sdui/...` (and therefore CI).
package main

import (
	"log"
	"os"
	"path/filepath"

	"server-control-panel/internal/mobilebff/sdui"
)

const outputPath = "contracts/sdui/contract.json"

func main() {
	contract := sdui.GenerateContract()

	b, err := sdui.MarshalContract(contract)
	if err != nil {
		log.Fatalf("sdui-contract: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		log.Fatalf("sdui-contract: creating the output directory: %v", err)
	}

	if err := os.WriteFile(outputPath, b, 0o644); err != nil {
		log.Fatalf("sdui-contract: writing %s: %v", outputPath, err)
	}

	log.Printf("sdui-contract: %s generated (%d bytes)", outputPath, len(b))
}
