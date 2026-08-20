package main

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// kdfIters is the PBKDF2 work factor. A var (not const) so tests can lower it.
var kdfIters = 600000

// stdinReader is shared so successive hidden reads don't drop buffered input.
var stdinReader = bufio.NewReader(os.Stdin)

// ---------------------------------------------------------------------------
// PBKDF2-HMAC-SHA256 (RFC 8018), implemented here to stay dependency-free.
// ---------------------------------------------------------------------------

func pbkdf2SHA256(password, salt []byte, iters, keyLen int) []byte {
	const hLen = sha256.Size // 32
	numBlocks := (keyLen + hLen - 1) / hLen
	dk := make([]byte, 0, numBlocks*hLen)

	var blockIdx [4]byte
	for block := 1; block <= numBlocks; block++ {
		binary.BigEndian.PutUint32(blockIdx[:], uint32(block))

		prf := hmac.New(sha256.New, password)
		prf.Write(salt)
		prf.Write(blockIdx[:])
		u := prf.Sum(nil)

		t := make([]byte, len(u))
		copy(t, u)

		for i := 1; i < iters; i++ {
			prf := hmac.New(sha256.New, password)
			prf.Write(u)
			u = prf.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		dk = append(dk, t...)
	}
	return dk[:keyLen]
}

// ---------------------------------------------------------------------------
// Encrypted envelope
// ---------------------------------------------------------------------------

const envMagic = "faded-enc-v1"

type encEnvelope struct {
	Faded  string `json:"faded"` // envMagic
	KDF    string `json:"kdf"`   // "pbkdf2-sha256"
	Iters  int    `json:"iters"`
	Salt   string `json:"salt"`  // base64
	Nonce  string `json:"nonce"` // base64
	Cipher string `json:"ct"`    // base64
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

// encryptBytes seals plaintext under a passphrase, returning a JSON envelope.
func encryptBytes(plaintext, passphrase []byte) ([]byte, error) {
	salt, err := randomBytes(16)
	if err != nil {
		return nil, err
	}
	key := pbkdf2SHA256(passphrase, salt, kdfIters, 32) // AES-256
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce, err := randomBytes(gcm.NonceSize())
	if err != nil {
		return nil, err
	}
	ct := gcm.Seal(nil, nonce, plaintext, nil)

	env := encEnvelope{
		Faded:  envMagic,
		KDF:    "pbkdf2-sha256",
		Iters:  kdfIters,
		Salt:   base64.StdEncoding.EncodeToString(salt),
		Nonce:  base64.StdEncoding.EncodeToString(nonce),
		Cipher: base64.StdEncoding.EncodeToString(ct),
	}
	return json.MarshalIndent(env, "", "  ")
}

// decryptBytes opens an envelope produced by encryptBytes. A wrong passphrase or
// any tampering fails authentication and returns an error.
func decryptBytes(data, passphrase []byte) ([]byte, error) {
	var env encEnvelope
	if err := json.Unmarshal(data, &env); err != nil || env.Faded != envMagic {
		return nil, fmt.Errorf("not a faded encrypted file")
	}
	salt, err := base64.StdEncoding.DecodeString(env.Salt)
	if err != nil {
		return nil, err
	}
	nonce, err := base64.StdEncoding.DecodeString(env.Nonce)
	if err != nil {
		return nil, err
	}
	ct, err := base64.StdEncoding.DecodeString(env.Cipher)
	if err != nil {
		return nil, err
	}
	iters := env.Iters
	if iters <= 0 {
		iters = kdfIters
	}
	key := pbkdf2SHA256(passphrase, salt, iters, 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, fmt.Errorf("bad nonce length")
	}
	return gcm.Open(nil, nonce, ct, nil)
}

// isEncrypted reports whether raw file bytes are a faded encrypted envelope.
func isEncrypted(data []byte) bool {
	var env encEnvelope
	return json.Unmarshal(data, &env) == nil && env.Faded == envMagic
}

// ---------------------------------------------------------------------------
// Passphrase input (echo off, never via argv)
// ---------------------------------------------------------------------------

func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// readSecret prompts on stderr and reads a line without echoing it. It uses
// $FADED_PASSPHRASE if set (handy for automation, but that leaks to the
// environment). On a terminal it disables echo via `stty`; otherwise it warns
// and reads visibly rather than failing.
func readSecret(prompt string) ([]byte, error) {
	if v := os.Getenv("FADED_PASSPHRASE"); v != "" {
		return []byte(v), nil
	}
	fmt.Fprint(os.Stderr, prompt)

	if stdinIsTerminal() {
		if _, err := exec.LookPath("stty"); err == nil {
			echoOff := exec.Command("stty", "-echo")
			echoOff.Stdin = os.Stdin
			_ = echoOff.Run()
			line, rerr := stdinReader.ReadString('\n')
			echoOn := exec.Command("stty", "echo")
			echoOn.Stdin = os.Stdin
			_ = echoOn.Run()
			fmt.Fprintln(os.Stderr)
			return []byte(strings.TrimRight(line, "\r\n")), rerr
		}
	}

	fmt.Fprintln(os.Stderr, "\n(warning: cannot hide input on this stream; it will be visible)")
	line, rerr := stdinReader.ReadString('\n')
	return []byte(strings.TrimRight(line, "\r\n")), rerr
}

// setNewPassphrase prompts twice and confirms the two entries match.
func setNewPassphrase() ([]byte, error) {
	p1, err := readSecret("Set a passphrase for the encrypted state: ")
	if err != nil {
		return nil, err
	}
	if len(p1) == 0 {
		return nil, fmt.Errorf("empty passphrase")
	}
	p2, err := readSecret("Confirm passphrase: ")
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare(p1, p2) != 1 {
		return nil, fmt.Errorf("passphrases did not match")
	}
	return p1, nil
}
