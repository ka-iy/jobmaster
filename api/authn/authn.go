package authn

import (
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
)

// The allowed/permitted X.509 certificate crypto.
// TODO: In production, these would be config items.
var (
	// AllowedX509SigAlgos is the list of string representations of the signature
	// algorithms for X509 certificates which are allowed by the jobmaster system.
	AllowedX509SigAlgos = []string{
		x509.ECDSAWithSHA256.String(),
		x509.ECDSAWithSHA384.String(),
		x509.ECDSAWithSHA512.String(),
	}

	// AllowedX509PKAlgos is the list of string representation of the public key
	// algorithms for X509 certificates which are allowed by the jobmaster system.
	AllowedX509PKAlgos = []string{
		x509.ECDSA.String(),
	}
)

// JobmasterEntityCNPrefix is the expected CN prefix in a jobmaster X.509 certificate.
// All jobmaster certificates, whether for clients or servers, are expected to have a
// CN which begins with this prefix. TODO: In production, this would be a config item.
const JobmasterEntityCNPrefix = "jobmaster-"

// ValidateCertificate validates that cert has the expected crypto algorithms.
// Note that the validity (`NotBefore`, `NotAfter`) would be checked by the TLS stack
// during connection setup/handshake.
func ValidateCertCryptoAndValidity(cert *x509.Certificate) error {
	if cert == nil {
		return errors.New("cannot validate a nil certificate")
	}

	sigAlgo := cert.SignatureAlgorithm.String()
	pkAlgo := cert.PublicKeyAlgorithm.String()
	nb := cert.NotBefore.UTC()
	na := cert.NotAfter.UTC()

	log.Printf("Certificate Info: SignatureAlgorithm=%s, PublicKeyAlgorithm=%s, NotBefore=%v, NotAfter=%v\n",
		sigAlgo, pkAlgo, nb, na)

	if !slices.Contains(AllowedX509SigAlgos, sigAlgo) {
		return fmt.Errorf("certificate signature algorithm %q is not permitted", sigAlgo)
	}

	if !slices.Contains(AllowedX509PKAlgos, pkAlgo) {
		return fmt.Errorf("certificate public key algorithm %q is not permitted", pkAlgo)
	}

	// Note: No need to do before/after checks as the TLS handshake checks for it.

	return nil
}

// GetCertCommonName returns the "CommonName" attribute of the certificate's Subject field.
func GetCertCommonName(cert *x509.Certificate) (string, error) {
	cn := cert.Subject.CommonName

	if cn == "" || strings.TrimSpace(cn) == "" {
		return "", errors.New("certicate CN attribute is empty")
	}

	return cn, nil
}

// GetJobmasterEntityNameFromCertCN extracts the name of the server or client from the
// CN of the X.509 certificate. Jobmaster certificates are expected to have a CN which
// starts with the string "jobmaster-" followed by the name of the entity. For example:
// "jobmaster-server1", "jobmaster-client1", etc.
func GetJobmasterEntityNameFromCertCN(cert *x509.Certificate) (string, error) {
	cn, err := GetCertCommonName(cert)
	if err != nil {
		return "", err
	}

	if !strings.HasPrefix(cn, JobmasterEntityCNPrefix) {
		return "", errors.New("certificate invalid: Does not contain expected CN prefix")
	}

	nm := strings.TrimPrefix(cn, JobmasterEntityCNPrefix)
	if nm == "" {
		return "", errors.New("certificate invalid: Does not contain expected CN field part")
	}

	return nm, nil
}
