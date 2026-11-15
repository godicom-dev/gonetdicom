package ae

import "github.com/godicom-dev/godicom/uid"

// ServiceClass is the DIMSE service class a SOP Class belongs to: the set of
// operations it answers, and therefore the presentation contexts a peer has to
// propose in order to ask for one of them.
//
// The table follows pynetdicom/pynetdicom/sop_class.py, where the same
// information decides which ServiceClass implementation handles a presentation
// context. It is knowledge about DIMSE rather than about the data set, which is
// why it lives here and not in godicom next to the UID dictionary: pydicom --
// the library godicom ports -- has no notion of a service class, and the map
// from a SOP Class to the service that serves it is stated in PS3.4, not PS3.6.
type ServiceClass string

const (
	// ServiceClassUnknown is what ServiceClassOf answers for a UID no service
	// class claims: a private SOP Class, a transfer syntax, a well-known
	// instance. pynetdicom answers its base ServiceClass in the same cases, so
	// this is a value rather than an error.
	ServiceClassUnknown ServiceClass = ""

	ServiceClassVerification                          ServiceClass = "Verification"
	ServiceClassQueryRetrieve                         ServiceClass = "QueryRetrieve"
	ServiceClassStorage                               ServiceClass = "Storage"
	ServiceClassApplicationEventLogging               ServiceClass = "ApplicationEventLogging"
	ServiceClassBasicWorklistManagement               ServiceClass = "BasicWorklistManagement"
	ServiceClassColorPaletteQueryRetrieve             ServiceClass = "ColorPaletteQueryRetrieve"
	ServiceClassDefinedProcedureProtocolQueryRetrieve ServiceClass = "DefinedProcedureProtocolQueryRetrieve"
	ServiceClassDisplaySystemManagement               ServiceClass = "DisplaySystemManagement"
	ServiceClassHangingProtocolQueryRetrieve          ServiceClass = "HangingProtocolQueryRetrieve"
	ServiceClassImplantTemplateQueryRetrieve          ServiceClass = "ImplantTemplateQueryRetrieve"
	ServiceClassInstanceAvailabilityNotification      ServiceClass = "InstanceAvailabilityNotification"
	ServiceClassInventoryQueryRetrieve                ServiceClass = "InventoryQueryRetrieve"
	ServiceClassMediaCreationManagement               ServiceClass = "MediaCreationManagement"
	ServiceClassNonPatientObjectStorage               ServiceClass = "NonPatientObjectStorage"
	ServiceClassPrintManagement                       ServiceClass = "PrintManagement"
	ServiceClassProcedureStep                         ServiceClass = "ProcedureStep"
	ServiceClassProtocolApprovalQueryRetrieve         ServiceClass = "ProtocolApprovalQueryRetrieve"
	ServiceClassRelevantPatientInformationQuery       ServiceClass = "RelevantPatientInformationQuery"
	ServiceClassRTMachineVerification                 ServiceClass = "RTMachineVerification"
	ServiceClassStorageCommitment                     ServiceClass = "StorageCommitment"
	ServiceClassStorageManagement                     ServiceClass = "StorageManagement"
	ServiceClassSubstanceAdministrationQuery          ServiceClass = "SubstanceAdministrationQuery"
	ServiceClassUnifiedProcedureStep                  ServiceClass = "UnifiedProcedureStep"
)

// String returns the name of the class, and "Unknown" for ServiceClassUnknown.
func (c ServiceClass) String() string {
	if c == ServiceClassUnknown {
		return "Unknown"
	}
	return string(c)
}

// SOPClasses returns the SOP Class UIDs of the class, in the order pynetdicom
// lists them.
//
// The slice is a fresh one. AllStorageSOPClasses and the tables beneath
// SOPClasses are package state, and a caller that sorts or truncates its copy
// must not reorder what the next caller sees.
func (c ServiceClass) SOPClasses() []string {
	return append([]string(nil), sopClassesByService[c]...)
}

// Has reports whether a UID belongs to the class.
func (c ServiceClass) Has(u string) bool {
	return ServiceClassOf(u) == c
}

// ServiceClasses returns every class in the table, in the order
// pynetdicom's uid_to_service_class checks them.
func ServiceClasses() []ServiceClass {
	return append([]ServiceClass(nil), serviceClassesInOrder...)
}

// ServiceClassOf returns the service class u belongs to, or
// ServiceClassUnknown for a UID no class claims.
//
// A Service Class UID is answered as well as a SOP Class UID: PS3.6 records
// 1.2.840.10008.4.2 and 1.2.840.10008.5.1.4.34.6 as type "Service Class", and
// asking which service one names has an answer. They appear in no SOPClasses
// list, because they are not abstract syntaxes -- proposing one as a
// presentation context is refused, and listing it would invite exactly that.
func ServiceClassOf(u string) ServiceClass {
	return serviceClassByUID[u]
}

// serviceClassesInOrder is the table's order, which is the order
// uid_to_service_class checks the classes in. It also decides the index below,
// where the first class to claim a UID keeps it; the classes do not overlap
// today, and this is what makes the answer stable if one ever does.
var serviceClassesInOrder = []ServiceClass{
	ServiceClassVerification,
	ServiceClassQueryRetrieve,
	ServiceClassStorage,
	ServiceClassApplicationEventLogging,
	ServiceClassBasicWorklistManagement,
	ServiceClassColorPaletteQueryRetrieve,
	ServiceClassDefinedProcedureProtocolQueryRetrieve,
	ServiceClassDisplaySystemManagement,
	ServiceClassHangingProtocolQueryRetrieve,
	ServiceClassImplantTemplateQueryRetrieve,
	ServiceClassInstanceAvailabilityNotification,
	ServiceClassInventoryQueryRetrieve,
	ServiceClassMediaCreationManagement,
	ServiceClassNonPatientObjectStorage,
	ServiceClassPrintManagement,
	ServiceClassProcedureStep,
	ServiceClassProtocolApprovalQueryRetrieve,
	ServiceClassRelevantPatientInformationQuery,
	ServiceClassRTMachineVerification,
	ServiceClassStorageCommitment,
	ServiceClassStorageManagement,
	ServiceClassSubstanceAdministrationQuery,
	ServiceClassUnifiedProcedureStep,
}

// sopClassesByService holds the SOP Classes of each service class, keyed by the
// class. Empty for ServiceClassUnknown, which claims nothing.
var sopClassesByService = map[ServiceClass][]string{
	ServiceClassVerification: UIDStrings(
		uid.Verification,
	),
	ServiceClassQueryRetrieve: UIDStrings(
		uid.PatientRootQueryRetrieveInformationModelFind,
		uid.PatientRootQueryRetrieveInformationModelMove,
		uid.PatientRootQueryRetrieveInformationModelGet,
		uid.StudyRootQueryRetrieveInformationModelFind,
		uid.StudyRootQueryRetrieveInformationModelMove,
		uid.StudyRootQueryRetrieveInformationModelGet,
		uid.PatientStudyOnlyQueryRetrieveInformationModelFind,
		uid.PatientStudyOnlyQueryRetrieveInformationModelMove,
		uid.PatientStudyOnlyQueryRetrieveInformationModelGet,
		uid.CompositeInstanceRootRetrieveMove,
		uid.CompositeInstanceRootRetrieveGet,
		uid.CompositeInstanceRetrieveWithoutBulkDataGet,
		uid.RepositoryQuery,
	),
	// The storage SOP Classes: AllStorageSOPClasses already holds exactly this
	// set, so the entry names it rather than keeping a second copy that could
	// drift.
	ServiceClassStorage: AllStorageSOPClasses,
	ServiceClassApplicationEventLogging: UIDStrings(
		uid.ProceduralEventLogging,
		uid.SubstanceAdministrationLogging,
	),
	ServiceClassBasicWorklistManagement: UIDStrings(
		uid.ModalityWorklistInformationModelFind,
	),
	ServiceClassColorPaletteQueryRetrieve: UIDStrings(
		uid.ColorPaletteQueryRetrieveInformationModelFind,
		uid.ColorPaletteQueryRetrieveInformationModelMove,
		uid.ColorPaletteQueryRetrieveInformationModelGet,
	),
	ServiceClassDefinedProcedureProtocolQueryRetrieve: UIDStrings(
		uid.DefinedProcedureProtocolInformationModelFind,
		uid.DefinedProcedureProtocolInformationModelMove,
		uid.DefinedProcedureProtocolInformationModelGet,
	),
	ServiceClassDisplaySystemManagement: UIDStrings(
		uid.DisplaySystem,
	),
	ServiceClassHangingProtocolQueryRetrieve: UIDStrings(
		uid.HangingProtocolInformationModelFind,
		uid.HangingProtocolInformationModelMove,
		uid.HangingProtocolInformationModelGet,
	),
	ServiceClassImplantTemplateQueryRetrieve: UIDStrings(
		uid.GenericImplantTemplateInformationModelFind,
		uid.GenericImplantTemplateInformationModelMove,
		uid.GenericImplantTemplateInformationModelGet,
		uid.ImplantAssemblyTemplateInformationModelFind,
		uid.ImplantAssemblyTemplateInformationModelMove,
		uid.ImplantAssemblyTemplateInformationModelGet,
		uid.ImplantTemplateGroupInformationModelFind,
		uid.ImplantTemplateGroupInformationModelMove,
		uid.ImplantTemplateGroupInformationModelGet,
	),
	ServiceClassInstanceAvailabilityNotification: UIDStrings(
		uid.InstanceAvailabilityNotification,
	),
	ServiceClassInventoryQueryRetrieve: UIDStrings(
		uid.InventoryFind,
		uid.InventoryMove,
		uid.InventoryGet,
	),
	ServiceClassMediaCreationManagement: UIDStrings(
		uid.MediaCreationManagement,
	),
	ServiceClassNonPatientObjectStorage: UIDStrings(
		uid.HangingProtocolStorage,
		uid.ColorPaletteStorage,
		uid.GenericImplantTemplateStorage,
		uid.ImplantAssemblyTemplateStorage,
		uid.ImplantTemplateGroupStorage,
		uid.CTDefinedProcedureProtocolStorage,
		uid.ProtocolApprovalStorage,
		uid.XADefinedProcedureProtocolStorage,
		uid.InventoryStorage,
	),
	ServiceClassPrintManagement: UIDStrings(
		uid.BasicFilmSession,
		uid.BasicFilmBox,
		uid.BasicGrayscaleImageBox,
		uid.BasicColorImageBox,
		uid.PrintJob,
		uid.BasicAnnotationBox,
		uid.Printer,
		uid.PrinterConfigurationRetrieval,
		uid.PresentationLUT,
		uid.BasicGrayscalePrintManagementMeta,
		uid.BasicColorPrintManagementMeta,
	),
	ServiceClassProcedureStep: UIDStrings(
		uid.ModalityPerformedProcedureStep,
		uid.ModalityPerformedProcedureStepRetrieve,
		uid.ModalityPerformedProcedureStepNotification,
	),
	ServiceClassProtocolApprovalQueryRetrieve: UIDStrings(
		uid.ProtocolApprovalInformationModelFind,
		uid.ProtocolApprovalInformationModelMove,
		uid.ProtocolApprovalInformationModelGet,
	),
	ServiceClassRelevantPatientInformationQuery: UIDStrings(
		uid.GeneralRelevantPatientInformationQuery,
		uid.BreastImagingRelevantPatientInformationQuery,
		uid.CardiacRelevantPatientInformationQuery,
	),
	ServiceClassRTMachineVerification: UIDStrings(
		uid.RTConventionalMachineVerification,
		uid.RTIonMachineVerification,
	),
	ServiceClassStorageCommitment: UIDStrings(
		uid.StorageCommitmentPushModel,
	),
	ServiceClassStorageManagement: UIDStrings(
		uid.InventoryCreation,
	),
	ServiceClassSubstanceAdministrationQuery: UIDStrings(
		uid.ProductCharacteristicsQuery,
		uid.SubstanceApprovalQuery,
	),
	ServiceClassUnifiedProcedureStep: UIDStrings(
		uid.UnifiedProcedureStepPush,
		uid.UnifiedProcedureStepWatch,
		uid.UnifiedProcedureStepPull,
		uid.UnifiedProcedureStepEvent,
		uid.UnifiedProcedureStepQuery,
	),
}

// serviceClassByUID is the reverse index, built once. A map is not ordered, so
// it is built by walking serviceClassesInOrder.
var serviceClassByUID = buildServiceClassIndex()

func buildServiceClassIndex() map[string]ServiceClass {
	index := make(map[string]ServiceClass, 256)
	for _, class := range serviceClassesInOrder {
		for _, u := range sopClassesByService[class] {
			if _, taken := index[u]; !taken {
				index[u] = class
			}
		}
	}
	index[string(uid.Storage)] = ServiceClassStorage
	index[string(uid.UnifiedWorklistAndProcedureStep)] = ServiceClassUnifiedProcedureStep
	return index
}
