package system

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// UnitRow is one row of `systemctl list-units`: the unit, its load, active and sub state.
type UnitRow = unitRow

// UnitRows lists every service unit with one `systemctl list-units` - what the status LED reads
// every ten seconds for its radio-down and service-failed states (task 95). One call, nothing
// shown per unit (B-60).
func (s SystemdServices) UnitRows(ctx context.Context) ([]UnitRow, error) {
	out, err := s.run(ctx, "list-units", "--type=service", "--all", "--no-pager", "--plain", "--output=json")
	if err != nil {
		return nil, fmt.Errorf("systemctl list-units: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var rows []UnitRow
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("systemctl list-units: %w", err)
	}
	return rows, nil
}
