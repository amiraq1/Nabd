package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func shaHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestVerifyCorpusValidGzipAndStream(t *testing.T) {
	payload := []byte("0061 0062;0;0;0 0;0 1\n0063 0064;1;1;1 1;1 0\n")
	path := writeFile(t, "corpus.txt.gz", gzipBytes(t, payload))

	if err := verifyCorpusSHA(path, shaHex(payload)); err != nil {
		t.Fatalf("valid gzip rejected: %v", err)
	}
	f, kind, err := openCorpus(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if kind != corpusGzip {
		t.Fatalf("kind = %d, want corpusGzip", kind)
	}
	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("stream mismatch: %q", got)
	}
}

func TestVerifyCorpusCorruptGzip(t *testing.T) {
	payload := []byte("0061;0;0;0;0\n")
	valid := gzipBytes(t, payload)

	t.Run("header magic", func(t *testing.T) {
		corrupt := append([]byte(nil), valid...)
		corrupt[0], corrupt[1] = 0x00, 0x00
		path := writeFile(t, "corrupt.txt.gz", corrupt)
		err := verifyCorpusSHA(path, shaHex(payload))
		if !errors.Is(err, errCorpusGzip) {
			t.Fatalf("error = %v, want errCorpusGzip", err)
		}
	})

	t.Run("body byte flip", func(t *testing.T) {
		corrupt := append([]byte(nil), valid...)
		mid := len(corrupt) / 2
		corrupt[mid] ^= 0xFF
		path := writeFile(t, "flip.txt.gz", corrupt)
		err := verifyCorpusSHA(path, shaHex(payload))
		if err == nil {
			t.Fatalf("corrupted gzip accepted")
		}
		if !errors.Is(err, errCorpusGzip) && !errors.Is(err, errCorpusSHA) {
			t.Fatalf("error = %v, want errCorpusGzip or errCorpusSHA", err)
		}
	})
}

func TestVerifyCorpusWrongSHA(t *testing.T) {
	payload := []byte("data")
	path := writeFile(t, "corpus.txt.gz", gzipBytes(t, payload))
	err := verifyCorpusSHA(path, shaHex([]byte("other")))
	if !errors.Is(err, errCorpusSHA) {
		t.Fatalf("error = %v, want errCorpusSHA", err)
	}
}

func TestVerifyCorpusMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.txt.gz")
	if err := verifyCorpusSHA(missing, "00"); !errors.Is(err, errCorpusOpen) {
		t.Fatalf("error = %v, want errCorpusOpen", err)
	}
}

func TestVerifyCorpusEarlyEOF(t *testing.T) {
	payload := bytes.Repeat([]byte("0061 0062;0;0;0 0;0 1\n"), 64)
	valid := gzipBytes(t, payload)
	truncated := valid[:len(valid)/2]
	path := writeFile(t, "truncated.txt.gz", truncated)
	err := verifyCorpusSHA(path, shaHex(payload))
	if !errors.Is(err, errCorpusGzip) {
		t.Fatalf("error = %v, want errCorpusGzip", err)
	}
}

func TestVerifyPlainSmallFiles(t *testing.T) {
	payload := []byte("0028; 0029; o # LEFT PARENTHESIS\n")
	path := writeFile(t, "BidiBrackets.txt", payload)
	if err := verifyCorpusSHA(path, shaHex(payload)); err != nil {
		t.Fatalf("plain file rejected: %v", err)
	}
	f, kind, err := openCorpus(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if kind != corpusPlain {
		t.Fatalf("kind = %d, want corpusPlain", kind)
	}
	if err := verifyCorpusSHA(path, shaHex([]byte("x"))); !errors.Is(err, errCorpusSHA) {
		t.Fatalf("wrong plain sha error = %v", err)
	}
}

func TestThresholdsUnchanged(t *testing.T) {
	if thresholdChar != 91707 || thresholdBidi != 770241 ||
		thresholdBrackets != 1152 || thresholdMirrors != 428 {
		t.Fatalf("conformance thresholds changed: %d/%d/%d/%d",
			thresholdChar, thresholdBidi, thresholdBrackets, thresholdMirrors)
	}
}

func testdataDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join("..", "..", "internal", "rtl", "testdata", "unicode", "17.0.0")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("testdata not present: %v", err)
	}
	return dir
}

func TestRealCorpusVerification(t *testing.T) {
	dir := testdataDir(t)
	for _, c := range []struct{ file, sha string }{
		{corpusCharFile, corpusCharSHA},
		{corpusBidiFile, corpusBidiSHA},
		{corpusBracketsFile, corpusBracketsSHA},
		{corpusMirrorFile, corpusMirrorSHA},
	} {
		if err := verifyCorpusSHA(filepath.Join(dir, c.file), c.sha); err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
	}
}

func TestRealFastGates(t *testing.T) {
	dir := testdataDir(t)
	n, err := gateBidiBrackets(filepath.Join(dir, corpusBracketsFile))
	if err != nil {
		t.Fatalf("bracket gate: %v", err)
	}
	if n < thresholdBrackets {
		t.Fatalf("bracket gate below threshold: %d", n)
	}
	n, err = gateMirroring(filepath.Join(dir, corpusMirrorFile))
	if err != nil {
		t.Fatalf("mirror gate: %v", err)
	}
	if n < thresholdMirrors {
		t.Fatalf("mirror gate below threshold: %d", n)
	}
}
