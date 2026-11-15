package ae_test

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
)

// Every entry is a godicom/uid constant, so a wrong name is a compile error.
// What a compiler cannot see is a constant that belongs to no class, a UID that
// appears in two, or a class that is listed but empty -- and those are exactly
// the mistakes a hand-maintained table makes.
func TestServiceClassTableIsWellFormed(t *testing.T) {
	t.Parallel()

	owner := make(map[string]ae.ServiceClass, 256)
	for _, class := range ae.ServiceClasses() {
		if class == ae.ServiceClassUnknown {
			t.Error("ServiceClassUnknown is listed as a class")
			continue
		}
		if class.String() == "Unknown" || class.String() == "" {
			t.Errorf("class %q has no name", class)
		}

		list := class.SOPClasses()
		if len(list) == 0 {
			t.Errorf("%s has no SOP Classes", class)
		}
		for _, u := range list {
			if err := uid.Validate(u); err != nil {
				t.Errorf("%s: %q: %v", class, u, err)
				continue
			}
			if first, dup := owner[u]; dup {
				t.Errorf("%s is claimed by both %s and %s", u, first, class)
				continue
			}
			owner[u] = class

			info, known := uid.Lookup(uid.UID(u))
			if !known {
				t.Errorf("%s: %s has no dictionary entry", class, u)
				continue
			}
			// PS3.6 types the two print management meta SOP Classes separately,
			// and they are what a print SCU proposes, so both types are SOP
			// Classes here. What must not appear is a Service Class UID: those
			// are answered by ServiceClassOf but are not abstract syntaxes, so
			// one in a list is a table error.
			if info.Type != "SOP Class" && info.Type != "Meta SOP Class" {
				t.Errorf("%s: %s is a %s, not a SOP Class", class, u, info.Type)
			}
			if got := ae.ServiceClassOf(u); got != class {
				t.Errorf("%s: ServiceClassOf(%s) = %s", class, u, got)
			}
		}
	}

	if len(owner) < 200 {
		t.Errorf("table has %d UIDs; pynetdicom's has more than 250", len(owner))
	}
}

func TestServiceClassOf(t *testing.T) {
	t.Parallel()

	cases := []struct {
		uid  string
		want ae.ServiceClass
	}{
		{string(uid.CTImageStorage), ae.ServiceClassStorage},
		{string(uid.SecondaryCaptureImageStorage), ae.ServiceClassStorage},
		{string(uid.Verification), ae.ServiceClassVerification},
		{string(uid.PatientRootQueryRetrieveInformationModelFind), ae.ServiceClassQueryRetrieve},
		{string(uid.StudyRootQueryRetrieveInformationModelMove), ae.ServiceClassQueryRetrieve},
		{string(uid.ModalityWorklistInformationModelFind), ae.ServiceClassBasicWorklistManagement},
		{string(uid.StorageCommitmentPushModel), ae.ServiceClassStorageCommitment},
		{string(uid.HangingProtocolStorage), ae.ServiceClassNonPatientObjectStorage},
		{string(uid.UnifiedProcedureStepPush), ae.ServiceClassUnifiedProcedureStep},
		{string(uid.BasicGrayscalePrintManagementMeta), ae.ServiceClassPrintManagement},
		{string(uid.RTBeamsTreatmentRecordStorage), ae.ServiceClassStorage},

		// Service Class UIDs: PS3.6 types them "Service Class", pynetdicom's
		// uid_to_service_class answers them, and so does this.
		{string(uid.Storage), ae.ServiceClassStorage},
		{string(uid.UnifiedWorklistAndProcedureStep), ae.ServiceClassUnifiedProcedureStep},

		// pynetdicom answers its base ServiceClass for the DICOMDIR SOP Class,
		// whose media storage service is not implemented there either.
		{string(uid.MediaStorageDirectoryStorage), ae.ServiceClassUnknown},

		// Not SOP Classes at all.
		{string(uid.ExplicitVRLittleEndian), ae.ServiceClassUnknown},
		{"1.2.826.0.1.3680043.8.498.1", ae.ServiceClassUnknown},
		{"", ae.ServiceClassUnknown},
	}

	for _, c := range cases {
		if got := ae.ServiceClassOf(c.uid); got != c.want {
			t.Errorf("ServiceClassOf(%q) = %s, want %s", c.uid, got, c.want)
		}
	}
}

// SOPClasses hands out a copy. AllStorageSOPClasses is an exported variable and
// the rest of the tables are package state, so a caller that sorts its result --
// the obvious thing to do with a list of UIDs -- must not reorder the answer the
// next caller gets.
func TestServiceClassSOPClassesAreCopies(t *testing.T) {
	t.Parallel()

	got := ae.ServiceClassStorage.SOPClasses()
	if len(got) != len(ae.AllStorageSOPClasses) {
		t.Fatalf("storage class has %d SOP Classes, AllStorageSOPClasses has %d",
			len(got), len(ae.AllStorageSOPClasses))
	}
	for i := range got {
		if got[i] != ae.AllStorageSOPClasses[i] {
			t.Fatalf("entry %d is %s, AllStorageSOPClasses has %s", i, got[i], ae.AllStorageSOPClasses[i])
		}
	}

	got[0] = "1.2.3"
	if again := ae.ServiceClassStorage.SOPClasses(); again[0] == "1.2.3" {
		t.Error("SOPClasses returned the table itself, not a copy")
	}

	classes := ae.ServiceClasses()
	classes[0] = ae.ServiceClassUnknown
	if again := ae.ServiceClasses(); again[0] == ae.ServiceClassUnknown {
		t.Error("ServiceClasses returned the table itself, not a copy")
	}
}

// The table is a copy of pynetdicom's, and the file says so in a comment
// addressed to whoever bumps the submodule. This checks it instead: where the
// submodule is checked out -- CI does, with submodules: recursive -- the two must
// agree UID for UID.
func TestServiceClassesMatchPynetdicom(t *testing.T) {
	t.Parallel()

	const path = "../pynetdicom/pynetdicom/sop_class.py"
	src, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		t.Skipf("%s is not checked out (git submodule update --init)", path)
	}
	if err != nil {
		t.Fatal(err)
	}

	// Which of pynetdicom's dicts feeds which class. A dict that is not named
	// here and holds UIDs fails the test below, which is the point: adding a
	// class upstream has to be noticed.
	want := map[string]ae.ServiceClass{
		"_VERIFICATION_CLASSES":             ae.ServiceClassVerification,
		"_QR_CLASSES":                       ae.ServiceClassQueryRetrieve,
		"_STORAGE_CLASSES":                  ae.ServiceClassStorage,
		"_APPLICATION_EVENT_CLASSES":        ae.ServiceClassApplicationEventLogging,
		"_BASIC_WORKLIST_CLASSES":           ae.ServiceClassBasicWorklistManagement,
		"_COLOR_PALETTE_CLASSES":            ae.ServiceClassColorPaletteQueryRetrieve,
		"_DEFINED_PROCEDURE_CLASSES":        ae.ServiceClassDefinedProcedureProtocolQueryRetrieve,
		"_DISPLAY_SYSTEM_CLASSES":           ae.ServiceClassDisplaySystemManagement,
		"_HANGING_PROTOCOL_CLASSES":         ae.ServiceClassHangingProtocolQueryRetrieve,
		"_IMPLANT_TEMPLATE_CLASSES":         ae.ServiceClassImplantTemplateQueryRetrieve,
		"_INSTANCE_AVAILABILITY_CLASSES":    ae.ServiceClassInstanceAvailabilityNotification,
		"_INVENTORY_CLASSES":                ae.ServiceClassInventoryQueryRetrieve,
		"_MEDIA_CREATION_CLASSES":           ae.ServiceClassMediaCreationManagement,
		"_NON_PATIENT_OBJECT_CLASSES":       ae.ServiceClassNonPatientObjectStorage,
		"_PRINT_MANAGEMENT_CLASSES":         ae.ServiceClassPrintManagement,
		"_PROCEDURE_STEP_CLASSES":           ae.ServiceClassProcedureStep,
		"_PROTOCOL_APPROVAL_CLASSES":        ae.ServiceClassProtocolApprovalQueryRetrieve,
		"_RELEVANT_PATIENT_QUERY_CLASSES":   ae.ServiceClassRelevantPatientInformationQuery,
		"_RT_MACHINE_VERIFICATION_CLASSES":  ae.ServiceClassRTMachineVerification,
		"_STORAGE_COMMITMENT_CLASSES":       ae.ServiceClassStorageCommitment,
		"_STORAGE_MANAGEMENT_CLASSES":       ae.ServiceClassStorageManagement,
		"_SUBSTANCE_ADMINISTRATION_CLASSES": ae.ServiceClassSubstanceAdministrationQuery,
		"_UNIFIED_PROCEDURE_STEP_CLASSES":   ae.ServiceClassUnifiedProcedureStep,
	}

	dicts := parseServiceClassDicts(t, string(src))
	seen := make(map[string]bool, len(want))
	for name, class := range want {
		seen[name] = true
		upstream, ok := dicts[name]
		if !ok {
			t.Errorf("%s is not in sop_class.py any more", name)
			continue
		}

		have := make(map[string]bool, len(upstream))
		for _, u := range class.SOPClasses() {
			have[u] = true
		}
		for u, keyword := range upstream {
			if !have[u] {
				t.Errorf("%s has %s (%s), %s does not", name, keyword, u, class)
			}
		}
		for u := range have {
			if _, ok := upstream[u]; !ok {
				t.Errorf("%s has %s, %s does not", class, u, name)
			}
		}
	}

	for name, entries := range dicts {
		if name == "_MEDIA_STORAGE_CLASSES" {
			// The one dict pynetdicom maps to its base ServiceClass; asserted
			// below instead of being given a class here.
			continue
		}
		if len(entries) > 0 && !seen[name] {
			t.Errorf("sop_class.py has %s with %d UIDs and the table names no class for it", name, len(entries))
		}
	}

	// _MEDIA_STORAGE_CLASSES is the one dict pynetdicom maps to its base
	// ServiceClass, and the two Service Class UIDs are named directly by UID
	// instead of through a dict. Both are deliberate, so both are asserted
	// rather than left to the loop above.
	for u := range dicts["_MEDIA_STORAGE_CLASSES"] {
		if got := ae.ServiceClassOf(u); got != ae.ServiceClassUnknown {
			t.Errorf("ServiceClassOf(%s) = %s; pynetdicom answers its base class", u, got)
		}
	}
	if got := ae.ServiceClassOf(string(uid.Storage)); got != ae.ServiceClassStorage {
		t.Errorf("ServiceClassOf(Storage Service Class) = %s", got)
	}
	if got := ae.ServiceClassOf(string(uid.UnifiedWorklistAndProcedureStep)); got != ae.ServiceClassUnifiedProcedureStep {
		t.Errorf("ServiceClassOf(Unified Worklist and Procedure Step Service Class) = %s", got)
	}
	// A Service Class UID is not an abstract syntax, so it is in no list.
	for _, class := range ae.ServiceClasses() {
		for _, u := range class.SOPClasses() {
			if u == string(uid.Storage) || u == string(uid.UnifiedWorklistAndProcedureStep) {
				t.Errorf("%s lists the Service Class UID %s as a SOP Class", class, u)
			}
		}
	}
}

// parseServiceClassDicts returns every `_X_CLASSES = {"Keyword": "1.2.3"}` dict
// in pynetdicom's sop_class.py, keyed by dict name and then by UID.
func parseServiceClassDicts(t *testing.T, src string) map[string]map[string]string {
	t.Helper()

	block := regexp.MustCompile(`(?ms)^(_[A-Z_]+) = \{\n(.*?)^\}`)
	entry := regexp.MustCompile(`"(\w+)":\s*"([\d.]+)"`)

	// The submodule is a git checkout and core.autocrlf may have converted it;
	// a line-ending difference is not a change in the table.
	src = strings.ReplaceAll(src, "\r\n", "\n")

	out := make(map[string]map[string]string)
	for _, m := range block.FindAllStringSubmatch(src, -1) {
		name, body := m[1], m[2]
		if !strings.HasSuffix(name, "_CLASSES") {
			continue
		}
		entries := make(map[string]string)
		for _, e := range entry.FindAllStringSubmatch(body, -1) {
			entries[e[2]] = e[1]
		}
		out[name] = entries
	}
	if len(out) < 20 {
		t.Fatalf("parsed %d _CLASSES dicts: pynetdicom's sop_class.py changed shape", len(out))
	}
	return out
}
