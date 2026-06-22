/*
Copyright 2026 Anthony Green.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"net"
	"net/url"
	"testing"

	certificatesv1 "k8s.io/api/certificates/v1"
)

const testNodeName = "default-spot-worker01"

func TestShouldApproveKubeletCSR_BootstrapClientCSR(t *testing.T) {
	csr := kubeletCSR(kubeletClientSignerName, "system:bootstrap:abc123",
		[]string{systemBootstrappersGroup},
		csrTemplate(),
		[]certificatesv1.KeyUsage{
			certificatesv1.UsageDigitalSignature,
			certificatesv1.UsageKeyEncipherment,
			certificatesv1.UsageClientAuth,
		})

	if !shouldApproveKubeletCSR(csr, testNodeName) {
		t.Fatal("expected bootstrap client CSR to be approved")
	}
}

func TestShouldApproveKubeletCSR_OpenShiftBootstrapClientCSR(t *testing.T) {
	csr := kubeletCSR(kubeletClientSignerName, openshiftNodeBootstrapper,
		[]string{"system:serviceaccounts", "system:authenticated"},
		csrTemplate(),
		[]certificatesv1.KeyUsage{
			certificatesv1.UsageDigitalSignature,
			certificatesv1.UsageKeyEncipherment,
			certificatesv1.UsageClientAuth,
		})

	if !shouldApproveKubeletCSR(csr, testNodeName) {
		t.Fatal("expected OpenShift node-bootstrapper client CSR to be approved")
	}
}

func TestShouldApproveKubeletCSR_RejectsForgedClientCSRRequester(t *testing.T) {
	csr := kubeletCSR(kubeletClientSignerName, "system:serviceaccount:default:attacker",
		[]string{"system:serviceaccounts", "system:authenticated"},
		csrTemplate(),
		[]certificatesv1.KeyUsage{
			certificatesv1.UsageDigitalSignature,
			certificatesv1.UsageKeyEncipherment,
			certificatesv1.UsageClientAuth,
		})

	if shouldApproveKubeletCSR(csr, testNodeName) {
		t.Fatal("expected forged client CSR requester to be rejected")
	}
}

func TestShouldApproveKubeletCSR_RejectsNodeMismatch(t *testing.T) {
	csr := kubeletCSR(kubeletClientSignerName, "system:node:other-node",
		[]string{systemNodesGroup},
		csrTemplate(),
		[]certificatesv1.KeyUsage{
			certificatesv1.UsageDigitalSignature,
			certificatesv1.UsageKeyEncipherment,
			certificatesv1.UsageClientAuth,
		})

	if shouldApproveKubeletCSR(csr, testNodeName) {
		t.Fatal("expected mismatched node requester to be rejected")
	}
}

func TestShouldApproveKubeletCSR_RejectsWrongClientUsage(t *testing.T) {
	csr := kubeletCSR(kubeletClientSignerName, "system:bootstrap:abc123",
		[]string{systemBootstrappersGroup},
		csrTemplate(),
		[]certificatesv1.KeyUsage{certificatesv1.UsageServerAuth})

	if shouldApproveKubeletCSR(csr, testNodeName) {
		t.Fatal("expected client CSR with server usage to be rejected")
	}
}

func TestShouldApproveKubeletCSR_ServingCSR(t *testing.T) {
	template := csrTemplate()
	template.DNSNames = []string{testNodeName, testNodeName + ".example.com"}
	template.IPAddresses = []net.IP{net.ParseIP("192.0.2.10")}
	csr := kubeletCSR(kubeletServingSignerName, systemNodePrefix+testNodeName,
		[]string{systemNodesGroup},
		template,
		[]certificatesv1.KeyUsage{
			certificatesv1.UsageDigitalSignature,
			certificatesv1.UsageKeyEncipherment,
			certificatesv1.UsageServerAuth,
		})

	if !shouldApproveKubeletCSR(csr, testNodeName) {
		t.Fatal("expected serving CSR to be approved")
	}
}

func TestShouldApproveKubeletCSR_RejectsServingCSRFromBootstrapper(t *testing.T) {
	csr := kubeletCSR(kubeletServingSignerName, "system:bootstrap:abc123",
		[]string{systemBootstrappersGroup},
		csrTemplate(),
		[]certificatesv1.KeyUsage{
			certificatesv1.UsageDigitalSignature,
			certificatesv1.UsageKeyEncipherment,
			certificatesv1.UsageServerAuth,
		})

	if shouldApproveKubeletCSR(csr, testNodeName) {
		t.Fatal("expected serving CSR from bootstrapper to be rejected")
	}
}

func TestShouldApproveKubeletCSR_RejectsServingCSRWithForeignSAN(t *testing.T) {
	template := csrTemplate()
	template.DNSNames = []string{"api.internal.example.com"}
	template.EmailAddresses = []string{"attacker@example.com"}
	template.URIs = []*url.URL{{Scheme: "spiffe", Host: "example.com", Path: "/attacker"}}
	csr := kubeletCSR(kubeletServingSignerName, systemNodePrefix+testNodeName,
		[]string{systemNodesGroup},
		template,
		[]certificatesv1.KeyUsage{
			certificatesv1.UsageDigitalSignature,
			certificatesv1.UsageKeyEncipherment,
			certificatesv1.UsageServerAuth,
		})

	if shouldApproveKubeletCSR(csr, testNodeName) {
		t.Fatal("expected serving CSR with foreign SANs to be rejected")
	}
}

func TestNodeNameFromCSR_FallsBackToCSRCommonName(t *testing.T) {
	csr := kubeletCSR(kubeletClientSignerName, "system:bootstrap:abc123",
		[]string{systemBootstrappersGroup},
		csrTemplate(),
		[]certificatesv1.KeyUsage{certificatesv1.UsageClientAuth})

	if got := nodeNameFromCSR(csr); got != testNodeName {
		t.Fatalf("nodeNameFromCSR() = %q, want %q", got, testNodeName)
	}
}

func kubeletCSR(
	signerName string,
	username string,
	groups []string,
	template *x509.CertificateRequest,
	usages []certificatesv1.KeyUsage,
) *certificatesv1.CertificateSigningRequest {
	return &certificatesv1.CertificateSigningRequest{
		Spec: certificatesv1.CertificateSigningRequestSpec{
			Request:    mustCreateCSR(template),
			SignerName: signerName,
			Usages:     usages,
			Username:   username,
			Groups:     groups,
		},
	}
}

func csrTemplate() *x509.CertificateRequest {
	return &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:   systemNodePrefix + testNodeName,
			Organization: []string{systemNodesGroup},
		},
	}
}

func mustCreateCSR(template *x509.CertificateRequest) []byte {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, template, key)
	if err != nil {
		panic(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
}
