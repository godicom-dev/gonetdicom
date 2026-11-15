package ae

import "testing"

// HasStatus answers the general statuses for a class the table does not have, so
// an omitted row would not fail: it would quietly hold that class to the general
// list, which is the right answer for an unknown SOP Class and the wrong one for
// a class that has a set of its own.
func TestEveryServiceClassHasAStatusSet(t *testing.T) {
	t.Parallel()

	for _, class := range serviceClassesInOrder {
		if _, ok := statusesByService[class]; !ok {
			t.Errorf("%s has no status set", class)
		}
	}
	if len(statusesByService) != len(serviceClassesInOrder) {
		t.Errorf("statusesByService has %d entries for %d service classes: one is listed twice or is not a class",
			len(statusesByService), len(serviceClassesInOrder))
	}

	// ServiceClassUnknown is not a class in that list; its answer comes from the
	// fallback, and it has to be the general list.
	if _, ok := statusesByService[ServiceClassUnknown]; ok {
		t.Error("ServiceClassUnknown has a row; it must come from the fallback")
	}
	if !ServiceClassUnknown.HasStatus("", 0x0000) || ServiceClassUnknown.HasStatus("", 0xC000) {
		t.Error("ServiceClassUnknown does not answer the general statuses")
	}
}

// The ranges are what make the table readable; they are also the part a merge of
// two sets can lose silently. A C-MOVE refuses 0xC0xx through the shared range,
// not through a code of its own.
func TestStatusSetUnionKeepsRanges(t *testing.T) {
	t.Parallel()

	any := queryRetrieveStatuses.any
	for _, code := range []uint16{0xA700, 0xA710, 0xB001, 0xA801, 0xA702, 0xC001} {
		if !any.has(code) {
			t.Errorf("the union of the Query/Retrieve sets lost 0x%04X", code)
		}
	}
	if any.has(0x0101) {
		t.Error("the union has 0x0101, which none of the three sets has")
	}
}
