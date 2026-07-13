package auth

import (
	"crypto/rand"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Verifier checks whether a client's SSH public key is authorized.
type Verifier struct {
	authorizedKeys map[string]bool // key fingerprint -> authorized
}

// NewSSHVerifier creates a verifier from an authorized_keys file.
func NewSSHVerifier(path string) (*Verifier, error) {
	path = expandHome(path)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading authorized keys %s: %w", path, err)
	}

	v := &Verifier{
		authorizedKeys: make(map[string]bool),
	}

	for len(data) > 0 {
		pubKey, _, _, rest, err := ssh.ParseAuthorizedKey(data)
		if err != nil {
			// Skip unparseable lines
			if idx := indexNewline(data); idx >= 0 {
				data = data[idx+1:]
				continue
			}
			break
		}
		fp := ssh.FingerprintSHA256(pubKey)
		v.authorizedKeys[fp] = true
		data = rest
	}

	slog.Info("loaded authorized keys", "count", len(v.authorizedKeys))
	return v, nil
}

// IsAuthorized checks if the given public key is in the authorized set.
func (v *Verifier) IsAuthorized(pubKey ssh.PublicKey) bool {
	fp := ssh.FingerprintSHA256(pubKey)
	return v.authorizedKeys[fp]
}

// GenerateNonce creates a random nonce for challenge-response auth.
func GenerateNonce() ([]byte, error) {
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return nonce, nil
}

// VerifySignature checks that the signature was produced by the given key over the data.
func VerifySignature(pubKey ssh.PublicKey, data []byte, sig *ssh.Signature) error {
	return pubKey.Verify(data, sig)
}

// Signer provides access to SSH keys for signing challenges (client side).
type Signer struct {
	signers []ssh.Signer
}

// NewAgentSigner connects to the SSH agent and returns a Signer.
func NewAgentSigner() (*Signer, error) {
	socket := os.Getenv("SSH_AUTH_SOCK")
	if socket == "" {
		return nil, fmt.Errorf("SSH_AUTH_SOCK not set — is ssh-agent running?")
	}

	conn, err := net.Dial("unix", socket)
	if err != nil {
		return nil, fmt.Errorf("connecting to ssh-agent: %w", err)
	}

	ag := agent.NewClient(conn)
	signers, err := ag.Signers()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("getting signers from agent: %w", err)
	}

	if len(signers) == 0 {
		conn.Close()
		return nil, fmt.Errorf("no keys available in ssh-agent")
	}

	slog.Info("loaded keys from ssh-agent", "count", len(signers))
	return &Signer{signers: signers}, nil
}

// NewKeySigner loads a private key from a file.
func NewKeySigner(keyPath string) (*Signer, error) {
	keyPath = expandHome(keyPath)

	data, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("reading key file %s: %w", keyPath, err)
	}

	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("parsing private key: %w", err)
	}

	return &Signer{signers: []ssh.Signer{signer}}, nil
}

// Sign signs the given data with the first available key.
func (s *Signer) Sign(data []byte) (ssh.PublicKey, *ssh.Signature, error) {
	for _, signer := range s.signers {
		sig, err := signer.Sign(rand.Reader, data)
		if err != nil {
			continue
		}
		return signer.PublicKey(), sig, nil
	}
	return nil, nil, fmt.Errorf("no key could sign the challenge")
}

// PublicKeys returns all available public keys.
func (s *Signer) PublicKeys() []ssh.PublicKey {
	keys := make([]ssh.PublicKey, len(s.signers))
	for i, signer := range s.signers {
		keys[i] = signer.PublicKey()
	}
	return keys
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return home + path[1:]
	}
	return path
}

func indexNewline(data []byte) int {
	for i, b := range data {
		if b == '\n' {
			return i
		}
	}
	return -1
}
