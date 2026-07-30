package core

// Test-only hooks exposing unexported helpers to the external core_test package.

// PlanPartsForTest exposes planParts for testing the multipart sizing math.
func PlanPartsForTest(size int64) (int, int64) { return planParts(size) }
