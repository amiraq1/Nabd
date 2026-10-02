package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestGeneratorDeterminism verifies that the generator produces byte-for-byte
// identical Go source code on every run, free from map iteration nondeterminism,
// and that the generated files on disk (arabic_tables_generated.go and
// arabic_pres_tables.go) match the generator output.
func TestGeneratorDeterminism(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..")
	dataDir := filepath.Join(repoRoot, "internal", "rtl", "testdata", "unicode", "17.0.0")
	unicodeDataFile := filepath.Join(dataDir, "UnicodeData.txt")
	arabicShapingFile := filepath.Join(dataDir, "ArabicShaping.txt")
	outFile := filepath.Join(repoRoot, "internal", "rtl", "arabic_tables_generated.go")

	// 1. Verify inputs exist and match official Unicode 17.0.0 checksums.
	if err := verifySHA256(unicodeDataFile, expectedUnicodeDataSHA256); err != nil {
		t.Fatalf("UnicodeData.txt checksum verification failed: %v", err)
	}
	if err := verifySHA256(arabicShapingFile, expectedArabicShapingSHA256); err != nil {
		t.Fatalf("ArabicShaping.txt checksum verification failed: %v", err)
	}

	// 2. Run derivation and generation: Run 1.
	jt1, jg1, err := parseJoiningData(arabicShapingFile, unicodeDataFile)
	if err != nil {
		t.Fatalf("run 1 parseJoiningData: %v", err)
	}
	forms1, lamAlefs1, missing1, err := deriveForms(unicodeDataFile, jt1)
	if err != nil {
		t.Fatalf("run 1 deriveForms: %v", err)
	}
	src1, err := generate(forms1, lamAlefs1, missing1, jt1, jg1)
	if err != nil {
		t.Fatalf("run 1 generate: %v", err)
	}

	// 3. Run derivation and generation: Run 2.
	jt2, jg2, err := parseJoiningData(arabicShapingFile, unicodeDataFile)
	if err != nil {
		t.Fatalf("run 2 parseJoiningData: %v", err)
	}
	forms2, lamAlefs2, missing2, err := deriveForms(unicodeDataFile, jt2)
	if err != nil {
		t.Fatalf("run 2 deriveForms: %v", err)
	}
	src2, err := generate(forms2, lamAlefs2, missing2, jt2, jg2)
	if err != nil {
		t.Fatalf("run 2 generate: %v", err)
	}

	// 4. Assert byte-for-byte identity between runs.
	if !bytes.Equal(src1, src2) {
		t.Fatalf("generator is nondeterministic: run 1 (%d bytes) != run 2 (%d bytes)", len(src1), len(src2))
	}

	// 5. Assert that the generated file on disk matches run 1 byte-for-byte.
	disk, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("reading %s: %v", outFile, err)
	}
	cleanDisk := bytes.ReplaceAll(disk, []byte("\r\n"), []byte("\n"))
	cleanSrc := bytes.ReplaceAll(src1, []byte("\r\n"), []byte("\n"))
	if !bytes.Equal(cleanDisk, cleanSrc) {
		t.Fatalf("%s is out of date or differs from generator output; run 'go run ./internal/rtl/cmd/genarabic'", outFile)
	}

	// 6. Pres tables: deterministic across runs and byte-for-byte on disk.
	pres1, err := generatePresTables()
	if err != nil {
		t.Fatalf("run 1 generatePresTables: %v", err)
	}
	pres2, err := generatePresTables()
	if err != nil {
		t.Fatalf("run 2 generatePresTables: %v", err)
	}
	if !bytes.Equal(pres1, pres2) {
		t.Fatalf("pres table generator is nondeterministic: run 1 (%d bytes) != run 2 (%d bytes)", len(pres1), len(pres2))
	}
	presFile := filepath.Join(repoRoot, "internal", "rtl", "arabic_pres_tables.go")
	presDisk, err := os.ReadFile(presFile)
	if err != nil {
		t.Fatalf("reading %s: %v", presFile, err)
	}
	cleanPresDisk := bytes.ReplaceAll(presDisk, []byte("\r\n"), []byte("\n"))
	cleanPresSrc := bytes.ReplaceAll(pres1, []byte("\r\n"), []byte("\n"))
	if !bytes.Equal(cleanPresDisk, cleanPresSrc) {
		t.Fatalf("%s is out of date or differs from generator output; run 'go run ./internal/rtl/cmd/genarabic'", presFile)
	}
}
