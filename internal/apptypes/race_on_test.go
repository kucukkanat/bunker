//go:build race

package apptypes

// The race detector slows encoding/xml ~10x, so wall-clock budgets in tests
// are scaled by this factor.
const raceSlowdown = 10
