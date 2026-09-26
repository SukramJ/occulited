package acme

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/json"
)

// Request is one run of the ACME flow, everything the issuer needs and nothing of the box.
type Request struct {
	DirectoryURL string
	// Roots is the pool the directory connection trusts (openccu-lite task 231: occulited's
	// store plus the ACME anchors); nil = Go's own roots.
	Roots       *x509.CertPool
	Email       string
	EABKID      string
	EABHMAC     string
	Names       []string
	Challenge   string
	DNSProvider string
	DNSFields   map[string]string
	// AccountKey is the ACME account's key; Registration what the directory answered for it
	// before (nil = register, or find the account by its key).
	AccountKey   *ecdsa.PrivateKey
	Registration json.RawMessage
	// HTTP01 serves the http-01 tokens (the TokenStore behind the open route).
	HTTP01 interface {
		Present(domain, token, keyAuth string) error
		CleanUp(domain, token, keyAuth string) error
	}
	// Log receives the issuer's own lines; nil = silent.
	Log func(line string)
	// LibLog receives lego's own log lines, which the run writes marked as lego's (task 102);
	// nil = Log.
	LibLog func(line string)
}

// Result is what a successful run yields.
type Result struct {
	Certificate []byte // the chain, PEM, leaf first
	PrivateKey  []byte // PEM
	// Registration is the account as the directory knows it now, to be stored for next time.
	Registration json.RawMessage
}

// Issuer runs the flow. The box uses LegoIssuer; the tests a fake.
type Issuer interface {
	Issue(ctx context.Context, req Request) (*Result, error)
}
