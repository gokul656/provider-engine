package sshca

import (
	"crypto/rand"
	"fmt"
	"os"
	"time"

	"golang.org/x/crypto/ssh"
)

// CA holds the loaded SSH certificate authority.
type CA struct {
	signer ssh.Signer
}

// Load reads the CA private key from disk.
func Load(privKeyPath string) (*CA, error) {
	b, err := os.ReadFile(privKeyPath)
	if err != nil {
		return nil, fmt.Errorf("read ssh ca key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(b)
	if err != nil {
		return nil, fmt.Errorf("parse ssh ca key: %w", err)
	}
	return &CA{signer: signer}, nil
}

// SignRequest holds parameters for certificate issuance.
type SignRequest struct {
	UserPubKey string // authorized_keys-format public key from the buyer
	Principal  string // username to certify (e.g. "root", "ubuntu")
	VMID       string // embedded as key ID for audit
	TTL        time.Duration
}

// IssueCert returns a signed SSH certificate as an authorized-keys line.
func (ca *CA) IssueCert(req SignRequest) (string, error) {
	pubKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(req.UserPubKey))
	if err != nil {
		return "", fmt.Errorf("parse user pubkey: %w", err)
	}

	ttl := req.TTL
	if ttl == 0 {
		ttl = 8 * time.Hour
	}

	cert := &ssh.Certificate{
		Key:             pubKey,
		CertType:        ssh.UserCert,
		KeyId:           fmt.Sprintf("dcp-%s-%d", req.VMID, time.Now().Unix()),
		ValidPrincipals: []string{req.Principal},
		ValidAfter:      uint64(time.Now().Add(-30 * time.Second).Unix()),
		ValidBefore:     uint64(time.Now().Add(ttl).Unix()),
		Permissions: ssh.Permissions{
			Extensions: map[string]string{
				"permit-pty":              "",
				"permit-user-rc":          "",
				"permit-port-forwarding":  "",
			},
		},
	}

	if err := cert.SignCert(rand.Reader, ca.signer); err != nil {
		return "", fmt.Errorf("sign cert: %w", err)
	}

	return string(ssh.MarshalAuthorizedKey(cert)), nil
}

// PublicKey returns the CA public key as an authorized_keys line (bake into VMs).
func (ca *CA) PublicKey() string {
	return string(ssh.MarshalAuthorizedKey(ca.signer.PublicKey()))
}
