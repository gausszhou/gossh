package sshx

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gausszhou/gossh/internal/host"

	"golang.org/x/crypto/ssh"
)

// 真实的 ed25519 测试密钥(ssh-keygen 生成,仅用于单元测试)。
const testPublicKey1 = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHVWqW+eru2/NnqGmnPvFun0mrrbV6DH1ZBACdz+ChlL root@gossh"
const testPublicKey2 = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKa5m6S572p2Of6I+xrQcRbS3TLce2fkxhI9+aG41Fpp other@gossh"

func pubKey(t *testing.T, line string) ssh.PublicKey {
	t.Helper()
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		t.Fatalf("parse public key: %s", err)
	}
	return key
}

func TestKnownHostsTOFU(t *testing.T) {
	kh, err := LoadKnownHosts(filepath.Join(t.TempDir(), "known_hosts"))
	if err != nil {
		t.Fatal(err)
	}
	key := pubKey(t, testPublicKey1)
	addr := "127.0.0.1:22"

	// 首次:信任并记录
	if err := kh.Callback(addr)(addr, nil, key); err != nil {
		t.Fatalf("first connect should pin: %s", err)
	}
	// 再次:相同密钥通过
	if err := kh.Callback(addr)(addr, nil, key); err != nil {
		t.Fatalf("matching key should pass: %s", err)
	}

	// 不同密钥:拒绝
	alt := pubKey(t, testPublicKey2)
	err = kh.Callback(addr)(addr, nil, alt)
	if err == nil {
		t.Fatal("mismatched key must be rejected")
	}
	var mm *HostKeyMismatch
	if !errors.As(err, &mm) {
		t.Fatalf("expected HostKeyMismatch, got %v", err)
	}
	if !strings.Contains(err.Error(), "delete the pin") {
		t.Fatalf("mismatch error should mention the remedy: %v", err)
	}

	// 删除 pin 后可重新信任
	if err := kh.Forget(addr); err != nil {
		t.Fatal(err)
	}
	if err := kh.Callback(addr)(addr, nil, alt); err != nil {
		t.Fatalf("after forget, new key should be accepted: %s", err)
	}
}

func TestKnownHostsPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	kh, _ := LoadKnownHosts(path)
	key := pubKey(t, testPublicKey1)
	_ = kh.Callback("10.0.0.1:22")("10.0.0.1:22", nil, key)

	reloaded, err := LoadKnownHosts(path)
	if err != nil {
		t.Fatal(err)
	}
	pin, ok := reloaded.Get("10.0.0.1:22")
	if !ok {
		t.Fatal("pin must survive reload")
	}
	if string(pin.KeyBlob) != string(key.Marshal()) {
		t.Fatal("pin blob mismatch after reload")
	}
}

func TestSecretsMemoryFallback(t *testing.T) {
	s := NewSecrets()
	addr, user := "10.0.0.9:22", "ops"

	if _, ok, _ := s.GetPassword(addr, user); ok {
		t.Fatal("fresh store must not have the password")
	}
	if err := s.SetPassword(addr, user, "hunter2"); err != nil {
		t.Fatalf("set: %s", err)
	}
	pw, ok, err := s.GetPassword(addr, user)
	if err != nil || !ok || pw != "hunter2" {
		t.Fatalf("get: %q %v %v", pw, ok, err)
	}
	if err := s.DeletePassword(addr, user); err != nil {
		t.Fatalf("delete: %s", err)
	}
	if _, ok, _ := s.GetPassword(addr, user); ok {
		t.Fatal("password must be gone after delete")
	}

	// passphrase 路径
	kp := "/home/x/.ssh/id_rsa"
	_ = s.SetPassphrase(kp, "pp")
	pp, ok, _ := s.GetPassphrase(kp)
	if !ok || pp != "pp" {
		t.Fatalf("passphrase roundtrip failed: %q %v", pp, ok)
	}
}

func TestBuildHopPasswordCredential(t *testing.T) {
	kh, _ := LoadKnownHosts(t.TempDir() + "/kh")
	secrets := NewSecrets()
	h := &host.Host{
		ID: "h1", Name: "test", Address: "10.0.0.5", Port: 22, User: "root",
		Credential: host.Credential{Kind: host.CredPassword},
	}
	pw := "provided-secret"
	prov := &ProvidedSecrets{Password: &pw}

	hop, err := BuildHop(h, secrets, prov, kh, 0)
	if err != nil {
		t.Fatalf("build hop: %s", err)
	}
	if hop.User != "root" || hop.Addr != "10.0.0.5:22" {
		t.Fatalf("hop fields wrong: %+v", hop)
	}
	if len(hop.Auth) != 1 {
		t.Fatalf("expected 1 auth method, got %d", len(hop.Auth))
	}
}

func TestBuildHopNoCredentialFails(t *testing.T) {
	kh, _ := LoadKnownHosts(t.TempDir() + "/kh")
	secrets := NewSecrets()
	h := &host.Host{
		ID: "h1", Name: "test", Address: "10.0.0.5", User: "root",
		Credential: host.Credential{Kind: host.CredPassword},
	}
	// 无 keyring 密码、无 provided → 应报错
	if _, err := BuildHop(h, secrets, nil, kh, 0); err == nil {
		t.Fatal("password credential without a password must fail")
	}
}

func TestBuildHopKeyFromKeyringPassphrase(t *testing.T) {
	kh, _ := LoadKnownHosts(t.TempDir() + "/kh")
	secrets := NewSecrets()
	h := &host.Host{
		ID: "h1", Name: "test", Address: "10.0.0.5", User: "root",
		Credential: host.Credential{Kind: host.CredKey, KeyPath: "/nonexistent/key"},
	}
	if _, err := BuildHop(h, secrets, nil, kh, 0); err == nil {
		t.Fatal("missing key file must fail")
	} else if !strings.Contains(err.Error(), "key file") {
		t.Fatalf("error should mention the key file: %v", err)
	} else if err == nil {
		t.Fatal("unreachable")
	}
}
