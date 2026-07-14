// Package status defines DIMSE status codes as named constants.
//
// Go cannot look these up dynamically the way Python/pynetdicom does; values
// are declared explicitly, matching dcm4che's org.dcm4che3.net.Status catalog
// (PS3.7 / service-class annexes). Prefer status.Success over a bare 0x0000.
package status

// General DIMSE statuses (PS3.7).
const (
	Success        uint16 = 0x0000
	Pending        uint16 = 0xFF00
	PendingWarning uint16 = 0xFF01
	Cancel         uint16 = 0xFE00
)

// Common failure / warning codes used across C-* and N-* services.
const (
	NoSuchAttribute           uint16 = 0x0105
	InvalidAttributeValue     uint16 = 0x0106
	AttributeListError        uint16 = 0x0107
	ProcessingFailure         uint16 = 0x0110
	DuplicateSOPInstance      uint16 = 0x0111
	NoSuchObjectInstance      uint16 = 0x0112
	NoSuchEventType           uint16 = 0x0113
	NoSuchArgument            uint16 = 0x0114
	InvalidArgumentValue      uint16 = 0x0115
	AttributeValueOutOfRange  uint16 = 0x0116
	InvalidObjectInstance     uint16 = 0x0117
	NoSuchSOPClass            uint16 = 0x0118
	ClassInstanceConflict     uint16 = 0x0119
	MissingAttribute          uint16 = 0x0120
	MissingAttributeValue     uint16 = 0x0121
	SOPClassNotSupported      uint16 = 0x0122
	NoSuchActionType          uint16 = 0x0123
	NotAuthorized             uint16 = 0x0124
	DuplicateInvocation       uint16 = 0x0210
	UnrecognizedOperation     uint16 = 0x0211
	MistypedArgument          uint16 = 0x0212
	ResourceLimitation        uint16 = 0x0213
)

// Query/Retrieve and Storage class statuses (PS3.4).
const (
	OutOfResources                   uint16 = 0xA700
	UnableToCalculateNumberOfMatches uint16 = 0xA701
	UnableToPerformSubOperations     uint16 = 0xA702
	MoveDestinationUnknown           uint16 = 0xA801
	IdentifierDoesNotMatchSOPClass   uint16 = 0xA900
	DataSetDoesNotMatchSOPClassError uint16 = 0xA900

	OneOrMoreFailures                  uint16 = 0xB000
	CoercionOfDataElements             uint16 = 0xB000
	ElementsDiscarded                  uint16 = 0xB006
	DataSetDoesNotMatchSOPClassWarning uint16 = 0xB007

	UnableToProcess  uint16 = 0xC000
	CannotUnderstand uint16 = 0xC000
)

// Unified Procedure Step (UPS) statuses (PS3.4).
const (
	UPSCreatedWithModifications            uint16 = 0xB300
	UPSDeletionLockNotGranted              uint16 = 0xB301
	UPSAlreadyInRequestedStateOfCanceled   uint16 = 0xB304
	UPSCoercedInvalidValuesToValidValues   uint16 = 0xB305
	UPSAlreadyInRequestedStateOfCompleted  uint16 = 0xB306
	UPSMayNoLongerBeUpdated                uint16 = 0xC300
	UPSTransactionUIDNotCorrect            uint16 = 0xC301
	UPSAlreadyInProgress                   uint16 = 0xC302
	UPSStateMayNotChangedToScheduled       uint16 = 0xC303
	UPSNotMetFinalStateRequirements        uint16 = 0xC304
	UPSDoesNotExist                        uint16 = 0xC307
	UPSUnknownReceivingAET                 uint16 = 0xC308
	UPSNotScheduled                        uint16 = 0xC309
	UPSNotYetInProgress                    uint16 = 0xC310
	UPSAlreadyCompleted                    uint16 = 0xC311
	UPSPerformerCannotBeContacted          uint16 = 0xC312
	UPSPerformerChoosesNotToCancel         uint16 = 0xC313
	UPSActionNotAppropriate                uint16 = 0xC314
	UPSDoesNotSupportEventReports          uint16 = 0xC315
)

// IsPending reports whether code is a Pending / Pending Warning status
// (dcm4che Status.isPending: (status & Pending) == Pending).
func IsPending(code uint16) bool {
	return code&Pending == Pending
}
