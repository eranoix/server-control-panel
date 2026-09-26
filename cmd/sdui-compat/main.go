// Command sdui-compat freezes the SDUI vocabulary manifest per versionCode of
// an already published Android app, and checks whether the server's current
// contract/fixtures are still compatible with EACH frozen manifest.
//
// Why this exists: the app is distributed through its own F-Droid repository,
// with no forced update — an installed build can sit still for months while the
// server keeps changing. The whole point of SDUI (shipping new UI without an app
// release) is exactly what removes the compile error that would normally catch a
// contract change breaking that old build. This command replaces that compile
// error.
//
// Subcommands:
//
//	sdui-compat freeze -version <versionCode> [-force]
//	  Writes the Contract generated right now to
//	  contracts/sdui/client-support/android-<versionCode>.json. A frozen
//	  manifest describes an app build that already exists out in the world —
//	  overwriting it is falsifying history, which is why freeze refuses to
//	  overwrite without -force (which should essentially never be used).
//
//	sdui-compat check
//	  Loads every contracts/sdui/client-support/android-*.json, runs
//	  Compat(frozen, current-contract) for each, runs FixtureRenderable for
//	  every fixture under contracts/sdui/fixtures/ (including
//	  fixtures/screens/) against each frozen manifest, prints a report grouped
//	  by versionCode and exits != 0 if any Break has Severity==breaking. With
//	  zero frozen manifests it prints "no shipped client versions frozen yet"
//	  and exits 0 — the gate stays inert until the first build is frozen, and
//	  becomes live automatically afterwards.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"server-control-panel/internal/mobilebff/sdui"
)

const (
	clientSupportDir = "contracts/sdui/client-support"
	fixturesRoot     = "contracts/sdui/fixtures"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "freeze":
		runFreeze(os.Args[2:])
	case "check":
		runCheck(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: sdui-compat freeze -version <versionCode> [-force]")
	fmt.Fprintln(os.Stderr, "       sdui-compat check")
}

func runFreeze(args []string) {
	flagSet := flag.NewFlagSet("freeze", flag.ExitOnError)
	version := flagSet.Int("version", 0, "versionCode of the already published Android app")
	force := flagSet.Bool("force", false, "overwrite an already frozen manifest (should almost never be used)")
	_ = flagSet.Parse(args)

	if *version <= 0 {
		fmt.Fprintln(os.Stderr, "sdui-compat freeze: -version is required and must be positive")
		os.Exit(2)
	}

	path := filepath.Join(clientSupportDir, fmt.Sprintf("android-%d.json", *version))
	if _, err := os.Stat(path); err == nil && !*force {
		fmt.Fprintf(os.Stderr, "sdui-compat freeze: %s already exists — a frozen manifest describes an app build that already exists out in the world, and overwriting it forges history. Use -force only if you are absolutely certain.\n", path)
		os.Exit(1)
	}

	contract := sdui.GenerateContract()
	b, err := sdui.MarshalContract(contract)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sdui-compat freeze: %v\n", err)
		os.Exit(1)
	}

	if err := os.MkdirAll(clientSupportDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "sdui-compat freeze: creating %s: %v\n", clientSupportDir, err)
		os.Exit(1)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "sdui-compat freeze: writing %s: %v\n", path, err)
		os.Exit(1)
	}

	fmt.Printf("sdui-compat freeze: %s frozen (%d bytes)\n", path, len(b))
}

func runCheck(_ []string) {
	frozenPaths, err := filepath.Glob(filepath.Join(clientSupportDir, "android-*.json"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "sdui-compat check: listing %s: %v\n", clientSupportDir, err)
		os.Exit(1)
	}
	sort.Strings(frozenPaths)

	if len(frozenPaths) == 0 {
		fmt.Println("sdui-compat check: no shipped client versions frozen yet")
		return
	}

	current := sdui.GenerateContract()

	fixturePaths, err := collectFixturePaths(fixturesRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sdui-compat check: listing fixtures in %s: %v\n", fixturesRoot, err)
		os.Exit(1)
	}

	anyBreaking := false
	for _, fp := range frozenPaths {
		label := strings.TrimSuffix(filepath.Base(fp), ".json")

		frozenBytes, err := os.ReadFile(fp)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sdui-compat check: reading %s: %v\n", fp, err)
			os.Exit(1)
		}
		var frozen sdui.Contract
		if err := json.Unmarshal(frozenBytes, &frozen); err != nil {
			fmt.Fprintf(os.Stderr, "sdui-compat check: %s is not a valid Contract JSON: %v\n", fp, err)
			os.Exit(1)
		}

		var breaks []sdui.Break
		breaks = append(breaks, sdui.Compat(frozen, current)...)
		for _, fxp := range fixturePaths {
			fxBytes, err := os.ReadFile(fxp)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sdui-compat check: reading %s: %v\n", fxp, err)
				os.Exit(1)
			}
			for _, b := range sdui.FixtureRenderable(fxBytes, frozen) {
				b.Detail = fmt.Sprintf("%s: %s", fxp, b.Detail)
				breaks = append(breaks, b)
			}
		}

		if len(breaks) == 0 {
			fmt.Printf("%s: OK — no breakage against the current contract/fixtures\n", label)
			continue
		}

		breakingCount, noteCount := 0, 0
		fmt.Printf("%s:\n", label)
		for _, b := range breaks {
			fmt.Printf("  %s\n", b.String())
			if b.Severity == sdui.SeverityBreaking {
				breakingCount++
			} else {
				noteCount++
			}
		}
		fmt.Printf("  -> %d breaking, %d note(s)\n", breakingCount, noteCount)
		if breakingCount > 0 {
			anyBreaking = true
		}
	}

	if anyBreaking {
		fmt.Println()
		fmt.Println("sdui-compat check: FAILED — at least one frozen manifest (an app already shipped in the field) does not survive the current server change. Fix the field/type pointed out above, or revert the change before merging.")
		os.Exit(1)
	}
}

// collectFixturePaths walks dir recursively (contracts/sdui/fixtures,
// including fixtures/screens/) and returns every *.json in deterministic order
// — without treating a missing directory as silent success beyond the obvious
// "there is nothing here yet" of a first checkout.
//
// Fixtures whose name starts with "unknown-" are EXCLUDED on purpose — the same
// reason TestFixtureRoundTripMatchesRealMarshaller (contract_test.go) already
// excludes them: they carry a "type" (e.g. "gantt") the Go server is
// structurally incapable of emitting (outside the 7 closed types of the
// vocabulary) and exist only to exercise the Kotlin client's TOLERANCE of an
// unknown type, never as a real screen payload. Without this exclusion,
// unknown-critical.json (which carries "critical": true on purpose, to prove the
// "update the app" card) would fail `check` against ANY frozen manifest, for
// ever — a permanently red gate is the same "decoration" failure as a gate that
// always skips, only in the opposite direction.
func collectFixturePaths(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".json") {
			return nil
		}
		if strings.HasPrefix(filepath.Base(path), "unknown-") {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}
