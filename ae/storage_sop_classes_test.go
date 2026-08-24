package ae_test

import (
	"errors"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
)

// The list used to be 170 UID strings whose names lived only in trailing
// comments, so a name could say one thing while the UID next to it meant
// another and nothing would notice. The entries are godicom/uid constants now,
// which makes a wrong name a compile error; what is left to check is that no
// entry names something other than a Storage SOP Class, and that none repeats.
func TestAllStorageSOPClassesAreDistinctStorageClasses(t *testing.T) {
	t.Parallel()

	seen := make(map[string]int, len(ae.AllStorageSOPClasses))
	for i, u := range ae.AllStorageSOPClasses {
		if err := uid.Validate(u); err != nil {
			t.Errorf("entry %d (%q): %v", i, u, err)
			continue
		}
		if first, dup := seen[u]; dup {
			t.Errorf("entries %d and %d are both %s", first, i, u)
			continue
		}
		seen[u] = i

		info, known := uid.Known[uid.UID(u)]
		if !known {
			// One of the two godicom does not name yet; the test below tracks those.
			continue
		}
		if info.Type != "SOP Class" {
			t.Errorf("entry %d %s is a %s (%q), not a SOP Class", i, u, info.Type, info.Name)
		}
		if !strings.Contains(info.Name, "Storage") {
			t.Errorf("entry %d %s is %q, which is not a Storage SOP Class", i, u, info.Name)
		}
	}
}

// The set is a copy of pynetdicom's, and the file has always said so in a
// comment addressed to whoever bumps the submodule. This checks it instead:
// where the submodule is checked out — CI does, with submodules: recursive —
// the two sets must agree.
func TestAllStorageSOPClassesMatchPynetdicom(t *testing.T) {
	t.Parallel()

	const path = "../pynetdicom/pynetdicom/sop_class.py"
	src, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		t.Skipf("%s is not checked out (git submodule update --init)", path)
	}
	if err != nil {
		t.Fatal(err)
	}

	want := parseStorageClasses(t, string(src))
	have := make(map[string]bool, len(ae.AllStorageSOPClasses))
	for _, u := range ae.AllStorageSOPClasses {
		have[u] = true
	}

	for u, keyword := range want {
		if !have[u] {
			t.Errorf("pynetdicom has %s (%s), AllStorageSOPClasses does not", keyword, u)
		}
	}
	for u := range have {
		if _, ok := want[u]; !ok {
			t.Errorf("AllStorageSOPClasses has %s, pynetdicom's _STORAGE_CLASSES does not", u)
		}
	}
}

// parseStorageClasses reads pynetdicom's _STORAGE_CLASSES dict, keyed by UID.
func parseStorageClasses(t *testing.T, src string) map[string]string {
	t.Helper()

	const marker = "\n_STORAGE_CLASSES = {"
	start := strings.Index(src, marker)
	if start < 0 {
		t.Fatal("_STORAGE_CLASSES not found: pynetdicom's sop_class.py changed shape")
	}
	body := src[start+len(marker):]
	if end := strings.Index(body, "\n}"); end >= 0 {
		body = body[:end]
	}

	entry := regexp.MustCompile(`"(\w+)":\s*"([\d.]+)"`)
	out := make(map[string]string)
	for _, m := range entry.FindAllStringSubmatch(body, -1) {
		out[m[2]] = m[1]
	}
	if len(out) == 0 {
		t.Fatal("_STORAGE_CLASSES parsed as empty: pynetdicom's sop_class.py changed shape")
	}
	return out
}

// Two Storage SOP Classes are written out here because godicom's dictionary does
// not name them. That is a gap in the dependency, not a permanent arrangement,
// so this test says when it closes.
func TestStorageSOPClassesGodicomDoesNotName(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ keyword, value string }{
		{"WaveformPresentationStateStorage", ae.WaveformPresentationStateStorage},
		{"WaveformAcquisitionPresentationStateStorage", ae.WaveformAcquisitionPresentationStateStorage},
	} {
		if !slices.Contains(ae.AllStorageSOPClasses, c.value) {
			t.Errorf("%s (%s) is missing from AllStorageSOPClasses", c.keyword, c.value)
		}
		got, ok := uid.Lookup(c.keyword)
		if !ok {
			continue
		}
		if string(got) != c.value {
			t.Errorf("godicom names %s %s, we have %s", c.keyword, got, c.value)
			continue
		}
		t.Errorf("godicom's dictionary now has %s: drop ae.%s and use uid.%s",
			c.keyword, c.keyword, c.keyword)
	}
}
