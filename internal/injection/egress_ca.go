// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/omkhar/workcell/internal/credbroker"
	"github.com/omkhar/workcell/internal/egressproxy"
	"github.com/omkhar/workcell/internal/rootio"
	"github.com/omkhar/workcell/internal/shellproto"
)

// maxEgressCABytes bounds the session CA that the sidecar hands back. One
// P-256 certificate in PEM is under 1 KiB.
const maxEgressCABytes = 16 << 10

// BrokeredCredentialKeys returns, sorted, the keys that the credential broker
// serves for the bundle at bundleRoot, from its resolver metadata.
func BrokeredCredentialKeys(bundleRoot string) ([]string, error) {
	broker, err := loadCredentialBroker(filepath.Join(bundleRoot, "resolver-metadata.json"))
	if err != nil {
		return nil, err
	}
	var keys []string
	for key, brokered := range broker.brokered {
		if brokered {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys, nil
}

// CredentialBrokerPlan returns, as shell fields, what the launcher needs to
// broker the session's keys: one terminate=<rule> field per egress proxy
// rule, one credential=<key>=keychain:<item> field per key for
// credential-broker-cli serve, and one audit=<credential_broker_start record>
// field per key.
func CredentialBrokerPlan(bundleRoot, placeholder string) (string, error) {
	if !egressproxy.IsPlaceholder(placeholder) {
		return "", errors.New("credential broker plan: the placeholder must be wcph-<32 hex>")
	}
	keys, err := BrokeredCredentialKeys(bundleRoot)
	if err != nil {
		return "", err
	}
	if len(keys) == 0 {
		return "", errors.New("credential broker plan: the session brokers no credential")
	}
	var fields []shellproto.Field
	for _, key := range keys {
		for _, rule := range credbroker.TerminateRules(key, placeholder) {
			fields = append(fields, shellproto.Field{Key: "terminate", Value: rule})
		}
	}
	for _, key := range keys {
		fields = append(fields, shellproto.Field{Key: "credential", Value: key + "=keychain:" + credbroker.KeychainItem(key)})
		fields = append(fields, shellproto.Field{Key: "audit", Value: credbroker.StartAuditRecord(key)})
	}
	var b strings.Builder
	if err := shellproto.WriteFields(&b, fields); err != nil {
		return "", err
	}
	return b.String(), nil
}

// PublishEgressCA checks the session CA certificate at srcPath and publishes
// it into the bundle at bundleRoot as the file the manifest lists. The file
// comes back from the sidecar, so it is untrusted: it must be exactly one
// unexpired CA certificate that expires within egressproxy.MaxCALifetime and
// may issue only end-entity certificates, with critical name constraints that
// permit only the hosts of the brokered keys.
func PublishEgressCA(srcPath, bundleRoot string) error {
	manifestData, err := rootio.ReadFileNoFollow(filepath.Join(bundleRoot, "manifest.json"), "injection manifest", rootio.MaxManifestBytes)
	if err != nil {
		return err
	}
	var manifest struct {
		Metadata struct {
			EgressCACert string `json:"egress_ca_cert"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return fmt.Errorf("injection manifest: %w", err)
	}
	if manifest.Metadata.EgressCACert != EgressCACertName {
		return errors.New("the injection manifest lists no session CA")
	}
	data, err := rootio.ReadFileNoFollow(srcPath, "session CA", maxEgressCABytes)
	if err != nil {
		return err
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return errors.New("the session CA must be exactly one PEM certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("session CA: %w", err)
	}
	if !cert.IsCA || !cert.MaxPathLenZero || !egressproxy.ValidCAExpiry(cert.NotAfter, time.Now()) {
		return fmt.Errorf("the session CA must be a CA with path length 0 that expires within %s", egressproxy.MaxCALifetime)
	}
	keys, err := BrokeredCredentialKeys(bundleRoot)
	if err != nil {
		return err
	}
	var hosts []string
	for _, key := range keys {
		hosts = append(hosts, credbroker.Hosts(key)...)
	}
	if !cert.PermittedDNSDomainsCritical || len(cert.PermittedDNSDomains) == 0 {
		return errors.New("the session CA must carry critical DNS name constraints")
	}
	for _, domain := range cert.PermittedDNSDomains {
		if !slices.Contains(hosts, domain) {
			return fmt.Errorf("the session CA permits %q, which no brokered key uses", domain)
		}
	}
	if !excludesEveryIP(cert) {
		return errors.New("the session CA must exclude every IPv4 and IPv6 address")
	}
	parent, path, err := rootio.OpenParentDirectoryNoFollow(filepath.Join(bundleRoot, EgressCACertName))
	if err != nil {
		return err
	}
	defer parent.Close()
	return rootio.StageAndPublishAt(parent, filepath.Base(path), pem.EncodeToMemory(block), 0o600, ".egress-ca-")
}

// excludesEveryIP reports whether cert permits no IP range and excludes all of
// IPv4 and IPv6, the constraints egressproxy.NewSessionCA sets, so the CA
// cannot sign a certificate for an IP address.
func excludesEveryIP(cert *x509.Certificate) bool {
	if len(cert.PermittedIPRanges) != 0 {
		return false
	}
	var v4, v6 bool
	for _, r := range cert.ExcludedIPRanges {
		ones, bits := r.Mask.Size()
		if ones != 0 {
			continue
		}
		v4 = v4 || (bits == 32 && r.IP.Equal(net.IPv4zero))
		v6 = v6 || (bits == 128 && r.IP.Equal(net.IPv6zero))
	}
	return v4 && v6
}
