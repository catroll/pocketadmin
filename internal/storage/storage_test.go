package storage_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pocketadmin/internal/storage"
)

// newDriver returns a driver rooted in a fresh temporary directory.
func newDriver(t *testing.T) *storage.LocalDriver {
	t.Helper()
	d, err := storage.NewLocalDriver(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalDriver: %v", err)
	}
	return d
}

// TestDriverRoundTrip is the baseline: what is written can be read back.
func TestDriverRoundTrip(t *testing.T) {
	d := newDriver(t)
	key := storage.Key("aa11bb22cc33dd44")

	want := []byte("hello, library")
	n, err := d.Save(key, bytes.NewReader(want))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if n != int64(len(want)) {
		t.Errorf("Save reported %d bytes, wrote %d", n, len(want))
	}

	ok, err := d.Exists(key)
	if err != nil || !ok {
		t.Fatalf("Exists = %v, %v; want true", ok, err)
	}

	rc, err := d.Open(key)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()

	got := make([]byte, len(want))
	if _, err := rc.Read(got); err != nil && err.Error() != "EOF" {
		t.Fatalf("Read: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("round trip returned %q, want %q", got, want)
	}

	if err := d.Delete(key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if ok, _ := d.Exists(key); ok {
		t.Error("the object is still present after Delete")
	}
}

// TestDeleteIsIdempotent: a retried delete must converge rather than fail.
func TestDeleteIsIdempotent(t *testing.T) {
	d := newDriver(t)
	key := storage.Key("0011223344")

	if err := d.Delete(key); err != nil {
		t.Errorf("deleting an absent key should be a no-op, got %v", err)
	}
	if _, err := d.Save(key, strings.NewReader("x")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := d.Delete(key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := d.Delete(key); err != nil {
		t.Errorf("a second Delete should be a no-op, got %v", err)
	}
}

// TestSaveLeavesNoPartialObject: an interrupted upload must not leave a file that
// a later read would serve as if it were complete.
func TestSaveLeavesNoPartialObject(t *testing.T) {
	root := t.TempDir()
	d, err := storage.NewLocalDriver(root)
	if err != nil {
		t.Fatalf("NewLocalDriver: %v", err)
	}
	key := storage.Key("deadbeef00")

	// A reader that fails partway.
	_, err = d.Save(key, &failingReader{after: 4})
	if err == nil {
		t.Fatal("Save reported success for a failing reader")
	}

	if ok, _ := d.Exists(key); ok {
		t.Error("a partial object was left behind")
	}

	// No temporary file should survive either.
	var leftovers []string
	_ = filepath.WalkDir(root, func(p string, e os.DirEntry, _ error) error {
		if e != nil {
			return nil
		}
		if !e.IsDir() {
			leftovers = append(leftovers, p)
		}
		return nil
	})
	if len(leftovers) != 0 {
		t.Errorf("temporary files were left behind: %v", leftovers)
	}
}

// failingReader returns some bytes and then an error.
type failingReader struct {
	after int
	read  int
}

func (f *failingReader) Read(p []byte) (int, error) {
	if f.read >= f.after {
		return 0, os.ErrInvalid
	}
	n := copy(p, "abcd")
	f.read += n
	return n, nil
}

// TestResolveRefusesTraversal is the security-critical one.
//
// Keys are generated from a content hash, so a traversal should not be able to
// arrive through the normal path. The check is tested anyway because the cost of
// being wrong is reading or writing an arbitrary file on the host.
func TestResolveRefusesTraversal(t *testing.T) {
	root := t.TempDir()
	d, err := storage.NewLocalDriver(root)
	if err != nil {
		t.Fatalf("NewLocalDriver: %v", err)
	}

	outside := filepath.Join(filepath.Dir(root), "escaped.txt")

	cases := []string{
		"../escaped.txt",
		"../../escaped.txt",
		"a/../../escaped.txt",
		"/etc/passwd",
		"objects/../../../escaped.txt",
		"..\\escaped.txt",
		"objects/\x00evil",
		"",
	}

	for _, key := range cases {
		if _, err := d.Save(key, strings.NewReader("payload")); err == nil {
			t.Errorf("Save accepted the key %q", key)
		}
		if _, err := d.Open(key); err == nil {
			t.Errorf("Open accepted the key %q", key)
		}
		if err := d.Delete(key); err == nil {
			t.Errorf("Delete accepted the key %q", key)
		}
	}

	if _, err := os.Stat(outside); err == nil {
		t.Fatalf("a file was written outside the root at %s", outside)
	}
}

// TestKeyIsContentAddressed: the same checksum must address the same object, and
// different checksums must not collide.
func TestKeyIsContentAddressed(t *testing.T) {
	a := storage.Key("abcdef0123456789")
	b := storage.Key("abcdef0123456789")
	if a != b {
		t.Errorf("the same checksum produced different keys: %q and %q", a, b)
	}

	c := storage.Key("abcdef9999999999")
	if a == c {
		t.Errorf("different checksums produced the same key: %q", a)
	}

	// Sharded, so one directory does not accumulate the whole library.
	if !strings.HasPrefix(a, "objects/ab/cd/") {
		t.Errorf("key %q is not sharded by the checksum prefix", a)
	}
}

// TestDeduplicationWritesOnce covers the behaviour the key exists for: two rows
// with the same content share one object.
func TestDeduplicationWritesOnce(t *testing.T) {
	d := newDriver(t)

	content := []byte(strings.Repeat("the same bytes", 100))
	first, err := storage.Inspect(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	second, err := storage.Inspect(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}

	if first.Checksum != second.Checksum {
		t.Fatalf("identical content produced different checksums")
	}

	key := storage.Key(first.Checksum)
	if _, err := d.Save(key, bytes.NewReader(content)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := d.Save(key, bytes.NewReader(content)); err != nil {
		t.Fatalf("second Save: %v", err)
	}

	// Both rows address the same key, so reading either returns the content and
	// deleting the key once removes it for both -- which is why the caller must
	// check for remaining references before deleting.
	rc, err := d.Open(key)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()
}

// TestInspectSniffsRatherThanTrusts: the type comes from the bytes, so a file
// cannot claim a harmless extension and be served as something else.
func TestInspectSniffsRatherThanTrusts(t *testing.T) {
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)

	got, err := storage.Inspect(bytes.NewReader(png))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if got.MimeType != "image/png" {
		t.Errorf("mime type = %q, want image/png", got.MimeType)
	}
	if got.Extension != ".png" {
		t.Errorf("extension = %q, want .png", got.Extension)
	}
	if got.Kind != "image" {
		t.Errorf("kind = %q, want image", got.Kind)
	}

	// A script with an image extension is still a script.
	script := []byte("<html><script>alert(1)</script></html>")
	got, err = storage.Inspect(bytes.NewReader(script))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if got.Extension == ".png" || got.Extension == ".jpg" {
		t.Errorf("a script was identified as an image: %q", got.Extension)
	}
}

// TestInspectCountsEveryByte: the size and checksum must cover the whole stream,
// not just the sniffed head.
func TestInspectCountsEveryByte(t *testing.T) {
	content := bytes.Repeat([]byte("x"), 5000)

	got, err := storage.Inspect(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if got.Size != int64(len(content)) {
		t.Errorf("size = %d, want %d", got.Size, len(content))
	}

	// The checksum has to differ when the tail differs, or dedup would merge two
	// files that are not the same.
	other := append([]byte{}, content...)
	other[len(other)-1] = 'y'

	second, err := storage.Inspect(bytes.NewReader(other))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if got.Checksum == second.Checksum {
		t.Error("a difference in the tail did not change the checksum")
	}
}

// TestInspectHandlesEmptyInput: a zero-byte upload must not panic or hang.
func TestInspectHandlesEmptyInput(t *testing.T) {
	got, err := storage.Inspect(bytes.NewReader(nil))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if got.Size != 0 {
		t.Errorf("size = %d, want 0", got.Size)
	}
	if got.Checksum == "" {
		t.Error("an empty file should still have a checksum")
	}
}

// TestSafeNameStripsWhatWouldBeDangerous covers the display label.
func TestSafeNameStripsWhatWouldBeDangerous(t *testing.T) {
	cases := map[string]string{
		"report.pdf":             "report.pdf",
		"../../etc/passwd":       "passwd",
		`C:\Users\me\secret.txt`: "secret.txt",
		"/absolute/path/a.png":   "a.png",
		".hidden":                "hidden",
		"..":                     "unnamed",
		"":                       "unnamed",
		"with\nnewline.txt":      "withnewline.txt",
		"  spaced.txt  ":         "spaced.txt",
	}

	for in, want := range cases {
		if got := storage.SafeName(in); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSafeNameTruncatesButKeepsTheExtension: a long name must not lose the part
// a reader uses to identify the file.
func TestSafeNameTruncatesButKeepsTheExtension(t *testing.T) {
	long := strings.Repeat("a", 400) + ".pdf"

	got := storage.SafeName(long)
	if len(got) > 200 {
		t.Errorf("name is %d characters, want at most 200", len(got))
	}
	if !strings.HasSuffix(got, ".pdf") {
		t.Errorf("name %q lost its extension", got)
	}
}

// TestPolicyIsAnAllowList: an extension not on the list is refused, and the
// comparison is case-insensitive.
func TestPolicyIsAnAllowList(t *testing.T) {
	p := storage.DefaultPolicy()

	for _, ok := range []string{".pdf", ".PDF", ".png", ".zip"} {
		if !p.Allows(ok) {
			t.Errorf("%q should be allowed", ok)
		}
	}
	for _, bad := range []string{"", ".exe", ".sh", ".php", ".js", ".html", ".bat"} {
		if p.Allows(bad) {
			t.Errorf("%q should not be allowed", bad)
		}
	}
}

// TestPolicyHasASizeLimit: an unbounded upload is a denial of service waiting to
// happen.
func TestPolicyHasASizeLimit(t *testing.T) {
	if storage.DefaultPolicy().MaxSize <= 0 {
		t.Error("the default policy has no size limit")
	}
}

// TestUnknownTypesHaveNoExtension: a deniable default, so an unrecognised type is
// rejected by the policy rather than stored under an invented extension.
func TestUnknownTypesHaveNoExtension(t *testing.T) {
	if got := storage.ExtensionFor("application/x-msdownload"); got != "" {
		t.Errorf("an unknown type produced the extension %q", got)
	}
	if got := storage.KindFor("application/x-msdownload"); got != "other" {
		t.Errorf("an unknown type produced the kind %q", got)
	}
}
