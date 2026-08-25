package dicomweb

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// A DICOMweb resource is its path, and the variable segments are UIDs the caller
// got from somewhere else — a worklist, a query result, a form. resolve used to
// paste them in as given: url.URL escapes "?" and "#" on the way out, but not
// "/", so a UID with a slash or a ".." in it addressed a different resource and
// the request went out looking entirely normal.
func TestResolveRejectsSegmentsThatChangeTheResource(t *testing.T) {
	t.Parallel()

	c := &Client{BaseURL: "https://pacs.example/dicom-web"}
	for _, uid := range []string{
		"1.2.3/../../../metadata",        // climbs out of the instance
		"../../studies/9.9.9",            // climbs out of the service
		"1.2.3/series/9.9/instances/9.9", // grafts on a whole resource path
		"..",                             // resolved away by the path itself
		".",                              //
		"1.2.3%2f9.9.9",                  // pre-escaped: escaped again, or not, either way not a UID
		"1.2.3?PatientID=*",              // query smuggled into the path
		"1.2.3#frag",                     //
		"1.2.3\n",                        // header-ish junk from a text field
		"",                               // silently dropped, leaving a shorter path
	} {
		got, err := c.resolve("studies", "1.2.3", "series", "1.2.3.4", "instances", uid, "metadata")
		if !errors.Is(err, ErrInvalidPath) {
			t.Errorf("resolve(instance %q) = %q, %v; want ErrInvalidPath", uid, got, err)
		}
	}
}

// The escaping rule has to leave a real UID alone: over-escaping would send
// requests no server answers.
func TestResolveKeepsLegitimatePaths(t *testing.T) {
	t.Parallel()

	c := &Client{BaseURL: "https://pacs.example/dicom-web/"}
	got, err := c.resolve("studies", "1.2.840.113619.2.55.3.604688", "series", "1.2.3.4", "instances", "1.2.3.4.5", "metadata")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := "https://pacs.example/dicom-web/studies/1.2.840.113619.2.55.3.604688/series/1.2.3.4/instances/1.2.3.4.5/metadata"
	if got != want {
		t.Errorf("resolve = %q, want %q", got, want)
	}
}

// The bound has to fail rather than truncate. io.LimitReader reports EOF at the
// limit, and a Part 10 instance cut short still parses as an instance — the
// caller would get a short study and no error at all.
func TestCapReaderFailsInsteadOfTruncating(t *testing.T) {
	t.Parallel()

	body := bytes.Repeat([]byte("x"), 1000)

	// Exactly at the bound is legal.
	got, err := readAll(bytes.NewReader(body), 1000)
	if err != nil {
		t.Fatalf("1000 bytes with a 1000-byte bound: %v", err)
	}
	if len(got) != 1000 {
		t.Fatalf("read %d bytes, want 1000", len(got))
	}

	// One byte over is not.
	got, err = readAll(bytes.NewReader(body), 999)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("1000 bytes with a 999-byte bound: %d bytes, %v; want ErrTooLarge", len(got), err)
	}
	if !strings.Contains(err.Error(), "999") {
		t.Errorf("error %q does not say what the bound was", err)
	}

	// A bound of zero or less means unbounded.
	if got, err = readAll(bytes.NewReader(body), 0); err != nil || len(got) != 1000 {
		t.Errorf("unbounded read: %d bytes, %v", len(got), err)
	}

	// Reading through a small buffer must not trip the bound early.
	r := capReader(bytes.NewReader(body), 1000)
	buf := make([]byte, 7)
	total := 0
	for {
		n, err := r.Read(buf)
		total += n
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("after %d bytes: %v", total, err)
		}
	}
	if total != 1000 {
		t.Errorf("read %d bytes in 7-byte chunks, want 1000", total)
	}
}
