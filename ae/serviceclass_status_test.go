package ae_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

func TestServiceClassHasStatus(t *testing.T) {
	t.Parallel()

	cases := []struct {
		class ae.ServiceClass
		op    ae.RetrieveOperation
		code  uint16
		want  bool
		why   string
	}{
		// The two assertions pynetdicom's test_service_class.py makes about
		// StorageServiceClass, which is where this started.
		{ae.ServiceClassStorage, "", 0x0000, true, "success is a storage status"},
		{ae.ServiceClassStorage, "", 0x0101, false, "0x0101 is in no table"},

		// pynetdicom stores the annex's ranges as thousands of dict entries.
		{ae.ServiceClassStorage, "", 0xA700, true, "0xA700-0xA7FF, out of resources"},
		{ae.ServiceClassStorage, "", 0xA7FF, true, "the far end of that range"},
		{ae.ServiceClassStorage, "", 0xC123, true, "0xC000-0xCFFF, cannot understand"},
		{ae.ServiceClassStorage, "", 0xB001, false, "not in the storage annex"},
		{ae.ServiceClassStorage, "", 0xFF00, false, "storage has no pending status"},

		// The same code, three operations, three answers.
		{ae.ServiceClassQueryRetrieve, ae.OperationFind, 0xB001, true, "C-FIND: response limit reached"},
		{ae.ServiceClassQueryRetrieve, ae.OperationMove, 0xB001, false, "C-MOVE has no such warning"},
		{ae.ServiceClassQueryRetrieve, ae.OperationMove, 0xA801, true, "C-MOVE: destination unknown"},
		{ae.ServiceClassQueryRetrieve, ae.OperationGet, 0xA801, false, "a C-GET names no destination"},
		{ae.ServiceClassQueryRetrieve, ae.OperationGet, 0xA702, true, "C-GET: unable to perform sub-operations"},
		{ae.ServiceClassQueryRetrieve, ae.OperationFind, 0xA702, false, "C-FIND performs no sub-operations"},
		// No operation named: the union of the three.
		{ae.ServiceClassQueryRetrieve, "", 0xA801, true, "the union covers C-MOVE"},
		{ae.ServiceClassQueryRetrieve, "", 0xB001, true, "the union covers C-FIND"},

		// The worklist is a C-FIND, and pynetdicom answers it with the C-FIND
		// set -- not the Annex K table it defines and never attaches.
		{ae.ServiceClassBasicWorklistManagement, "", 0xFF01, true, "a C-FIND warning"},
		{ae.ServiceClassBasicWorklistManagement, "", 0xB001, true, "QR_FIND_SERVICE_CLASS_STATUS has it, Annex K does not"},

		// Annexes that name their failures one by one do not get the range.
		{ae.ServiceClassRelevantPatientInformationQuery, "", 0xC100, true, "more than one match found"},
		{ae.ServiceClassRelevantPatientInformationQuery, "", 0xC101, false, "not in Annex Q"},
		{ae.ServiceClassRTMachineVerification, "", 0xC112, true, "no such machine verification instance"},
		{ae.ServiceClassRTMachineVerification, "", 0xC100, false, "not in Annex DD"},

		// Non-patient storage inherits the general list, not the storage ranges.
		{ae.ServiceClassNonPatientObjectStorage, "", 0xC000, true, "cannot understand"},
		{ae.ServiceClassNonPatientObjectStorage, "", 0xB000, false, "no coercion warning"},

		{ae.ServiceClassPrintManagement, "", 0xB600, true, "memory allocation not supported"},
		{ae.ServiceClassProcedureStep, "", 0x0001, true, "optional attributes not supported"},

		// Two classes whose upstream table exists and is attached to nothing.
		{ae.ServiceClassMediaCreationManagement, "", 0xC201, false, "pynetdicom leaves the table unused"},
		{ae.ServiceClassStorageManagement, "", 0xB010, false, "pynetdicom leaves the table unused"},

		// An unknown class answers the general list, which is what pynetdicom's
		// base ServiceClass does.
		{ae.ServiceClassUnknown, "", 0x0000, true, "success is always defined"},
		{ae.ServiceClassUnknown, "", 0xA700, false, "the general list has no 0xA7xx"},
		{ae.ServiceClassUnknown, "", 0xFE00, true, "cancel is general"},
	}

	for _, c := range cases {
		if got := c.class.HasStatus(c.op, c.code); got != c.want {
			t.Errorf("%s.HasStatus(%q, 0x%04X) = %v, want %v (%s)",
				c.class, c.op, c.code, got, c.want, c.why)
		}
	}
}

// A private SOP Class is not in any table either; it must be held to the general
// list, which is what pynetdicom's base ServiceClass answers.
func TestUnclassifiedSOPClassAnswersGeneralStatuses(t *testing.T) {
	t.Parallel()

	const private = "1.2.826.0.1.3680043.8.498.1"
	class := ae.ServiceClassOf(private)
	if class != ae.ServiceClassUnknown {
		t.Fatalf("ServiceClassOf(%s) = %s", private, class)
	}
	if !class.HasStatus("", 0x0110) {
		t.Error("a private SOP Class may not answer 0x0110")
	}
	if class.HasStatus("", 0xC000) {
		t.Error("a private SOP Class may answer 0xC000, which no general list has")
	}
}

// The tables are a copy of pynetdicom's status.py, and the file says so in a
// comment addressed to whoever bumps the submodule. This checks it instead: where
// the submodule is checked out -- CI does, with submodules: recursive -- the two
// must agree code for code.
func TestStatusSetsMatchPynetdicom(t *testing.T) {
	t.Parallel()

	const path = "../pynetdicom/pynetdicom/status.py"
	src, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		t.Skipf("%s is not checked out (git submodule update --init)", path)
	}
	if err != nil {
		t.Fatal(err)
	}

	dicts := parseStatusDicts(t, string(src))

	// Which of pynetdicom's dicts feeds which class and operation. The operation
	// is only named where the class's answer depends on it.
	cases := []struct {
		dict  string
		class ae.ServiceClass
		op    ae.RetrieveOperation
	}{
		{"GENERAL_STATUS", ae.ServiceClassVerification, ""},
		{"STORAGE_SERVICE_CLASS_STATUS", ae.ServiceClassStorage, ""},
		{"QR_FIND_SERVICE_CLASS_STATUS", ae.ServiceClassQueryRetrieve, ae.OperationFind},
		{"QR_MOVE_SERVICE_CLASS_STATUS", ae.ServiceClassQueryRetrieve, ae.OperationMove},
		{"QR_GET_SERVICE_CLASS_STATUS", ae.ServiceClassQueryRetrieve, ae.OperationGet},
		{"QR_FIND_SERVICE_CLASS_STATUS", ae.ServiceClassBasicWorklistManagement, ""},
		{"NON_PATIENT_SERVICE_CLASS_STATUS", ae.ServiceClassNonPatientObjectStorage, ""},
		{"RELEVANT_PATIENT_SERVICE_CLASS_STATUS", ae.ServiceClassRelevantPatientInformationQuery, ""},
		{"SUBSTANCE_ADMINISTRATION_SERVICE_CLASS_STATUS", ae.ServiceClassSubstanceAdministrationQuery, ""},
		{"PROCEDURE_STEP_STATUS", ae.ServiceClassProcedureStep, ""},
		{"PRINT_JOB_MANAGEMENT_SERVICE_CLASS_STATUS", ae.ServiceClassPrintManagement, ""},
		{"APPLICATION_EVENT_LOGGING_SERVICE_CLASS_STATUS", ae.ServiceClassApplicationEventLogging, ""},
		{"UNIFIED_PROCEDURE_STEP_SERVICE_CLASS_STATUS", ae.ServiceClassUnifiedProcedureStep, ""},
		{"RT_MACHINE_VERIFICATION_SERVICE_CLASS_STATUS", ae.ServiceClassRTMachineVerification, ""},
		{"STORAGE_COMMITMENT_SERVICE_CLASS_STATUS", ae.ServiceClassStorageCommitment, ""},
	}

	for _, c := range cases {
		upstream, ok := dicts[c.dict]
		if !ok {
			t.Errorf("%s is not in status.py any more", c.dict)
			continue
		}
		for _, code := range differingCodes(upstream, c.class, c.op) {
			t.Errorf("%s: 0x%04X: pynetdicom says %v, %s.HasStatus(%q) says %v",
				c.dict, code, upstream[code], c.class, c.op, c.class.HasStatus(c.op, code))
		}
	}

	// Three tables are defined upstream and attached to no class. Where a class
	// answers a code its own table lacks, the class is following another table;
	// where it refuses a code its own table has, the table is not attached.
	// Either way the assertion is about a code that tells the two apart.
	dead := []struct {
		dict         string
		class        ae.ServiceClass
		code         uint16
		tableHas     bool
		classAnswers bool
		why          string
	}{
		{
			dict: "MODALITY_WORKLIST_SERVICE_CLASS_STATUS", class: ae.ServiceClassBasicWorklistManagement,
			code: 0xB001, tableHas: false, classAnswers: true,
			why: "BasicWorklistManagementServiceClass answers QR_FIND_SERVICE_CLASS_STATUS",
		},
		{
			dict: "MEDIA_CREATION_MANAGEMENT_SERVICE_CLASS_STATUS", class: ae.ServiceClassMediaCreationManagement,
			code: 0xC201, tableHas: true, classAnswers: false,
			why: "MediaCreationManagementServiceClass sets no statuses",
		},
		{
			dict: "STORAGE_MANAGEMENT_SERVICE_CLASS_STATUS", class: ae.ServiceClassStorageManagement,
			code: 0xB010, tableHas: true, classAnswers: false,
			why: "StorageManagementServiceClass sets no statuses",
		},
	}
	for _, d := range dead {
		upstream, ok := dicts[d.dict]
		if !ok {
			t.Errorf("%s is not in status.py any more; check whether it is now attached", d.dict)
			continue
		}
		if upstream[d.code] != d.tableHas {
			t.Fatalf("%s no longer %s 0x%04X; this case no longer proves anything",
				d.dict, map[bool]string{true: "lists", false: "lacks"}[d.tableHas], d.code)
		}
		if got := d.class.HasStatus("", d.code); got != d.classAnswers {
			t.Errorf("%s.HasStatus(0x%04X) = %v: %s", d.class, d.code, got, d.why)
		}
	}

	// The Query/Retrieve subclasses share the Query/Retrieve answer: pynetdicom
	// routes them through QueryRetrieveServiceClass, which picks the set from
	// the operation, not from the model.
	for _, class := range []ae.ServiceClass{
		ae.ServiceClassColorPaletteQueryRetrieve,
		ae.ServiceClassDefinedProcedureProtocolQueryRetrieve,
		ae.ServiceClassHangingProtocolQueryRetrieve,
		ae.ServiceClassImplantTemplateQueryRetrieve,
		ae.ServiceClassInventoryQueryRetrieve,
		ae.ServiceClassProtocolApprovalQueryRetrieve,
	} {
		for _, code := range differingCodes(dicts["QR_MOVE_SERVICE_CLASS_STATUS"], class, ae.OperationMove) {
			t.Errorf("%s C-MOVE: 0x%04X does not match QR_MOVE_SERVICE_CLASS_STATUS", class, code)
		}
	}
}

// differingCodes returns every 16-bit code whose membership the two disagree on.
// Status codes are 16 bits wide, so sweeping all of them is cheaper than
// reasoning about which ranges an error could hide in.
func differingCodes(upstream map[uint16]bool, class ae.ServiceClass, op ae.RetrieveOperation) []uint16 {
	var out []uint16
	for code := 0; code <= 0xFFFF; code++ {
		if upstream[uint16(code)] != class.HasStatus(op, uint16(code)) {
			out = append(out, uint16(code))
		}
	}
	return out
}

// parseStatusDicts evaluates the dicts in pynetdicom's status.py down to plain
// sets of codes, in the order the file applies them: a literal, then the
// range loops that add to it, then the .update() calls that merge the general
// codes in. Tuples are ignored -- this test is about membership.
func parseStatusDicts(t *testing.T, src string) map[string]map[uint16]bool {
	t.Helper()

	// The submodule is a git checkout and core.autocrlf may have converted it;
	// a line-ending difference is not a change in the table.
	src = strings.ReplaceAll(src, "\r\n", "\n")
	lines := strings.Split(src, "\n")

	var (
		literal     = regexp.MustCompile(`^([A-Z][A-Z_]*)(?:: StatusDictType)? = \{\}$`)
		opensBlock  = regexp.MustCompile(`^([A-Z][A-Z_]*)(?:: StatusDictType)? = \{$`)
		alias       = regexp.MustCompile(`^([A-Z][A-Z_]*)(?:: StatusDictType)? = ([A-Z][A-Z_]*)$`)
		ranged      = regexp.MustCompile(`^for _code in range\((0x[0-9A-Fa-f]+), (0x[0-9A-Fa-f]+) \+ 1\):$`)
		rangedEntry = regexp.MustCompile(`^\s+([A-Z][A-Z_]*)\[_code\] = `)
		update      = regexp.MustCompile(`^([A-Z][A-Z_]*)(?:: StatusDictType)?\.update\(([A-Z][A-Z_]*)\)$`)
		updateBlock = regexp.MustCompile(`^([A-Z][A-Z_]*)(?:: StatusDictType)?\.update\($`)
		code        = regexp.MustCompile(`0x([0-9A-Fa-f]{4}):`)
	)

	dicts := make(map[string]map[uint16]bool)
	codesIn := func(text string) []uint16 {
		var out []uint16
		for _, m := range code.FindAllStringSubmatch(text, -1) {
			n, err := strconv.ParseUint(m[1], 16, 16)
			if err != nil {
				t.Fatalf("0x%s: %v", m[1], err)
			}
			out = append(out, uint16(n))
		}
		return out
	}
	entry := func(name string) map[uint16]bool {
		if d, ok := dicts[name]; ok {
			return d
		}
		d := make(map[uint16]bool)
		dicts[name] = d
		return d
	}

	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t")

		switch {
		case literal.MatchString(line):
			entry(literal.FindStringSubmatch(line)[1])

		case opensBlock.MatchString(line):
			name := opensBlock.FindStringSubmatch(line)[1]
			j := i + 1
			for ; j < len(lines) && strings.TrimRight(lines[j], " \t") != "}"; j++ {
			}
			if j == len(lines) {
				t.Fatalf("%s: the dict block is not closed", name)
			}
			d := entry(name)
			for _, c := range codesIn(strings.Join(lines[i+1:j], "\n")) {
				d[c] = true
			}
			i = j

		case alias.MatchString(line):
			m := alias.FindStringSubmatch(line)
			src, ok := dicts[m[2]]
			if !ok {
				t.Fatalf("%s = %s: %s has not been seen yet", m[1], m[2], m[2])
			}
			d := make(map[uint16]bool, len(src))
			for c := range src {
				d[c] = true
			}
			dicts[m[1]] = d

		case ranged.MatchString(line):
			m := ranged.FindStringSubmatch(line)
			lo, errLo := strconv.ParseUint(m[1][2:], 16, 16)
			hi, errHi := strconv.ParseUint(m[2][2:], 16, 16)
			if errLo != nil || errHi != nil {
				t.Fatalf("%s: bad range", line)
			}
			// The assignment is the first `NAME[_code] =` line in the loop body.
			j := i + 1
			for ; j < len(lines) && !rangedEntry.MatchString(lines[j]); j++ {
			}
			if j == len(lines) {
				t.Fatalf("%s: the loop body has no assignment", line)
			}
			d := entry(rangedEntry.FindStringSubmatch(lines[j])[1])
			for c := lo; c <= hi; c++ {
				d[uint16(c)] = true
			}
			i = j

		case update.MatchString(line):
			m := update.FindStringSubmatch(line)
			src, ok := dicts[m[2]]
			if !ok {
				t.Fatalf("%s.update(%s): %s has not been seen yet", m[1], m[2], m[2])
			}
			d := entry(m[1])
			for c := range src {
				d[c] = true
			}

		case updateBlock.MatchString(line):
			name := updateBlock.FindStringSubmatch(line)[1]
			j := i + 1
			for ; j < len(lines) && strings.TrimRight(lines[j], " \t") != ")"; j++ {
			}
			if j == len(lines) {
				t.Fatalf("%s.update(: the call is not closed", name)
			}
			d := entry(name)
			for _, c := range codesIn(strings.Join(lines[i+1:j], "\n")) {
				d[c] = true
			}
			i = j
		}
	}

	if len(dicts) < 16 {
		t.Fatalf("parsed %d dicts from status.py; the file has changed shape", len(dicts))
	}
	if len(dicts["GENERAL_STATUS"]) < 20 {
		t.Fatalf("GENERAL_STATUS parsed with %d codes", len(dicts["GENERAL_STATUS"]))
	}
	return dicts
}

// queryIdentifier is the smallest C-FIND identifier this SCU can send: one
// query/retrieve level, which is what makes the payload non-empty.
func queryIdentifier() *godicom.Dataset {
	ident := godicom.NewDataset()
	ident.Set(godicom.NewDataElement(godicom.MustTag("QueryRetrieveLevel"), godicom.VRCS, "STUDY"))
	return ident
}

// A status the class does not define is a handler's bug, not the peer's: the
// response goes out unchanged and the warning is ours. pynetdicom logs the same
// thing and sends the same response (service_class.py, "Unknown status value
// returned by callback").
func TestSCPWarnsAboutUnexpectedStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status uint16
		warn   bool
	}{
		{"C-MOVE status on a C-FIND", 0xA801, true},
		{"C-FIND warning", 0xB001, false},
		{"success", 0x0000, false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			var logBuf bytes.Buffer
			serverLogger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn}))

			errCh := make(chan error, 1)
			go func() {
				errCh <- ae.Serve(ctx, ln, ae.ServerConfig{
					AETitle:                  "STSCP",
					AcceptedAbstractSyntaxes: []string{ae.StudyRootQueryRetrieveInformationModelFind},
					Logger:                   serverLogger,
					OnCFind:                  ae.FindMatches([]ae.FindMatch{{Status: tc.status}}),
				})
			}()

			dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer dialCancel()

			assoc, err := ae.Dial(dialCtx, ae.Config{
				AETitle: "STSCU",
				PresentationContexts: []ae.PresentationContext{{
					ID:               1,
					AbstractSyntax:   ae.StudyRootQueryRetrieveInformationModelFind,
					TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
				}},
			}, ln.Addr().String(), "STSCP")
			if err != nil {
				t.Fatalf("dial: %v", err)
			}

			matches, err := assoc.CFind(dialCtx, ae.FindRequest{
				QueryModel:     ae.StudyRootQueryRetrieveInformationModelFind,
				IdentifierData: queryIdentifier(),
			})
			if err != nil {
				t.Fatalf("C-FIND: %v", err)
			}
			if len(matches) != 1 || matches[0].Status != tc.status {
				t.Fatalf("matches = %+v, want one with status 0x%04X", matches, tc.status)
			}
			if err := assoc.Release(dialCtx); err != nil {
				t.Fatalf("release: %v", err)
			}
			cancel()
			<-errCh

			out := logBuf.String()
			got := strings.Contains(out, "not defined for this service class")
			if got != tc.warn {
				t.Fatalf("warning = %v, want %v for status 0x%04X in:\n%s", got, tc.warn, tc.status, out)
			}
			if tc.warn {
				for _, want := range []string{dimse.FormatStatus(tc.status), "command_name=C-FIND-RSP", ae.StudyRootQueryRetrieveInformationModelFind} {
					if !strings.Contains(out, want) {
						t.Fatalf("missing %q in:\n%s", want, out)
					}
				}
			}
		})
	}
}
