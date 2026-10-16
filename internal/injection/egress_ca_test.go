// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omkhar/workcell/internal/egressproxy"
)

// brokerBundle prepares a real bundle for agent with policy, with or without
// credential brokering, and returns the result.
func brokerBundle(t *testing.T, agent, policy string, broker bool) *PrepareBundleResult {
	t.Helper()
	realHome := t.TempDir()
	workspace := filepath.Join(realHome, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realHome, "key.txt"), []byte("sk-real\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	policyPath := filepath.Join(realHome, "policy.toml")
	if err := os.WriteFile(policyPath, []byte("version = 1\n"+policy), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := PrepareBundle(PrepareBundleOptions{
		Agent: agent, Mode: "strict", WorkspacePath: workspace, PolicyPath: policyPath, RealHome: realHome,
		BrokerCredentials: broker, BundleParentOverride: filepath.Join(realHome, "host-inputs"), ProcessPID: os.Getpid(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

const claudeBrokered = "[credentials.claude_api_key]\nresolver = \"keychain-broker\"\n"

func TestCredentialBrokerPlanComesFromTheGrants(t *testing.T) {
	claude := brokerBundle(t, "claude", claudeBrokered, true)
	p := claude.CredentialPlaceholder
	got, err := CredentialBrokerPlan(claude.InjectionBundleRoot, p)
	want := "terminate=api.anthropic.com=x-api-key=" + p + "\n" +
		"terminate=api.anthropic.com=authorization=" + p + "\n" +
		"credential=claude_api_key=keychain:workcell-claude-api-key\n"
	if err != nil || got != want {
		t.Fatalf("CredentialBrokerPlan = %q, %v; want %q", got, err, want)
	}
	gemini := brokerBundle(t, "gemini", "[credentials.gemini_env]\nresolver = \"keychain-broker\"\n", true)
	got, err = CredentialBrokerPlan(gemini.InjectionBundleRoot, gemini.CredentialPlaceholder)
	want = "terminate=generativelanguage.googleapis.com=x-goog-api-key=" + gemini.CredentialPlaceholder + "\n" +
		"credential=gemini_env=keychain:workcell-gemini-api-key\n"
	if err != nil || got != want {
		t.Fatalf("CredentialBrokerPlan = %q, %v; want %q", got, err, want)
	}
	if _, err := CredentialBrokerPlan(claude.InjectionBundleRoot, "wcph-short"); err == nil {
		t.Error("a bad placeholder was accepted")
	}
	direct := brokerBundle(t, "claude", "[credentials.claude_api_key]\nsource = \"key.txt\"\n", false)
	if _, err := CredentialBrokerPlan(direct.InjectionBundleRoot, p); err == nil {
		t.Error("a bundle that brokers no key got a plan")
	}
}

// selfSigned writes a self-signed certificate made from template.
func selfSigned(t *testing.T, path string, template *x509.Certificate) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template.SerialNumber = big.NewInt(1)
	template.Subject = pkix.Name{CommonName: "test"}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestPublishEgressCAAcceptsOnlyTheSessionCA(t *testing.T) {
	bundle := brokerBundle(t, "claude", claudeBrokered, true).InjectionBundleRoot
	dir := t.TempDir()
	ca, err := egressproxy.NewSessionCA([]string{"api.anthropic.com"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(dir, "good.pem")
	if err := os.WriteFile(good, ca.CertPEM(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PublishEgressCA(good, bundle); err != nil {
		t.Fatal(err)
	}
	published := filepath.Join(bundle, EgressCACertName)
	data, err := os.ReadFile(published)
	if info, statErr := os.Stat(published); err != nil || statErr != nil || string(data) != string(ca.CertPEM()) || info.Mode().Perm() != 0o600 {
		t.Fatalf("published CA = %q (%v, %v), want the session CA with mode 0600", data, err, statErr)
	}

	now := time.Now()
	constrained := func(t *x509.Certificate) *x509.Certificate {
		t.IsCA, t.BasicConstraintsValid, t.MaxPathLenZero = true, true, true
		t.PermittedDNSDomainsCritical, t.PermittedDNSDomains = true, []string{"api.anthropic.com"}
		t.NotBefore, t.NotAfter = now.Add(-time.Hour), now.Add(time.Hour)
		t.KeyUsage = x509.KeyUsageCertSign
		t.ExcludedIPRanges = []*net.IPNet{
			{IP: net.IPv4zero, Mask: net.CIDRMask(0, 32)},
			{IP: net.IPv6zero, Mask: net.CIDRMask(0, 128)},
		}
		return t
	}
	other, _ := egressproxy.NewSessionCA([]string{"example.com"}, now.Add(time.Hour))
	bad := map[string]func(string) error{
		"two certificates": func(p string) error { return os.WriteFile(p, append(ca.CertPEM(), ca.CertPEM()...), 0o600) },
		"trailing bytes":   func(p string) error { return os.WriteFile(p, append(ca.CertPEM(), "junk"...), 0o600) },
		"not PEM":          func(p string) error { return os.WriteFile(p, []byte("junk"), 0o600) },
		"other host":       func(p string) error { return os.WriteFile(p, other.CertPEM(), 0o600) },
		"symlink":          func(p string) error { return os.Symlink(good, p) },
		"not a CA": func(p string) error {
			c := constrained(&x509.Certificate{})
			c.IsCA, c.MaxPathLenZero = false, false
			selfSigned(t, p, c)
			return nil
		},
		"path length": func(p string) error {
			c := constrained(&x509.Certificate{})
			c.MaxPathLenZero, c.MaxPathLen = false, 1
			selfSigned(t, p, c)
			return nil
		},
		"expired": func(p string) error {
			c := constrained(&x509.Certificate{})
			c.NotAfter = now.Add(-time.Minute)
			selfSigned(t, p, c)
			return nil
		},
		"unconstrained": func(p string) error {
			c := constrained(&x509.Certificate{})
			c.PermittedDNSDomainsCritical, c.PermittedDNSDomains = false, nil
			selfSigned(t, p, c)
			return nil
		},
		"excluded only": func(p string) error {
			c := constrained(&x509.Certificate{})
			c.PermittedDNSDomains, c.ExcludedDNSDomains = nil, []string{"example.com"}
			selfSigned(t, p, c)
			return nil
		},
		"other PEM type": func(p string) error {
			block, _ := pem.Decode(ca.CertPEM())
			return os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "TRUSTED CERTIFICATE", Bytes: block.Bytes}), 0o600)
		},
		"basic constraints cA false": func(p string) error {
			// A hand-edited CA: cA FALSE with pathLen 0. The parser does not
			// check the signature, so only the IsCA check refuses it.
			block, _ := pem.Decode(selfSigned(t, p, constrained(&x509.Certificate{})))
			edited := bytes.Replace(block.Bytes, []byte{0x01, 0x01, 0xff, 0x02, 0x01, 0x00}, []byte{0x01, 0x01, 0x00, 0x02, 0x01, 0x00}, 1)
			if bytes.Equal(edited, block.Bytes) {
				t.Fatal("no basic constraints to edit")
			}
			return os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: edited}), 0o600)
		},
		"no IP exclusions": func(p string) error {
			c := constrained(&x509.Certificate{})
			c.ExcludedIPRanges = nil
			selfSigned(t, p, c)
			return nil
		},
		"IPv4 exclusion only": func(p string) error {
			c := constrained(&x509.Certificate{})
			c.ExcludedIPRanges = c.ExcludedIPRanges[:1]
			selfSigned(t, p, c)
			return nil
		},
		"narrow IPv4 exclusion": func(p string) error {
			c := constrained(&x509.Certificate{})
			c.ExcludedIPRanges[0] = &net.IPNet{IP: net.IPv4zero, Mask: net.CIDRMask(8, 32)}
			selfSigned(t, p, c)
			return nil
		},
		"permits an IP range": func(p string) error {
			c := constrained(&x509.Certificate{})
			c.PermittedIPRanges = []*net.IPNet{{IP: net.IPv4(192, 0, 2, 0), Mask: net.CIDRMask(24, 32)}}
			selfSigned(t, p, c)
			return nil
		},
		"not critical": func(p string) error {
			c := constrained(&x509.Certificate{})
			c.PermittedDNSDomainsCritical = false
			selfSigned(t, p, c)
			return nil
		},
	}
	for name, write := range bad {
		src := filepath.Join(dir, name+".pem")
		if err := write(src); err != nil {
			t.Fatal(err)
		}
		if err := PublishEgressCA(src, bundle); err == nil {
			t.Errorf("%s: PublishEgressCA accepted it", name)
		}
	}
	// The control CA passes, so each refusal above is its own check.
	control := filepath.Join(dir, "control.pem")
	selfSigned(t, control, constrained(&x509.Certificate{}))
	if err := PublishEgressCA(control, bundle); err != nil {
		t.Errorf("control CA: %v", err)
	}
	// The same brokering bundle, with the CA dropped from its manifest.
	manifestPath := filepath.Join(bundle, "manifest.json")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	unlisted := bytes.Replace(manifest, []byte(`"egress_ca_cert":"egress-ca.pem"`), []byte(`"egress_ca_cert":""`), 1)
	if bytes.Equal(unlisted, manifest) {
		t.Fatal("the manifest does not list the CA")
	}
	if err := os.WriteFile(manifestPath, unlisted, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PublishEgressCA(good, bundle); err == nil {
		t.Error("a bundle whose manifest lists no session CA got one")
	}
}
