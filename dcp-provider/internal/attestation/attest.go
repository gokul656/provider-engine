package attestation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

// Info is sent to the control plane during registration.
type Info struct {
	MachineID  string `json:"machine_id"`
	BinaryHash string `json:"binary_hash"`
}

// Collect gathers attestation data for this machine.
func Collect() (Info, error) {
	mid, err := machineID()
	if err != nil {
		return Info{}, fmt.Errorf("machine id: %w", err)
	}
	hash, err := selfHash()
	if err != nil {
		return Info{}, fmt.Errorf("self hash: %w", err)
	}
	return Info{MachineID: mid, BinaryHash: hash}, nil
}

// machineID reads /etc/machine-id (stable across reboots on Linux).
func machineID() (string, error) {
	b, err := os.ReadFile("/etc/machine-id")
	if err != nil {
		// Fallback: hostname
		h, _ := os.Hostname()
		return h, nil
	}
	return strings.TrimSpace(string(b)), nil
}

// selfHash returns SHA-256 of the running binary.
func selfHash() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	f, err := os.Open(exe)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
