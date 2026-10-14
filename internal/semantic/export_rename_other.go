//go:build !darwin && !linux && !windows

package semantic

import "fmt"

func renameExportDirectory(from, to string) error {
	return fmt.Errorf("atomic directory exports are unsupported on this platform; choose JSON or JSONL")
}
