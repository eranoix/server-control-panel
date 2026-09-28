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
