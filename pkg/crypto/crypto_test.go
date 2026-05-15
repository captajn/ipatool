package crypto

import "testing"

func TestEncryptDecrypt(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	c, err := New(key)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cases := []string{
		"MyPassword123!@#",
		"密码123",
		"a",
		"this is a longer password with spaces and !@#$%^&*()",
		"",
	}
	for _, plain := range cases {
		enc, err := c.Encrypt(plain)
		if err != nil {
			t.Fatalf("Encrypt %q: %v", plain, err)
		}
		dec, err := c.Decrypt(enc)
		if err != nil {
			t.Fatalf("Decrypt %q: %v", plain, err)
		}
		if dec != plain {
			t.Errorf("Round-trip mismatch: got %q, want %q", dec, plain)
		}
	}
}

func TestEncryptNonceDiffers(t *testing.T) {
	key, _ := GenerateKey()
	c, _ := New(key)

	e1, _ := c.Encrypt("samepassword")
	e2, _ := c.Encrypt("samepassword")
	if e1 == e2 {
		t.Error("expected different ciphertexts due to random nonce")
	}
}

func TestKeyMissing(t *testing.T) {
	if _, err := New(""); err != ErrKeyMissing {
		t.Errorf("want ErrKeyMissing, got %v", err)
	}
}

func TestInvalidKey(t *testing.T) {
	// Key chỉ 16 bytes (base64 of 16 bytes)
	if _, err := New("c2hvcnRrZXk="); err != ErrInvalidKey {
		t.Errorf("want ErrInvalidKey, got %v", err)
	}
}

func TestTamperDetection(t *testing.T) {
	key, _ := GenerateKey()
	c, _ := New(key)

	enc, _ := c.Encrypt("secret")
	// Flip last char (tamper)
	tampered := enc[:len(enc)-2] + "XY"
	if _, err := c.Decrypt(tampered); err == nil {
		t.Error("expected error on tampered ciphertext")
	}
}

func TestIsEncrypted(t *testing.T) {
	key, _ := GenerateKey()
	c, _ := New(key)
	enc, _ := c.Encrypt("hello")
	if !IsEncrypted(enc) {
		t.Errorf("expected ciphertext to be detected as encrypted: %q", enc)
	}
	if IsEncrypted("plain_password!@#") {
		t.Error("expected plaintext to NOT be detected as encrypted")
	}
	if IsEncrypted("") {
		t.Error("expected empty to NOT be detected as encrypted")
	}
}
