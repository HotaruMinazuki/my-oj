package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// legacyHash reproduces the old "hex(sha256(salt||pw)):hex(salt)" format so the
// migration path can be exercised without a database.
func legacyHash(password string, salt []byte) string {
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(password))
	return hex.EncodeToString(h.Sum(nil)) + ":" + hex.EncodeToString(salt)
}

func TestHashPasswordProducesBcrypt(t *testing.T) {
	hash, err := hashPassword("correct horse battery")
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$2") {
		t.Fatalf("expected bcrypt hash ($2…), got %q", hash)
	}
	ok, needsRehash := checkPassword("correct horse battery", hash)
	if !ok || needsRehash {
		t.Fatalf("bcrypt hash: ok=%v needsRehash=%v, want ok=true needsRehash=false", ok, needsRehash)
	}
}

func TestCheckPasswordBcrypt_WrongPassword(t *testing.T) {
	hash, err := hashPassword("right")
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	if ok, _ := checkPassword("wrong", hash); ok {
		t.Fatal("wrong password verified against bcrypt hash")
	}
}

func TestCheckPasswordLegacy(t *testing.T) {
	salt := []byte("0123456789abcdef")
	stored := legacyHash("hunter2", salt)

	// Correct password against the old format: passes but flagged for rehash.
	ok, needsRehash := checkPassword("hunter2", stored)
	if !ok {
		t.Fatal("legacy hash: correct password rejected")
	}
	if !needsRehash {
		t.Fatal("legacy hash: needsRehash should be true so the caller upgrades it")
	}

	// Wrong password against the old format still fails (and is flagged legacy).
	if ok, needsRehash := checkPassword("nope", stored); ok || !needsRehash {
		t.Fatalf("legacy wrong password: ok=%v needsRehash=%v, want ok=false needsRehash=true", ok, needsRehash)
	}
}

func TestCheckPasswordMalformed(t *testing.T) {
	if ok, _ := checkPassword("anything", "not-a-valid-hash"); ok {
		t.Fatal("malformed stored hash should never verify")
	}
}
