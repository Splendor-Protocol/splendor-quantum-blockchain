package main

import (
	"fmt"
	"os"
)

// refuseExistingWallet keeps a scripted `generate --pass-file` from silently
// replacing a wallet that may hold the validator bond: SaveWallet truncates.
func refuseExistingWallet(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists; move it away before generating a new wallet", path)
	}
	return nil
}
