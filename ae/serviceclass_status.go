package ae

import (
	"context"

	"github.com/godicom-dev/gonetdicom"
	"github.com/godicom-dev/gonetdicom/dimse"
)

// This file is the other half of the service class table in serviceclass.go:
// not which presentation contexts a SOP Class is served on, but which statuses
// it may answer with once it is.
//
// The data is pynetdicom/pynetdicom/status.py, which transcribes the annexes of
// PS3.4 -- Annex B.2.3 for Storage, C.4 for the Query/Retrieve operations, K for
// the worklist, and so on -- plus the general codes of PS3.7 Annex C that every
// service shares. Each set is the list as that file gives it; where an annex
// gives a range ("0xA700-0xA7FF: refused, out of resources") it is kept as a
// range rather than expanded, because a table a reader can compare against the
// standard is worth more than one a lookup can binary-search.

// statusSet is a set of DIMSE status codes: the codes an annex names one by one,
// plus the ranges it gives as ranges.
type statusSet struct {
	codes  []uint16
	ranges [][2]uint16 // inclusive
}

func (s statusSet) has(code uint16) bool {
	for _, c := range s.codes {
		if c == code {
			return true
		}
	}
	for _, r := range s.ranges {
		if code >= r[0] && code <= r[1] {
			return true
		}
	}
	return false
}

// with returns the union of s and the sets given.
func (s statusSet) with(more ...statusSet) statusSet {
	out := statusSet{
		codes:  append([]uint16(nil), s.codes...),
		ranges: append([][2]uint16(nil), s.ranges...),
	}
	for _, m := range more {
		out.codes = append(out.codes, m.codes...)
		out.ranges = append(out.ranges, m.ranges...)
	}
	return out
}

// generalStatuses are the codes every service may answer with (PS3.7 Annex C).
// pynetdicom calls this GENERAL_STATUS and gives it to its base ServiceClass, so
// an unknown SOP Class is held to exactly this list here too.
var generalStatuses = statusSet{codes: []uint16{
	0x0000, // Success
	0x0105, 0x0106, 0x0107,
	0x0110, 0x0111, 0x0112, 0x0113, 0x0114, 0x0115, 0x0116, 0x0117, 0x0118, 0x0119,
	0x0120, 0x0121, 0x0122, 0x0123, 0x0124,
	0x0210, 0x0211, 0x0212, 0x0213,
	0xFE00, // Cancel
}}

// unableToProcess is the C000-CFFF range the Query/Retrieve, worklist, UPS and
// Storage annexes reserve for "unable to process" and its refinements.
var unableToProcess = [2]uint16{0xC000, 0xCFFF}

// storageStatuses is the Storage Service Class (PS3.4 Annex B.2.3).
var storageStatuses = generalStatuses.with(statusSet{
	codes: []uint16{0xB000, 0xB006, 0xB007},
	ranges: [][2]uint16{
		{0xA700, 0xA7FF}, // refused: out of resources
		{0xA900, 0xA9FF}, // data set does not match SOP class
		unableToProcess,
	},
})

// qrFindStatuses is C-FIND for the Query/Retrieve and Hanging Protocol models
// (PS3.4 C.4.1.1.4).
var qrFindStatuses = generalStatuses.with(statusSet{
	codes:  []uint16{0xA700, 0xA710, 0xA900, 0xB001, 0xFF00, 0xFF01},
	ranges: [][2]uint16{unableToProcess},
})

// qrMoveStatuses is C-MOVE (PS3.4 C.4.2.1.5). It has 0xA801 for an unknown
// destination and the sub-operation warnings, and neither 0xB001 nor 0xFF01.
var qrMoveStatuses = generalStatuses.with(statusSet{
	codes:  []uint16{0xA701, 0xA702, 0xA801, 0xA900, 0xAA00, 0xAA01, 0xAA02, 0xAA03, 0xAA04, 0xB000, 0xFF00},
	ranges: [][2]uint16{unableToProcess},
})

// qrGetStatuses is C-GET (PS3.4 C.4.3.1.4). Everything C-MOVE has except the
// destination, which a C-GET does not name.
var qrGetStatuses = generalStatuses.with(statusSet{
	codes:  []uint16{0xA701, 0xA702, 0xA900, 0xAA00, 0xAA01, 0xAA02, 0xAA03, 0xAA04, 0xB000, 0xFF00},
	ranges: [][2]uint16{unableToProcess},
})

// modalityWorklistStatuses (PS3.4 Annex K) is deliberately absent: pynetdicom
// defines it and attaches it to nothing, because BasicWorklistManagementServiceClass
// answers the C-FIND set above. Copying the table would have let a worklist SCP
// answer 0xB001 in silence, where pynetdicom warns.

// relevantPatientStatuses is PS3.4 Annex Q. The annex names its failures one by
// one, so C100 and C200 are here and the rest of C000-CFFF is not.
var relevantPatientStatuses = generalStatuses.with(statusSet{
	codes: []uint16{0xA700, 0xA900, 0xC000, 0xC100, 0xC200, 0xFF00},
})

// substanceAdministrationStatuses is PS3.4 Annex V.
var substanceAdministrationStatuses = generalStatuses.with(statusSet{
	codes:  []uint16{0xA700, 0xA900, 0xFF00, 0xFF01},
	ranges: [][2]uint16{unableToProcess},
})

// nonPatientStatuses is non-patient object storage (PS3.4 Annex GG): the three
// codes it adds to the general list, and no more -- in particular not the
// Storage ranges, because the annex does not inherit them.
var nonPatientStatuses = generalStatuses.with(statusSet{
	codes: []uint16{0xA700, 0xA900, 0xC000},
})

// procedureStepStatuses is the Modality Performed Procedure Step SOP Class
// (PS3.4 F.2.4.1.1.1), whose "no longer updatable" answer is 0x0110 with a
// different meaning than the general one.
var procedureStepStatuses = generalStatuses.with(statusSet{
	codes: []uint16{0x0001, 0x0110},
})

// printJobStatuses is Print Job Management (PS3.4 Annex H).
var printJobStatuses = generalStatuses.with(statusSet{
	codes: []uint16{
		0xB600, 0xB601, 0xB602, 0xB603, 0xB604, 0xB605, 0xB609, 0xB60A,
		0xC600, 0xC601, 0xC602, 0xC603, 0xC605, 0xC613, 0xC616,
	},
})

// storageManagementStatuses (0xB010) and mediaCreationStatuses (PS3.4 Annex J)
// are absent for the same reason: status.py defines both and attaches neither.

// applicationEventLoggingStatuses is PS3.4 Annex I.
var applicationEventLoggingStatuses = generalStatuses.with(statusSet{
	codes: []uint16{0xB101, 0xB102, 0xB104, 0xC101, 0xC102, 0xC103, 0xC104, 0xC10E, 0xC110, 0xC111},
})

// unifiedProcedureStepStatuses is UPS (PS3.4 Annex CC.2.4). The C3xx codes the
// annex lists by name are inside the range, so the range stands for them.
var unifiedProcedureStepStatuses = generalStatuses.with(statusSet{
	codes:  []uint16{0x0001, 0xA700, 0xA900, 0xB300, 0xB301, 0xB304, 0xB305, 0xB306, 0xFF00, 0xFF01},
	ranges: [][2]uint16{unableToProcess},
})

// rtMachineVerificationStatuses is RT Machine Verification (PS3.4 Annex DD).
// Its failures are named one by one, so unlike UPS it does not take the whole
// C000-CFFF range.
var rtMachineVerificationStatuses = generalStatuses.with(statusSet{
	codes: []uint16{0xC112, 0xC221, 0xC222, 0xC223, 0xC224, 0xC225, 0xC226, 0xC227},
})

// serviceStatuses is one class's answer: the set to use when the operation is
// named, and the class-wide set when it is not.
type serviceStatuses struct {
	any         statusSet
	byOperation map[RetrieveOperation]statusSet
}

func (s serviceStatuses) has(op RetrieveOperation, code uint16) bool {
	if set, ok := s.byOperation[op]; ok {
		return set.has(code)
	}
	return s.any.has(code)
}

// queryRetrieveStatuses is the Query/Retrieve classes, whose set depends on the
// operation: pynetdicom picks QR_FIND, QR_GET or QR_MOVE from the model UID in
// QueryRetrieveServiceClass.__init__, and the three differ by more than the
// operation-specific codes -- 0xFF01 is a C-FIND warning, 0xB000 a C-MOVE one.
var queryRetrieveStatuses = serviceStatuses{
	any: qrFindStatuses.with(qrGetStatuses, qrMoveStatuses),
	byOperation: map[RetrieveOperation]statusSet{
		OperationFind: qrFindStatuses,
		OperationGet:  qrGetStatuses,
		OperationMove: qrMoveStatuses,
	},
}

// statusesByService is the class → set table. A class missing from it would fall
// back to generalStatuses in HasStatus, silently, so
// TestEveryServiceClassHasAStatusSet requires this to cover ServiceClasses().
var statusesByService = map[ServiceClass]serviceStatuses{
	ServiceClassVerification:  {any: generalStatuses},
	ServiceClassQueryRetrieve: queryRetrieveStatuses,
	ServiceClassStorage:       {any: storageStatuses},

	// pynetdicom gives these classes no statuses attribute, so they answer the
	// general list: DisplaySystemManagement, InstanceAvailabilityNotification,
	// MediaCreationManagement, StorageManagement. The last two have a table in
	// status.py that no class attaches -- following the table instead of the
	// behaviour would quietly bless a status pynetdicom warns about.
	ServiceClassApplicationEventLogging:               {any: applicationEventLoggingStatuses},
	ServiceClassBasicWorklistManagement:               {any: qrFindStatuses},
	ServiceClassColorPaletteQueryRetrieve:             queryRetrieveStatuses,
	ServiceClassDefinedProcedureProtocolQueryRetrieve: queryRetrieveStatuses,
	ServiceClassDisplaySystemManagement:               {any: generalStatuses},
	ServiceClassHangingProtocolQueryRetrieve:          queryRetrieveStatuses,
	ServiceClassImplantTemplateQueryRetrieve:          queryRetrieveStatuses,
	ServiceClassInstanceAvailabilityNotification:      {any: generalStatuses},
	ServiceClassInventoryQueryRetrieve:                queryRetrieveStatuses,
	ServiceClassMediaCreationManagement:               {any: generalStatuses},
	ServiceClassNonPatientObjectStorage:               {any: nonPatientStatuses},
	ServiceClassPrintManagement:                       {any: printJobStatuses},
	ServiceClassProcedureStep:                         {any: procedureStepStatuses},
	ServiceClassProtocolApprovalQueryRetrieve:         queryRetrieveStatuses,
	ServiceClassRelevantPatientInformationQuery:       {any: relevantPatientStatuses},
	ServiceClassRTMachineVerification:                 {any: rtMachineVerificationStatuses},
	ServiceClassStorageCommitment:                     {any: generalStatuses},
	ServiceClassStorageManagement:                     {any: generalStatuses},
	ServiceClassSubstanceAdministrationQuery:          {any: substanceAdministrationStatuses},
	ServiceClassUnifiedProcedureStep:                  {any: unifiedProcedureStepStatuses},
}

// HasStatus reports whether code is a status the class may answer with.
//
// op names the operation being answered, and only changes the answer for the
// Query/Retrieve classes, where C-FIND, C-GET and C-MOVE each have their own
// list. The empty value means "no operation in particular" and answers the union
// of them, which is what a caller that only knows the class can be told.
//
// An unknown class -- a private SOP Class, or a UID from no table at all --
// answers the general statuses, which is what pynetdicom's base ServiceClass
// does. This is a question about protocol, not a validation the package
// enforces: a handler's status is answered whatever this says.
func (c ServiceClass) HasStatus(op RetrieveOperation, code uint16) bool {
	if s, ok := statusesByService[c]; ok {
		return s.has(op, code)
	}
	return generalStatuses.has(code)
}

// operationForResponse names the operation a response answers. Only the
// Query/Retrieve classes have more than one set, and only these three responses
// carry a retrieve operation.
func operationForResponse(field uint16) RetrieveOperation {
	switch field {
	case dimse.CommandCFindRSP:
		return OperationFind
	case dimse.CommandCGetRSP:
		return OperationGet
	case dimse.CommandCMoveRSP:
		return OperationMove
	}
	return ""
}

// warnUnexpectedStatus logs a response whose status the SOP Class does not
// define, and does nothing else with it.
//
// pynetdicom logs the same thing and sends the status anyway
// (service_class.py: "Unknown status value returned by callback"), and that is
// the right shape here too: a handler that answers 0xA801 to a C-FIND has a bug,
// but turning its answer into an A-ABORT or a substitute status would report
// that bug to the peer as a protocol violation it did not commit.
//
// The check sits on the way out rather than in each handler because that is
// where the response, its command and its SOP Class are all in hand at once: the
// C-GET SCP answers C-STORE-RSPs as well as C-GET-RSPs, and a status can come
// from a peer rather than from a handler.
func warnUnexpectedStatus(ctx context.Context, command []byte) {
	field, status, sopClass, ok := dimse.PeekResponseInfo(command)
	if !ok {
		return
	}
	if ServiceClassOf(sopClass).HasStatus(operationForResponse(field), status) {
		return
	}
	gonetdicom.LoggerFromContext(ctx).WarnContext(ctx,
		"status is not defined for this service class; sending it anyway",
		gonetdicom.AttrStatus, dimse.FormatStatus(status),
		gonetdicom.AttrCommandName, dimse.CommandName(field),
		gonetdicom.AttrSOPClass, sopClass,
	)
}
